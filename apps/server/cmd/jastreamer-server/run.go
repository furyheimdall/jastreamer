package main

import (
	"context"
	"crypto/tls"
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
	manager, err := security.NewManager(security.Config{
		SetupSecret: config.setupSecret, StatePath: filepath.Join(config.dataDirectory, "security", "state.json"), PairingTTL: config.pairingTTL,
	})
	if err != nil {
		return err
	}
	scanner, err := catalog.NewScanner(config.catalogRoot)
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
	scan, err := scanner.Scan(ctx, previousCatalog)
	if err != nil {
		return err
	}
	if err := catalogStore.Save(ctx, scan); err != nil {
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
	handler := api.New(api.Config{
		Security: manager, Queue: queue, Catalog: scan.Snapshot,
		CertificateFingerprint: identity.Fingerprint, ProductVersion: productVersion,
		SourceRevision: resolvedSourceRevision(), Portal: pairing.Assets,
		LoadCatalog: catalogStore.Load, AllowedOrigins: config.allowedOrigins,
		Scan: func(scanContext context.Context, previous catalog.Snapshot) (catalog.Snapshot, error) {
			result, scanErr := scanner.Scan(scanContext, previous)
			if scanErr != nil {
				return catalog.Snapshot{}, scanErr
			}
			if saveErr := catalogStore.Save(scanContext, result); saveErr != nil {
				return catalog.Snapshot{}, saveErr
			}
			analysisProcessor.Notify()
			return result.Snapshot, nil
		},
	})
	server := &http.Server{
		Addr: config.address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity.Certificate}, NextProtos: []string{"http/1.1"}},
	}
	listener, err := net.Listen("tcp", config.address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", config.address, err)
	}
	defer listener.Close()
	errorsChannel := make(chan error, 1)
	go func() {
		if _, writeErr := fmt.Fprintf(os.Stdout, "ready https://%s fingerprint=%s\n", listener.Addr(), identity.Fingerprint); writeErr != nil {
			errorsChannel <- writeErr
			return
		}
		err := server.Serve(tls.NewListener(listener, server.TLSConfig))
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}()
	return waitForServer(ctx, errorsChannel, analysisProcessor.Errors(), server.Shutdown)
}

func waitForServer(ctx context.Context, serverErrors, processorErrors <-chan error, shutdown func(context.Context) error) error {
	select {
	case err := <-serverErrors:
		return err
	case processorErr := <-processorErrors:
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(processorErr, shutdown(shutdownContext))
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return shutdown(shutdownContext)
	}
}
