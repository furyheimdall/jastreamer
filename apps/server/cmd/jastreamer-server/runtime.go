package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/airplay"
	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/discovery"
	"github.com/jastreamer/jastreamer-server/internal/dlna"
	"github.com/jastreamer/jastreamer-server/internal/events"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/httpapi"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
	"github.com/jastreamer/jastreamer-server/internal/stream"
	"github.com/jastreamer/jastreamer-server/web/ui"
)

func runServer(parent context.Context, value config.Config, configPath string) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(value.DataDir, 0700); err != nil {
		return err
	}
	db, err := database.Open(filepath.Join(value.DataDir, "server.sqlite"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait(); _ = db.Close() }()
	hub := events.New()
	accounts, err := auth.New(db)
	if err != nil {
		return err
	}
	discoveryService, err := discovery.New(ctx, db, value.ServerName, productVersion)
	if err != nil {
		return err
	}
	roots := make([]library.Root, 0, len(value.LibraryRoots))
	for _, root := range value.LibraryRoots {
		roots = append(roots, library.Root{ID: root.ID, Name: root.Name, Path: root.Path})
	}
	catalog, err := library.New(ctx, db, roots, filepath.Join(value.DataDir, "artwork"), hub.Publish)
	if err != nil {
		return err
	}
	upnp, err := dlna.New(dlna.Config{Interfaces: value.Network.Interfaces, DiscoveryInterval: time.Duration(value.Network.DiscoveryIntervalSeconds) * time.Second, Notify: hub.Publish})
	if err != nil {
		return err
	}
	media, err := stream.New(catalog, stream.Config{BaseURL: mediaOrigin(value), FFmpegPath: value.Media.FFmpegPath, Transcode: value.Media.Transcode})
	if err != nil {
		return err
	}
	backends := []output.Backend{{Protocol: output.ProtocolUPnP, Controller: upnp, Media: media}}
	if value.AirPlay.Enabled {
		airplayManager, airplayErr := airplay.New(db, catalog, airplay.Config{
			Interfaces: value.Network.Interfaces, DiscoveryInterval: time.Duration(value.Network.DiscoveryIntervalSeconds) * time.Second,
			FFmpegPath: value.Media.FFmpegPath, HelperPath: value.AirPlay.HelperPath,
			DataDir: filepath.Join(value.DataDir, "airplay"), Notify: hub.Publish,
		})
		if airplayErr != nil {
			return airplayErr
		}
		backends = append(backends, output.Backend{Protocol: output.ProtocolAirPlay, Controller: airplayManager, Media: airplayManager, Pairer: airplayManager})
	}
	outputs := output.NewManager(backends...)
	playback, err := player.New(ctx, db, catalog, outputs, time.Duration(value.Network.PollIntervalSeconds)*time.Second, hub.Publish)
	if err != nil {
		return err
	}
	hosts := runtimeHosts(value)
	hosts = append(hosts, discoveryService.Hostname(), discoveryService.Hostname()+".")
	var identity tls.Certificate
	if value.HTTPS.Enabled {
		identity, err = tls.LoadX509KeyPair(value.HTTPS.CertificateFile, value.HTTPS.PrivateKeyFile)
		if err != nil {
			return err
		}
		if len(identity.Certificate) > 0 {
			if leaf, parseErr := x509.ParseCertificate(identity.Certificate[0]); parseErr == nil {
				hosts = append(hosts, leaf.DNSNames...)
			}
		}
	}
	handler := httpapi.New(httpapi.Options{Context: ctx, ConfigPath: configPath, Config: value, Auth: accounts, Discovery: discoveryService, Library: catalog, Devices: outputs, Player: playback, Stream: media, Events: hub, UI: ui.Assets(), TrustedHosts: hosts})
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
	for _, endpoint := range []struct {
		enabled bool
		address string
		secure  bool
	}{{value.HTTP.Enabled, value.HTTP.Address, false}, {value.HTTPS.Enabled, value.HTTPS.Address, true}} {
		if !endpoint.enabled {
			continue
		}
		listener, listenErr := net.Listen("tcp", endpoint.address)
		if listenErr != nil {
			return fmt.Errorf("listen %s: %w", endpoint.address, listenErr)
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
	failures := make(chan error, len(bindings)+2)
	launch := func(work func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := work(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
				select {
				case failures <- err:
				default:
				}
			}
		}()
	}
	launch(func() error { return outputs.Run(ctx) })
	launch(func() error { return playback.Run(ctx) })
	for _, bound := range bindings {
		launch(func() error {
			if bound.secure {
				return bound.server.ServeTLS(bound.listener, "", "")
			}
			return bound.server.Serve(bound.listener)
		})
		scheme := "HTTP"
		if bound.secure {
			scheme = "HTTPS"
		}
		log.Printf("%s listening on %s", scheme, bound.listener.Addr())
	}
	if required, setupErr := accounts.NeedsSetup(ctx); setupErr == nil && required {
		log.Print("Initial setup required: create the administrator in the Web interface.")
	}
	select {
	case <-parent.Done():
	case err = <-failures:
	}
	if advertisement != nil {
		advertisement.Close()
		advertisement = nil
	}
	// Stop accepting requests before the bounded, best-effort renderer stop.
	for _, bound := range bindings {
		_ = bound.listener.Close()
	}
	stopCtx, stop := context.WithTimeout(context.Background(), 6*time.Second)
	if state, stateErr := playback.Snapshot(stopCtx); stateErr == nil && state.State != "stopped" && state.RendererID != "" {
		if _, stopErr := playback.Command(stopCtx, player.Command{Action: "stop"}); stopErr == nil {
			ticker := time.NewTicker(100 * time.Millisecond)
		waiting:
			for {
				select {
				case <-stopCtx.Done():
					break waiting
				case <-ticker.C:
					current, currentErr := playback.Snapshot(stopCtx)
					if currentErr != nil || (current.PendingCommand == "" && current.State == "stopped") {
						break waiting
					}
				}
			}
			ticker.Stop()
		}
	}
	stop()
	cancel()
	for _, bound := range bindings {
		_ = bound.server.Close()
	}
	return err
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
		connection, dialErr := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IP(ip.AsSlice()), Port: 1900})
		if dialErr != nil {
			return "", fault.New(409, "RENDERER_UNREACHABLE", "재생 기기로 연결되는 네트워크가 없습니다.")
		}
		local := connection.LocalAddr().(*net.UDPAddr).IP.String()
		_ = connection.Close()
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
		return scheme + "://" + net.JoinHostPort(local, port), nil
	}
}
