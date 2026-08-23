package main

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
)

func TestRun_rejects_bad_and_busy_addresses_before_ready(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		address func(t *testing.T) string
	}{
		{name: "bad address", address: func(*testing.T) string { return "not-an-address" }},
		{name: "busy address", address: func(t *testing.T) string {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("reserve busy address: %v", err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			return listener.Addr().String()
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			config := serverConfig{
				address: scenario.address(t), dataDirectory: directory, catalogRoot: filepath.Join(directory, "music"),
				catalogMigrationPath: "../../migrations/001_catalog.sql", playbackMigrationPath: "../../migrations/002_playback.sql",
				playbackExpansionPath: "../../migrations/003_todo12.sql", setupSecret: "fixture",
				certificateDNS: []string{"localhost"}, certificateIPs: []net.IP{net.ParseIP("127.0.0.1")}, pairingTTL: 5,
			}
			if err := run(context.Background(), config); err == nil {
				t.Fatal("run accepted unavailable address")
			}
		})
	}
}

func TestWaitForServerShutsDownAndReturnsProcessorFailure(t *testing.T) {
	processorFailure := errors.New("processor failed")
	shutdownFailure := errors.New("shutdown failed")
	processorErrors := make(chan error, 1)
	processorErrors <- processorFailure
	called := false
	err := waitForServer(context.Background(), make(chan error), processorErrors, func(context.Context) error { called = true; return shutdownFailure })
	if !called || !errors.Is(err, processorFailure) || !errors.Is(err, shutdownFailure) {
		t.Fatalf("shutdown=%v error=%v", called, err)
	}
}
