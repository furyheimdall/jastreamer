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

func TestLoadConfig_requires_complete_external_TLS_pair_and_honors_precedence(t *testing.T) {
	t.Setenv("JASTREAMER_SETUP_SECRET", "fixture-secret")
	t.Setenv("JASTREAMER_TLS_CERTIFICATE_PATH", "/env/cert.pem")
	t.Setenv("JASTREAMER_TLS_PRIVATE_KEY_PATH", "")
	if _, err := loadConfig(nil); err == nil || !strings.Contains(err.Error(), "TLS certificate and private key paths must be configured together") {
		t.Fatalf("missing environment key error = %v", err)
	}
	t.Setenv("JASTREAMER_TLS_PRIVATE_KEY_PATH", "/env/key.pem")
	config, err := loadConfig([]string{"--tls-certificate", "/cli/cert.pem", "--tls-private-key", "/cli/key.pem"})
	if err != nil {
		t.Fatalf("load external TLS config: %v", err)
	}
	if config.tlsCertificatePath != "/cli/cert.pem" || config.tlsPrivateKeyPath != "/cli/key.pem" {
		t.Fatalf("external TLS paths = %q/%q", config.tlsCertificatePath, config.tlsPrivateKeyPath)
	}
}

func TestLoadConfig_reads_external_TLS_pair_from_machine_config(t *testing.T) {
	t.Setenv("JASTREAMER_SETUP_SECRET", "fixture-secret")
	path := filepath.Join(t.TempDir(), "server.json")
	body := `{"address":"127.0.0.1:0","data_directory":"data","catalog_root":"media","catalog_migration":"catalog.sql","playback_migration":"playback.sql","playback_expansion":"expansion.sql","pairing_ttl":"5m","tls_certificate_path":"/operator/cert.pem","tls_private_key_path":"/operator/key.pem"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil { t.Fatal(err) }
	config, err := loadConfig([]string{"--config", path})
	if err != nil || config.tlsCertificatePath != "/operator/cert.pem" || config.tlsPrivateKeyPath != "/operator/key.pem" {
		t.Fatalf("machine TLS config = %#v, %v", config, err)
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
