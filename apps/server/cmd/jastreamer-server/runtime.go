package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/airplay"
	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/browseroutput"
	"github.com/jastreamer/jastreamer-server/internal/cast"
	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/discovery"
	"github.com/jastreamer/jastreamer-server/internal/dlna"
	"github.com/jastreamer/jastreamer-server/internal/downloads"
	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/events"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/httpapi"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
	"github.com/jastreamer/jastreamer-server/internal/stream"
	"github.com/jastreamer/jastreamer-server/web/ui"
)

const (
	runtimeShutdownTimeout = 8 * time.Second
	runtimeWorkersTimeout  = 30 * time.Second
)

type runtimeResult struct {
	restart *httpapi.RestartRequest
	started bool
	err     error
}

func runServer(parent context.Context, initial config.Config, configPath string) error {
	active := initial
	restartError := ""
	var recoveryConfig config.Config
	var restartFailure error
	startingRestart := false
	recovering := false
	for {
		result := runRuntime(parent, active, configPath, restartError)
		if result.restart != nil {
			if result.err != nil {
				return fmt.Errorf("shut down previous runtime for restart: %w", result.err)
			}
			saved, err := config.Load(configPath)
			if err != nil || !reflect.DeepEqual(saved, result.restart.Config) {
				if err != nil {
					restartError = fmt.Sprintf("Restart canceled because the saved configuration could not be reloaded: %v", err)
				} else {
					restartError = "Restart canceled because the saved configuration changed after the request was accepted."
				}
				active = result.restart.Active
				startingRestart = false
				recovering = false
				continue
			}
			recoveryConfig = result.restart.Active
			active = result.restart.Config
			restartError = ""
			restartFailure = nil
			startingRestart = true
			recovering = false
			continue
		}
		if result.err == nil {
			return nil
		}
		if startingRestart && !result.started && parent.Err() == nil {
			restartFailure = result.err
			restartError = fmt.Sprintf("The restarted server failed to start: %v", result.err)
			active = recoveryConfig
			startingRestart = false
			recovering = true
			continue
		}
		if recovering && !result.started && parent.Err() == nil {
			return errors.Join(
				fmt.Errorf("start restarted server: %w", restartFailure),
				fmt.Errorf("recover previous server configuration: %w", result.err),
			)
		}
		return result.err
	}
}

