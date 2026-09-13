package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/config"
)

func TestValidateRequiresAtLeastOneListener(t *testing.T) {
	value := config.Default()
	value.HTTP.Enabled = false
	value.HTTPS.Enabled = false
	if err := config.Validate(value); err == nil {
		t.Fatal("configuration with no listener was accepted")
	}
}

func TestValidateServerNameBoundaries(t *testing.T) {
	value := config.Default()
	value.ServerName = strings.Repeat("界", 64)
	if err := config.Validate(value); err != nil {
		t.Fatalf("64-character server name rejected: %v", err)
	}
	value.ServerName += "界"
	if err := config.Validate(value); err == nil {
		t.Fatal("65-character server name was accepted")
	}
	value.ServerName = " Living room"
	if err := config.Validate(value); err == nil {
		t.Fatal("server name with surrounding whitespace was accepted")
	}
	value.ServerName = ""
	if err := config.Validate(value); err != nil {
		t.Fatalf("empty hostname fallback rejected: %v", err)
	}
}

func TestValidateAllowsUnavailableRootsButRejectsPublicCIDRs(t *testing.T) {
	value := config.Default()
	value.LibraryRoots = []config.Root{{
		ID:   "offline",
		Name: "Offline library",
		Path: filepath.Join(t.TempDir(), "not-mounted"),
	}}
	value.Network.AllowedCIDRs = []string{"192.168.0.0/16"}
	if err := config.Validate(value); err != nil {
		t.Fatalf("unavailable root or private CIDR rejected: %v", err)
	}
	value.Network.AllowedCIDRs = []string{"0.0.0.0/0"}
	if err := config.Validate(value); err == nil {
		t.Fatal("public CIDR was accepted")
	}
}

func TestValidateRejectsInvalidEnabledTLS(t *testing.T) {
	directory := t.TempDir()
	certificate := filepath.Join(directory, "certificate.pem")
	privateKey := filepath.Join(directory, "private-key.pem")
	if err := os.WriteFile(certificate, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKey, []byte("not a private key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value := config.Default()
	value.HTTP.Enabled = false
	value.HTTPS = config.HTTPS{
		Enabled:         true,
		Address:         ":8443",
		CertificateFile: certificate,
		PrivateKeyFile:  privateKey,
	}
	if err := config.Validate(value); err == nil {
		t.Fatal("enabled HTTPS with an invalid PEM pair was accepted")
	}
}

func TestValidateAirPlayRequiresExecutableHelperAndFFmpeg(t *testing.T) {
	value := config.Default()
	value.AirPlay.Enabled = true
	if err := config.Validate(value); err == nil {
		t.Fatal("enabled AirPlay without sender executables was accepted")
	}
	if runtime.GOOS != "linux" {
		return
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "airplay-helper")
	ffmpeg := filepath.Join(directory, "ffmpeg")
	for _, path := range []string{helper, ffmpeg} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	value.AirPlay.HelperPath = helper
	value.Media.FFmpegPath = ffmpeg
	if err := config.Validate(value); err != nil {
		t.Fatalf("enabled AirPlay with executable helper and FFmpeg rejected: %v", err)
	}
}

func TestLoadLegacyConfigDefaultsCastWithoutChangingState(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.json")
	expected := config.Default()
	expected.DataDir = filepath.Join(directory, "data")
	expected.ServerName = "Legacy server"
	expected.LibraryRoots = []config.Root{{
		ID:   "music",
		Name: "Music",
		Path: filepath.Join(directory, "music"),
	}}
	expected.Network.Interfaces = []string{"ethernet0"}
	expected.Network.AllowedCIDRs = []string{"192.168.0.0/16"}
	expected.AirPlay.HelperPath = filepath.Join(directory, "airplay-helper")

	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "cast")
	encoded, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config without cast field: %v", err)
	}
	if !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("legacy config state changed:\nloaded:   %#v\nexpected: %#v", loaded, expected)
	}
}

func TestSaveLoadIsAtomicAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "server.json")
	value := config.Default()
	value.DataDir = filepath.Join(t.TempDir(), "data")
	value.LibraryRoots = []config.Root{{ID: "music", Name: "Music", Path: filepath.Join(t.TempDir(), "music")}}
	if err := config.Save(path, value); err != nil {
		t.Fatalf("save config: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.DataDir != value.DataDir || len(loaded.LibraryRoots) != 1 || loaded.LibraryRoots[0] != value.LibraryRoots[0] {
		t.Fatalf("loaded config differs: %#v", loaded)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if permission := info.Mode().Perm(); permission != 0o600 {
			t.Fatalf("config permission = %o", permission)
		}
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".server-config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary config files remain: %v", matches)
	}
}
