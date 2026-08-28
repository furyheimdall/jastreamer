package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMainExitCode_helpReturnsSuccess(t *testing.T) {
	// Given
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	// When
	code := mainExitCode([]string{"--help"}, &stdout, &stderr)

	// Then
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: jastreamer-server") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLoadConfig_requires_installer_setup_secret(t *testing.T) {
	// Given
	t.Setenv("JASTREAMER_SETUP_SECRET", "")

	// When
	_, err := loadConfig(nil)

	// Then
	if err == nil {
		t.Fatal("loadConfig succeeded without setup secret")
	}
}

func TestLoadConfig_uses_local_state_and_TLS_defaults(t *testing.T) {
	// Given
	t.Setenv("JASTREAMER_SETUP_SECRET", "fixture-secret")
	t.Setenv("JASTREAMER_DATA_DIR", "/tmp/jastreamer-fixture")

	// When
	config, err := loadConfig(nil)

	// Then
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.address != ":8443" || config.dataDirectory != "/tmp/jastreamer-fixture" || config.setupSecret != "fixture-secret" {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadConfig_reads_strict_checked_in_machine_config(t *testing.T) {
	// Given
	t.Setenv("JASTREAMER_SETUP_SECRET", "fixture-secret")
	t.Setenv("JASTREAMER_DATA_DIR", t.TempDir())

	// When
	config, err := loadConfig([]string{"--config", "../../../../tooling/fixtures/e2e/local.yaml"})

	// Then
	if err != nil {
		t.Fatalf("load checked-in config: %v", err)
	}
	if config.address != "127.0.0.1:0" || len(config.certificateIPs) != 2 {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadConfig_rejects_unknown_machine_config_key(t *testing.T) {
	// Given
	t.Setenv("JASTREAMER_SETUP_SECRET", "fixture-secret")
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte(`{"address":"127.0.0.1:0","unknown":true}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// When
	_, err := loadConfig([]string{"--config", path})

	// Then
	if err == nil {
		t.Fatal("unknown config key was accepted")
	}
}

func TestLoadConfig_applies_pairing_TTL_override(t *testing.T) {
	// Given
	t.Setenv("JASTREAMER_SETUP_SECRET", "fixture-secret")
	t.Setenv("JASTREAMER_PAIRING_TTL", "1ns")

	// When
	config, err := loadConfig(nil)

	// Then
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.pairingTTL != time.Nanosecond {
		t.Fatalf("pairing TTL = %s", config.pairingTTL)
	}
}