func runRuntime(parent context.Context, value config.Config, configPath, restartError string) (result runtimeResult) {
	if err := parent.Err(); err != nil {
		result.err = err
		return result
	}
	runtimeID, err := newRuntimeID()
	if err != nil {
		result.err = err
		return result
	}
	if err = os.MkdirAll(value.DataDir, 0700); err != nil {
		result.err = err
		return result
	}
	stopLogging, err := startDiagnosticLogging(value.DataDir)
	if err != nil {
		result.err = err
		return result
	}
	defer stopLogging()
	db, err := database.Open(filepath.Join(value.DataDir, "server.sqlite"))
	if err != nil {
		result.err = err
		return result
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	var workers sync.WaitGroup
	var catalog *library.Service
	var downloadService *downloads.Service
	failures := make(chan error, len(runtimeEndpoints(value))+2)
	defer func() {
		cancel()
		if downloadService != nil {
			downloadService.Close()
		}
		if catalog != nil {
			waitCtx, stopWaiting := context.WithTimeout(context.Background(), runtimeWorkersTimeout)
			if waitErr := catalog.WaitVerification(waitCtx); waitErr != nil {
				result.err = errors.Join(result.err, fmt.Errorf("stop library verification: %w", waitErr))
			}
			stopWaiting()
		}
		if waitErr := waitRuntimeWorkers(&workers, failures, runtimeWorkersTimeout); waitErr != nil {
			result.err = errors.Join(result.err, waitErr)
		}
		if closeErr := db.Close(); closeErr != nil {
			result.err = errors.Join(result.err, fmt.Errorf("close runtime database: %w", closeErr))
		}
	}()
	hub := events.New()
	history, err := errorhistory.New(ctx, db, hub.Publish)
	if err != nil {
		result.err = err
		return result
	}
	accounts, err := auth.New(db)
	if err != nil {
		result.err = err
		return result
	}
	discoveryService, err := discovery.New(ctx, db, value.ServerName, productVersion)
	if err != nil {
		result.err = err
		return result
	}
	roots := make([]library.Root, 0, len(value.LibraryRoots))
	for _, root := range value.LibraryRoots {
		roots = append(roots, library.Root{ID: root.ID, Name: root.Name, Path: root.Path})
	}
	catalog, err = library.New(ctx, db, roots, filepath.Join(value.DataDir, "artwork"), hub.Publish)
	if err != nil {
		result.err = err
		return result
	}
	downloadService, err = downloads.New(ctx, db, catalog, filepath.Join(value.DataDir, "downloads"), value.Media.FFmpegPath)
	if err != nil {
		result.err = err
		return result
	}
	if importErr := browseroutput.ImportErrorHistory(ctx, history, value.DataDir); importErr != nil {
		log.Printf("diagnostic component=history event=log_import_incomplete error_type=%T", importErr)
	}
	upnp, err := dlna.New(dlna.Config{Interfaces: value.Network.Interfaces, DiscoveryInterval: time.Duration(value.Network.DiscoveryIntervalSeconds) * time.Second, Notify: hub.Publish})
	if err != nil {
		result.err = err
		return result
	}
	media, err := stream.New(catalog, stream.Config{
		BaseURL: mediaOrigin(value), FFmpegPath: value.Media.FFmpegPath, Transcode: value.Media.Transcode,
		BrowserAuthorized: func(request *http.Request) bool {
			cookie, cookieErr := request.Cookie("jastreamer_session")
			if cookieErr != nil {
				return false
			}
			_, validateErr := accounts.Validate(request.Context(), cookie.Value)
			return validateErr == nil
		},
	})
	if err != nil {
		result.err = err
		return result
	}
	browser, err := browseroutput.New(media, hub.Publish, history)
	if err != nil {
		result.err = err
		return result
	}
	backends := []output.Backend{
		{Protocol: output.ProtocolUPnP, Controller: upnp, Media: media},
		{Protocol: output.ProtocolBrowser, Controller: browser, Media: browser},
	}
	if value.Cast.Enabled {
		castManager, castErr := cast.New(cast.Config{
			Interfaces: value.Network.Interfaces, DiscoveryInterval: time.Duration(value.Network.DiscoveryIntervalSeconds) * time.Second,
			Notify: hub.Publish,
		})
		if castErr != nil {
			result.err = castErr
			return result
		}
		backends = append(backends, output.Backend{Protocol: output.ProtocolCast, Controller: castManager, Media: media})
	}
	if value.AirPlay.Enabled {
		airplayManager, airplayErr := airplay.New(db, catalog, airplay.Config{
			Interfaces: value.Network.Interfaces, DiscoveryInterval: time.Duration(value.Network.DiscoveryIntervalSeconds) * time.Second,
			FFmpegPath: value.Media.FFmpegPath, HelperPath: value.AirPlay.HelperPath,
			DataDir: filepath.Join(value.DataDir, "airplay"), Notify: hub.Publish,
		})
		if airplayErr != nil {
			result.err = airplayErr
			return result
		}
		backends = append(backends, output.Backend{Protocol: output.ProtocolAirPlay, Controller: airplayManager, Media: airplayManager, Pairer: airplayManager})
	}
	outputs := output.NewManager(backends...)
	playback, err := player.New(ctx, db, catalog, outputs, time.Duration(value.Network.PollIntervalSeconds)*time.Second, hub.Publish, history)
	if err != nil {
		result.err = err
		return result
	}
	if err = catalog.StartVerification(library.VerificationOptions{
		FFmpegPath: value.Media.FFmpegPath,
		History:    history,
		IsPlaybackActive: func() bool {
			probeCtx, stop := context.WithTimeout(ctx, time.Second)
			defer stop()
			active, probeErr := playback.IsPlaybackActive(probeCtx)
			return probeErr != nil || active
		},
	}); err != nil {
		result.err = err
		return result
	}
	hosts := runtimeHosts(value)
	hosts = append(hosts, discoveryService.Hostname(), discoveryService.Hostname()+".")
	var identity tls.Certificate
	if value.HTTPS.Enabled {
		identity, err = tls.LoadX509KeyPair(value.HTTPS.CertificateFile, value.HTTPS.PrivateKeyFile)
		if err != nil {
			result.err = err
			return result
		}
		if len(identity.Certificate) > 0 {
			if leaf, parseErr := x509.ParseCertificate(identity.Certificate[0]); parseErr == nil {
				hosts = append(hosts, leaf.DNSNames...)
			}
		}
	}
	var lifecycleMu sync.Mutex
	var shuttingDown atomic.Bool
	restartRequests := make(chan httpapi.RestartRequest, 1)
	restartHooks := &httpapi.RestartHooks{
		Prepare: func(prepareCtx context.Context, target config.Config) error {
			lifecycleMu.Lock()
			defer lifecycleMu.Unlock()
			if shuttingDown.Load() || parent.Err() != nil || ctx.Err() != nil {
				return fault.New(http.StatusServiceUnavailable, "RESTART_UNAVAILABLE", "The current server runtime is shutting down.")
			}
			if err := preflightRestart(value, target, db, catalog); err != nil {
				return fault.New(http.StatusConflict, "RESTART_PREFLIGHT_FAILED", fmt.Sprintf("Restart preflight failed: %v", err))
			}
			if shuttingDown.Load() || parent.Err() != nil {
				return fault.New(http.StatusServiceUnavailable, "RESTART_UNAVAILABLE", "The current server runtime began shutting down during restart preparation.")
			}
			if err := playback.StopForRestart(prepareCtx); err != nil {
				return fault.New(http.StatusConflict, "RESTART_STOP_FAILED", err.Error())
			}
			if shuttingDown.Load() || parent.Err() != nil {
				return fault.New(http.StatusServiceUnavailable, "RESTART_UNAVAILABLE", "The current server runtime began shutting down during restart preparation.")
			}
			return nil
		},
		Commit: func(request httpapi.RestartRequest) {
			restartRequests <- request
		},
	}
	handler := httpapi.New(httpapi.Options{
		Context: ctx, ConfigPath: configPath, Config: value, RuntimeID: runtimeID,
		RestartError: restartError, Restart: restartHooks, Auth: accounts,
		Discovery: discoveryService, Library: catalog, Downloads: downloadService, Devices: outputs, Player: playback,
		Browser: browser, Stream: media, Events: hub, History: history, UI: ui.Assets(), TrustedHosts: hosts,
	})
	type binding struct {
		listener net.Listener
		server   *http.Server
		secure   bool
	}
	var bindings []binding
	defer func() {
		for _, bound := range bindings {
			_ = bound.listener.Close()
			_ = bound.server.Close()
		}
	}()
	for _, endpoint := range runtimeEndpoints(value) {
		if !endpoint.enabled {
			continue
		}
		listener, listenErr := net.Listen("tcp", endpoint.address)
		if listenErr != nil {
			result.err = fmt.Errorf("listen %s: %w", endpoint.address, listenErr)
			return result
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
		if endpoint.secure {
			server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{identity}}
		}
		bindings = append(bindings, binding{listener: listener, server: server, secure: endpoint.secure})
	}
	var advertisedBinding *binding
	for index := range bindings {
		if advertisedBinding == nil || bindings[index].secure {
			advertisedBinding = &bindings[index]
		}
		if bindings[index].secure {
			break
		}
	}
	var advertisement *discovery.Advertisement
	if advertisedBinding != nil {
		scheme := "http"
		if advertisedBinding.secure {
			scheme = "https"
		}
		advertisement, err = discoveryService.Advertise(advertisedBinding.listener, scheme, value.Network.Interfaces)
		if err != nil {
			log.Printf("Warning: LAN discovery is unavailable: %v", err)
			err = nil
		} else {
			log.Printf("LAN discovery advertising %s on %s", scheme, advertisedBinding.listener.Addr())
		}
	}
	defer func() {
		if advertisement != nil {
			advertisement.Close()
		}
	}()
	launch := func(work func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := work(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
				failures <- err
			}
		}()
	}
	launch(func() error { return outputs.Run(ctx) })
	launch(func() error { return playback.Run(ctx) })
	for _, bound := range bindings {
		launch(func() error {
			var serveErr error
			if bound.secure {
				serveErr = bound.server.ServeTLS(bound.listener, "", "")
			} else {
				serveErr = bound.server.Serve(bound.listener)
			}
			if shuttingDown.Load() && errors.Is(serveErr, net.ErrClosed) {
				return nil
			}
			return serveErr
		})
		scheme := "HTTP"
		if bound.secure {
			scheme = "HTTPS"
		}
		log.Printf("%s listening on %s", scheme, bound.listener.Addr())
	}
	result.started = true
	log.Printf("diagnostic component=server event=runtime_started runtime_id=%q source_revision=%q", runtimeID, resolvedSourceRevision())
	if required, setupErr := accounts.NeedsSetup(ctx); setupErr == nil && required {
		log.Print("Initial setup required: create the administrator in the Web interface.")
	}
	stopReason := "signal"
	select {
	case <-parent.Done():
		shuttingDown.Store(true)
	case result.err = <-failures:
		stopReason = "runtime_failure"
		shuttingDown.Store(true)
	case request := <-restartRequests:
		stopReason = "configuration_restart"
		result.restart = &request
	}
	shuttingDown.Store(true)
	log.Printf("diagnostic component=server event=runtime_stopping runtime_id=%q reason=%q error_type=%T", runtimeID, stopReason, result.err)
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	if advertisement != nil {
		advertisement.Close()
		advertisement = nil
	}
	if result.restart == nil {
		stopCtx, stop := context.WithTimeout(context.Background(), runtimeShutdownTimeout)
		stopErr := playback.StopForRestart(stopCtx)
		stop()
		if stopErr != nil {
			if parent.Err() != nil {
				log.Printf("Playback stop during process shutdown was not confirmed: %v", stopErr)
			} else {
				result.err = errors.Join(result.err, fmt.Errorf("stop playback during runtime shutdown: %w", stopErr))
			}
		}
	}
	for _, bound := range bindings {
		_ = bound.listener.Close()
	}
	cancel()
	shutdownCtx, shutdown := context.WithTimeout(context.Background(), runtimeShutdownTimeout)
	for _, bound := range bindings {
		if shutdownErr := shutdownRuntimeHTTP(shutdownCtx, bound.server); shutdownErr != nil {
			result.err = errors.Join(result.err, fmt.Errorf("shut down HTTP server: %w", shutdownErr))
		}
	}
	shutdown()
	return result
}

