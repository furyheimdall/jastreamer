package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func TestRuntimeSettingsConfig_exposes_runtime_locks_and_marks_environment_overrides(t *testing.T) {
	// Given
	directory := t.TempDir()
	catalogRoot := filepath.Join(directory, "music")
	if err := os.Mkdir(catalogRoot, 0o750); err != nil {
		t.Fatalf("create catalog root: %v", err)
	}
	t.Setenv("JASTREAMER_ALLOWED_ORIGINS", "https://control.example")
	config := serverConfig{
		address: "127.0.0.1:8443", dataDirectory: directory, catalogRoot: catalogRoot,
		certificateDNS: []string{"localhost"}, certificateIPs: []net.IP{net.ParseIP("127.0.0.1")},
		pairingTTL: 5 * time.Minute, allowedOrigins: []string{"https://control.example"},
	}

	// When
	storeConfig, err := runtimeSettingsConfig(config, "redacted-fingerprint")
	if err != nil {
		t.Fatalf("runtime settings config: %v", err)
	}
	store, err := settings.Open(storeConfig)
	// Then
	if err != nil {
		t.Fatalf("open runtime settings: %v", err)
	}
	snapshot := store.Snapshot()
	if snapshot.Locks.ListenAddress != config.address || snapshot.Locks.DataDirectory != directory ||
		len(snapshot.Locks.EnvironmentLockedFields) != 1 || snapshot.Locks.EnvironmentLockedFields[0] != "control_origins" {
		t.Fatalf("runtime locks = %+v", snapshot.Locks)
	}
}

func TestLoadConfig_allows_setup_secret_free_restart_after_bootstrap(t *testing.T) {
	// Given
	dataDirectory := t.TempDir()
	statePath := filepath.Join(dataDirectory, "security", "state.json")
	manager, err := security.NewManager(security.Config{SetupSecret: "installer-secret", StatePath: statePath})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	if _, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "Admin"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Setenv("JASTREAMER_DATA_DIR", dataDirectory)
	t.Setenv("JASTREAMER_SETUP_SECRET", "")

	// When
	config, err := loadConfig(nil)
	// Then
	if err != nil {
		t.Fatalf("load config after bootstrap: %v", err)
	}
	if config.setupSecret != "" {
		t.Fatalf("setup secret retained after bootstrap: %q", config.setupSecret)
	}
}
