package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

func TestMediaOriginUsesDiscoveredInterfaceBeforeDefaultRoute(t *testing.T) {
	value := config.Default()
	value.HTTP.Address = ":8080"
	device := output.Device{Address: "127.0.0.1", LocalAddress: "127.0.0.2", Protocol: output.ProtocolUPnP}
	origin, err := mediaOrigin(value)(device)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "http://127.0.0.2:8080" {
		t.Fatalf("media origin ignored the discovered interface: %s", origin)
	}
}

func TestExplicitMediaOriginAndListenerOverrideDiscoveredInterface(t *testing.T) {
	device := output.Device{Address: "127.0.0.1", LocalAddress: "127.0.0.2", Protocol: output.ProtocolUPnP}
	value := config.Default()
	value.HTTP.Address = "127.0.0.3:8080"
	origin, err := mediaOrigin(value)(device)
	if err != nil || origin != "http://127.0.0.3:8080" {
		t.Fatalf("media URL did not respect the bound listener: origin=%s err=%v", origin, err)
	}
	value.Media.BaseURL = "https://127.0.0.4:9443"
	origin, err = mediaOrigin(value)(device)
	if err != nil || origin != value.Media.BaseURL {
		t.Fatalf("explicit media origin did not take precedence: origin=%s err=%v", origin, err)
	}
}

func TestRestartListenerPreflightSkipsOnlyCurrentBindings(t *testing.T) {
	current, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	active := config.Default()
	active.HTTP.Address = current.Addr().String()
	target := active
	if err = preflightListeners(active, target); err != nil {
		t.Fatalf("preflight tried to double-bind the current listener: %v", err)
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	target.HTTP.Address = occupied.Addr().String()
	if err = preflightListeners(active, target); err == nil || !strings.Contains(err.Error(), occupied.Addr().String()) {
		t.Fatalf("preflight accepted a listener owned by another process: %v", err)
	}
}

func TestRestartListenerPreflightRejectsOverlappingTargets(t *testing.T) {
	active := config.Default()
	target := active
	target.HTTP.Address = "127.0.0.1:19090"
	target.HTTPS.Enabled = true
	target.HTTPS.Address = target.HTTP.Address
	if err := preflightListeners(active, target); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("preflight did not identify overlapping HTTP and HTTPS listeners: %v", err)
	}
}

type runtimeZeroReader struct{}

func (runtimeZeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestRuntimeHTTPShutdownClosesBlockedResponses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		_, writeErr := io.CopyN(writer, runtimeZeroReader{}, 32<<20)
		finished <- writeErr
	})}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	connection, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(connection, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP response did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := shutdownRuntimeHTTP(ctx, server); err != nil {
		t.Fatalf("blocked read-only client prevented runtime replacement: %v", err)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("slow response did not exercise forced connection closure")
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("unread response unexpectedly completed instead of being interrupted")
		}
	case <-time.After(time.Second):
		t.Fatal("forced shutdown left the response writer blocked")
	}
}

type stalledRuntimeBackend struct {
	output.Controller
	started chan struct{}
	release chan struct{}
	exited  chan struct{}
}

func (backend *stalledRuntimeBackend) Run(context.Context) error {
	close(backend.started)
	<-backend.release
	close(backend.exited)
	return nil
}

func TestRuntimeWorkerDrainRejectsLiveBackendAfterShutdownTimeout(t *testing.T) {
	backend := &stalledRuntimeBackend{
		started: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}),
	}
	defer close(backend.release)
	manager := output.NewManager(output.Backend{Protocol: output.ProtocolUPnP, Controller: backend})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := manager.Run(ctx); err != nil {
			failures <- err
		}
	}()
	<-backend.started
	cancel()
	err := waitRuntimeWorkers(&workers, failures, runtimeWorkersTimeout)
	if !errors.Is(err, output.ErrShutdownTimedOut) {
		t.Fatalf("runtime replacement would lose the backend shutdown failure: %v", err)
	}
	select {
	case <-backend.exited:
		t.Fatal("fixture backend exited before its shutdown failure was checked")
	default:
	}
}