type runtimeEndpoint struct {
	enabled bool
	address string
	secure  bool
}

func runtimeEndpoints(value config.Config) []runtimeEndpoint {
	return []runtimeEndpoint{
		{enabled: value.HTTP.Enabled, address: value.HTTP.Address},
		{enabled: value.HTTPS.Enabled, address: value.HTTPS.Address, secure: true},
	}
}

func preflightRestart(active, target config.Config, db *sql.DB, catalog *library.Service) error {
	if err := config.Validate(target); err != nil {
		return err
	}
	if target.DataDir != active.DataDir {
		return fmt.Errorf("data directory migration is required")
	}
	if target.HTTPS.Enabled {
		if _, err := tls.LoadX509KeyPair(target.HTTPS.CertificateFile, target.HTTPS.PrivateKeyFile); err != nil {
			return fmt.Errorf("load HTTPS identity: %w", err)
		}
	}
	if _, err := dlna.New(dlna.Config{Interfaces: target.Network.Interfaces, DiscoveryInterval: time.Duration(target.Network.DiscoveryIntervalSeconds) * time.Second}); err != nil {
		return fmt.Errorf("initialize UPnP output: %w", err)
	}
	if target.Cast.Enabled {
		if _, err := cast.New(cast.Config{Interfaces: target.Network.Interfaces, DiscoveryInterval: time.Duration(target.Network.DiscoveryIntervalSeconds) * time.Second}); err != nil {
			return fmt.Errorf("initialize Cast output: %w", err)
		}
	}
	if target.AirPlay.Enabled {
		if _, err := airplay.New(db, catalog, airplay.Config{
			Interfaces: target.Network.Interfaces, DiscoveryInterval: time.Duration(target.Network.DiscoveryIntervalSeconds) * time.Second,
			FFmpegPath: target.Media.FFmpegPath, HelperPath: target.AirPlay.HelperPath,
			DataDir: filepath.Join(target.DataDir, "airplay"),
		}); err != nil {
			return fmt.Errorf("initialize AirPlay output: %w", err)
		}
	}
	return preflightListeners(active, target)
}

