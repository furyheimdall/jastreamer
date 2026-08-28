package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/settings"
	"github.com/jastreamer/jastreamer-server/web/pairing"
)

func run(ctx context.Context, config serverConfig) (err error) {
	if err := os.MkdirAll(config.catalogRoot, 0o750); err != nil {
		return fmt.Errorf("create catalog root: %w", err)
	}
	identity, err := security.LoadOrCreateIdentity(security.IdentityConfig{
		Directory: filepath.Join(config.dataDirectory, "identity"), DNSNames: config.certificateDNS,
		IPAddresses: config.certificateIPs,
	})
	if err != nil {
		return err
	}
	settingsConfig, err := runtimeSettingsConfig(config, identity.Fingerprint)
	if err != nil {
		return err
	}
	settingsStore, err := settings.Open(settingsConfig)
	if err != nil {
		return fmt.Errorf("open settings: %w", err)
	}
	runtimeSettings := settingsStore.Snapshot()
	manager, err := security.NewManager(security.Config{
		SetupSecret: config.setupSecret, StatePath: filepath.Join(config.dataDirectory, "security", "state.json"),
		PairingTTL: time.Duration(runtimeSettings.Settings.PairingTTLSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	catalogSchema, err := os.ReadFile(config.catalogMigrationPath)
	if err != nil {
		return fmt.Errorf("read catalog migration: %w", err)
	}
	catalogStore, err := catalog.OpenStore(ctx, catalog.StoreConfig{
		Path: filepath.Join(config.dataDirectory, "catalog.sqlite"), Root: config.catalogRoot,
		Schema: string(catalogSchema), Now: time.Now,
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, catalogStore.Close()) }()
	previousCatalog, err := catalogStore.Load(ctx)
	if err != nil {
		return err
	}
	analysisProcessor := catalog.NewProcessor(catalogStore, 2)
	analysisProcessor.Start()
	defer analysisProcessor.Close()
	queue, err := playback.Open(ctx, playback.Config{
		Path: filepath.Join(config.dataDirectory, "playback.sqlite"), MigrationPath: config.playbackMigrationPath,
		ExpansionPath: config.playbackExpansionPath, BackupDirectory: filepath.Join(config.dataDirectory, "backups"),
		SupportedSchema: playback.CurrentSchemaVersion,
		JournalMode:     playback.JournalRollback,
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, queue.Close()) }()
	transformer, err := configureFFmpeg(ctx, ffmpegRuntimeConfig{
		path: runtimeSettings.Settings.FFmpegPath, probes: queue, settings: settingsStore,
	})
	if err != nil {
		return err
	}
	if transformer != nil {
		defer func() { err = errors.Join(err, transformer.Close()) }()
	}
	runtimeContext, stopRuntime := context.WithCancel(ctx)
	defer stopRuntime()
	coordinator, err := catalog.OpenCoordinator(runtimeContext, catalog.CoordinatorConfig{
		StatePath:    filepath.Join(config.dataDirectory, "catalog", "coordinator.json"),
		AllowedBases: runtimeSettings.Locks.AllowedCatalogBases, Now: time.Now, InitialSnapshot: previousCatalog,
		Scan: func(scanContext context.Context, root catalog.Root, previous catalog.Snapshot) (catalog.ScanResult, error) {
			result, scanErr := catalog.ScanRoot(scanContext, root, previous)
			if scanErr != nil {
				return catalog.ScanResult{}, scanErr
			}
			if root.CanonicalPath == settingsConfig.Defaults.CatalogRoots[0].Path {
				if saveErr := catalogStore.Save(scanContext, result); saveErr != nil {
					return catalog.ScanResult{}, saveErr
				}
				analysisProcessor.Notify()
			}
			return result, nil
		},
	})
	if err != nil {
		stopRuntime()
		return fmt.Errorf("open catalog coordinator: %w", err)
	}
	desiredRoots := make([]catalog.DesiredRoot, len(runtimeSettings.Settings.CatalogRoots))
	for index, root := range runtimeSettings.Settings.CatalogRoots {
		desiredRoots[index] = catalog.DesiredRoot{ID: catalog.RootID(root.ID), DisplayName: root.DisplayName, Path: root.Path}
	}
	if err := coordinator.ReconcileRoots(ctx, desiredRoots); err != nil {
		stopRuntime()
		_ = coordinator.Close()
		return fmt.Errorf("reconcile catalog roots: %w", err)
	}
	mediaService, err := configureMediaService(ctx, mediaRuntimeConfig{
		certificate: identity.Certificate, fingerprint: identity.Fingerprint,
		queue: queue, catalog: coordinator, transformer: transformer,
	})
	if err != nil {
		return err
	}
	eventEpoch, err := newEventEpoch()
	if err != nil {
		stopRuntime()
		_ = coordinator.Close()
		return err
	}
	upnpService, err := newK17UPnP(runtimeSettings.Settings, queue)
	if err != nil {
		return fmt.Errorf("configure K17 UPnP: %w", err)
	}
	var mediaServer *http.Server
	var mediaListener net.Listener
	k17MediaBaseURL := ""
	k17MediaListenerAddress := ""
	if runtimeSettings.Settings.K17HTTP.Enabled {
		mediaListener, err = net.Listen("tcp", runtimeSettings.Settings.K17HTTP.ListenerAddress)
		if err != nil {
			return fmt.Errorf("listen K17 media %s: %w", runtimeSettings.Settings.K17HTTP.ListenerAddress, err)
		}
		defer mediaListener.Close()
		k17MediaListenerAddress = mediaListener.Addr().String()
		k17MediaBaseURL = "http://" + k17MediaListenerAddress
		mediaServer = &http.Server{
			Addr: mediaListener.Addr().String(), Handler: api.MediaOnly(mediaService),
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second,
		}
	}
	listener, err := net.Listen("tcp", config.address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", config.address, err)
	}
	defer listener.Close()
	certificate, err := x509.ParseCertificate(identity.Certificate.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse server HTTPS certificate: %w", err)
	}
	serverHTTPSOrigin, err := serverHTTPSOriginFromListener(serverHTTPSOriginConfig{
		Listener: listener.Addr(), Certificate: certificate,
		K17Interfaces: runtimeSettings.Settings.UPnPInterfaces, Enumerate: systemInterfaceAddresses,
	})
	if err != nil {
		return err
	}
	handler := api.New(api.Config{
		Security: manager, Queue: queue, Settings: settingsStore, Catalog: coordinator.Snapshot(),
		CatalogCoordinator: coordinator, Context: runtimeContext, EventEpoch: eventEpoch,
		CatalogSnapshot:        func(context.Context) catalog.Snapshot { return coordinator.Snapshot() },
		CertificateFingerprint: identity.Fingerprint, ProductVersion: productVersion,
		SourceRevision: resolvedSourceRevision(), Portal: pairing.Assets,
		AllowedOrigins: runtimeSettings.Settings.ControlOrigins, Media: mediaService, ServerHTTPSOrigin: serverHTTPSOrigin,
		UPnP: upnpService, K17HTTPEnabled: runtimeSettings.Settings.K17HTTP.Enabled, K17MediaBaseURL: k17MediaBaseURL,
		K17MediaListenerAddress: k17MediaListenerAddress,
	})
	server := &http.Server{
		Addr: config.address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity.Certificate}, NextProtos: []string{"http/1.1"}},
	}
	errorsChannel := make(chan error, 3)
	ready := make(chan struct{})
	if upnpService != nil {
		startK17Lifecycle(runtimeContext, k17LifecycleRuntime{
			service: upnpService, store: queue, media: mediaService, errors: errorsChannel,
			compatibilityURL: k17MediaBaseURL, serverHTTPSOrigin: serverHTTPSOrigin,
		})
	}
	if mediaServer != nil {
		go func() {
			serveErr := mediaServer.Serve(mediaListener)
			if errors.Is(serveErr, http.ErrServerClosed) {
				serveErr = nil
			}
			errorsChannel <- serveErr
		}()
	}
	go func() {
		if _, writeErr := fmt.Fprintf(os.Stdout, "ready https://%s fingerprint=%s\n", listener.Addr(), identity.Fingerprint); writeErr != nil {
			errorsChannel <- writeErr
			return
		}
		close(ready)
		err := server.Serve(tls.NewListener(listener, server.TLSConfig))
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}()
	select {
	case <-ready:
	case serveErr := <-errorsChannel:
		stopRuntime()
		_ = coordinator.Close()
		return serveErr
	}
	scansDone := startInitialScans(runtimeContext, coordinator)
	defer func() {
		stopRuntime()
		err = errors.Join(err, coordinator.Close())
		<-scansDone
	}()
	return waitForServer(ctx, errorsChannel, analysisProcessor.Errors(), func(shutdownContext context.Context) error {
		stopRuntime()
		shutdownErr := server.Shutdown(shutdownContext)
		if mediaServer != nil {
			shutdownErr = errors.Join(shutdownErr, mediaServer.Shutdown(shutdownContext))
		}
		return shutdownErr
	})
}