func preflightListeners(active, target config.Config) error {
	current := runtimeEndpoints(active)
	var probes []net.Listener
	defer func() {
		for _, probe := range probes {
			_ = probe.Close()
		}
	}()
	var admitted []string
	for _, endpoint := range runtimeEndpoints(target) {
		if !endpoint.enabled {
			continue
		}
		for _, address := range admitted {
			if listenerAddressesOverlap(address, endpoint.address) {
				return fmt.Errorf("target listeners overlap at %s and %s", address, endpoint.address)
			}
		}
		admitted = append(admitted, endpoint.address)
		reused := false
		for _, existing := range current {
			if existing.enabled && listenerAddressesOverlap(existing.address, endpoint.address) {
				reused = true
				break
			}
		}
		if reused {
			continue
		}
		probe, err := net.Listen("tcp", endpoint.address)
		if err != nil {
			return fmt.Errorf("listen %s: %w", endpoint.address, err)
		}
		probes = append(probes, probe)
	}
	return nil
}

func listenerAddressesOverlap(left, right string) bool {
	leftHost, leftPort, leftErr := net.SplitHostPort(left)
	rightHost, rightPort, rightErr := net.SplitHostPort(right)
	if leftErr != nil || rightErr != nil || leftPort != rightPort {
		return false
	}
	if wildcardListenerHost(leftHost) || wildcardListenerHost(rightHost) {
		return true
	}
	if strings.EqualFold(leftHost, rightHost) {
		return true
	}
	leftIP, rightIP := net.ParseIP(leftHost), net.ParseIP(rightHost)
	if strings.EqualFold(leftHost, "localhost") && rightIP != nil && rightIP.IsLoopback() ||
		strings.EqualFold(rightHost, "localhost") && leftIP != nil && leftIP.IsLoopback() {
		return true
	}
	return leftIP != nil && rightIP != nil && leftIP.Equal(rightIP)
}

func wildcardListenerHost(host string) bool {
	ip := net.ParseIP(host)
	return host == "" || ip != nil && ip.IsUnspecified()
}

func shutdownRuntimeHTTP(ctx context.Context, server *http.Server) error {
	err := server.Shutdown(ctx)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	closeErr := server.Close()
	if closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
		return errors.Join(err, closeErr)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func waitRuntimeWorkers(workers *sync.WaitGroup, failures <-chan error, timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		var result error
		for {
			select {
			case err, ok := <-failures:
				if !ok {
					return result
				}
				result = errors.Join(result, err)
			default:
				return result
			}
		}
	case <-timer.C:
		return fmt.Errorf("runtime workers did not stop within %s", timeout)
	}
}

func newRuntimeID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create runtime identity: %w", err)
	}
	return "runtime_" + hex.EncodeToString(value[:]), nil
}

func runtimeHosts(value config.Config) []string {
	var hosts []string
	if name, err := os.Hostname(); err == nil {
		hosts = append(hosts, name, name+".local")
	}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, raw := range addresses {
			if prefix, err := netip.ParsePrefix(raw.String()); err == nil {
				hosts = append(hosts, prefix.Addr().Unmap().String())
			}
		}
	}
	for _, address := range []string{value.HTTP.Address, value.HTTPS.Address} {
		if host, _, err := net.SplitHostPort(address); err == nil && host != "" && host != "0.0.0.0" && host != "::" {
			hosts = append(hosts, host)
		}
	}
	if address, err := url.Parse(value.Media.BaseURL); err == nil && address.Hostname() != "" {
		hosts = append(hosts, address.Hostname())
	}
	return hosts
}

func mediaOrigin(value config.Config) func(output.Device) (string, error) {
	return func(device output.Device) (string, error) {
		if value.Media.BaseURL != "" {
			return strings.TrimRight(value.Media.BaseURL, "/"), nil
		}
		scheme, address := "http", value.HTTP.Address
		if !value.HTTP.Enabled {
			scheme, address = "https", value.HTTPS.Address
		}
		listenHost, port, err := net.SplitHostPort(address)
		if err != nil {
			return "", fault.New(500, "MEDIA_ADDRESS_INVALID", "미디어 수신 주소를 확인하세요.")
		}
		remote := device.Address
		if endpoint, parseErr := url.Parse(remote); parseErr == nil && endpoint.Host != "" {
			remote = endpoint.Hostname()
		} else if host, _, splitErr := net.SplitHostPort(remote); splitErr == nil {
			remote = host
		}
		ip, parseErr := netip.ParseAddr(strings.Trim(remote, "[]"))
		if parseErr != nil {
			return "", fault.New(409, "RENDERER_ADDRESS_UNKNOWN", "재생 기기의 네트워크 주소를 확인하지 못했습니다.")
		}
		// Reuse the interface bound by discovery/control instead of an unrelated
		// default (for example VPN) route when the output backend provides it.
		local := device.LocalAddress
		if listenHost != "" && listenHost != "0.0.0.0" && listenHost != "::" {
			if bound, err := netip.ParseAddr(listenHost); err == nil {
				if bound.IsLoopback() && !ip.IsLoopback() {
					return "", fault.New(409, "MEDIA_LISTENER_LOCAL_ONLY", "HTTP 수신 주소를 사설망 기기에서 접근할 수 있도록 설정하세요.")
				}
				local = bound.String()
			} else {
				local = listenHost
			}
		}
		if local == "" {
			connection, dialErr := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IP(ip.AsSlice()), Port: 1900})
			if dialErr != nil {
				return "", fault.New(409, "RENDERER_UNREACHABLE", "재생 기기로 연결되는 네트워크가 없습니다.")
			}
			local = connection.LocalAddr().(*net.UDPAddr).IP.String()
			_ = connection.Close()
		}
		return scheme + "://" + net.JoinHostPort(local, port), nil
	}
}
