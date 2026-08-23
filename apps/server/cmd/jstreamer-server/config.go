package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type serverConfig struct {
	address               string
	dataDirectory         string
	catalogRoot           string
	catalogMigrationPath  string
	playbackMigrationPath string
	playbackExpansionPath string
	setupSecret           string
	certificateDNS        []string
	certificateIPs        []net.IP
	pairingTTL            time.Duration
	allowedOrigins        []string
}

type fileConfig struct {
	Address               string   `json:"address"`
	DataDirectory         string   `json:"data_directory"`
	CatalogRoot           string   `json:"catalog_root"`
	CatalogMigrationPath  string   `json:"catalog_migration"`
	PlaybackMigrationPath string   `json:"playback_migration"`
	PlaybackExpansionPath string   `json:"playback_expansion"`
	CertificateDNS        []string `json:"certificate_dns"`
	CertificateIPs        []string `json:"certificate_ips"`
	AllowedOrigins        []string `json:"allowed_origins"`
	PairingTTL            string   `json:"pairing_ttl"`
}

func loadConfig(args []string) (serverConfig, error) {
	configured := fileConfig{
		Address: ":8443", DataDirectory: "./data", PairingTTL: "5m",
		CatalogMigrationPath:  "migrations/001_catalog.sql",
		PlaybackMigrationPath: "migrations/002_playback.sql",
		PlaybackExpansionPath: "migrations/003_todo12.sql",
		CertificateDNS:        []string{"localhost"}, CertificateIPs: []string{"127.0.0.1", "::1"},
	}
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "--config" || strings.TrimSpace(args[1]) == "" {
			return serverConfig{}, fmt.Errorf("usage: jstreamer-server [--config <path>]")
		}
		file, err := os.Open(args[1])
		if err != nil {
			return serverConfig{}, fmt.Errorf("open config: %w", err)
		}
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&configured); err != nil {
			return serverConfig{}, fmt.Errorf("decode config: %w", err)
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); err != io.EOF {
			return serverConfig{}, fmt.Errorf("decode config: exactly one object is required")
		}
		if configured.Address == "" || configured.DataDirectory == "" || configured.CatalogRoot == "" ||
			configured.CatalogMigrationPath == "" || configured.PlaybackMigrationPath == "" ||
			configured.PlaybackExpansionPath == "" || configured.PairingTTL == "" {
			return serverConfig{}, fmt.Errorf("config: address, data, catalog, migrations, and pairing_ttl are required")
		}
	}
	dataDirectory := envOr("JSTREAMER_DATA_DIR", configured.DataDirectory)
	catalogRoot := envOr("JSTREAMER_CATALOG_ROOT", configured.CatalogRoot)
	if catalogRoot == "" {
		catalogRoot = filepath.Join(dataDirectory, "catalog")
	}
	setupSecret := os.Getenv("JSTREAMER_SETUP_SECRET")
	if setupSecret == "" {
		return serverConfig{}, fmt.Errorf("JSTREAMER_SETUP_SECRET is required until first-admin recovery is configured")
	}
	pairingTTL, err := time.ParseDuration(envOr("JSTREAMER_PAIRING_TTL", configured.PairingTTL))
	if err != nil || pairingTTL <= 0 {
		return serverConfig{}, fmt.Errorf("JSTREAMER_PAIRING_TTL must be a positive duration")
	}
	dnsNames := append([]string(nil), configured.CertificateDNS...)
	if configuredDNS := strings.TrimSpace(os.Getenv("JSTREAMER_CERT_DNS")); configuredDNS != "" {
		dnsNames = nil
		for value := range strings.SplitSeq(configuredDNS, ",") {
			if name := strings.TrimSpace(value); name != "" {
				dnsNames = append(dnsNames, name)
			}
		}
	}
	ipValues := configured.CertificateIPs
	if configuredIPs := strings.TrimSpace(os.Getenv("JSTREAMER_CERT_IPS")); configuredIPs != "" {
		ipValues = strings.Split(configuredIPs, ",")
	}
	certificateIPs := make([]net.IP, 0, len(ipValues))
	for _, raw := range ipValues {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			return serverConfig{}, fmt.Errorf("certificate IP %q is invalid", raw)
		}
		certificateIPs = append(certificateIPs, ip)
	}
	allowedOrigins := configured.AllowedOrigins
	if configuredOrigins := strings.TrimSpace(os.Getenv("JSTREAMER_ALLOWED_ORIGINS")); configuredOrigins != "" {
		allowedOrigins = strings.Split(configuredOrigins, ",")
	}
	return serverConfig{
		address: envOr("JSTREAMER_ADDR", configured.Address), dataDirectory: dataDirectory, catalogRoot: catalogRoot,
		catalogMigrationPath:  envOr("JSTREAMER_CATALOG_MIGRATION", configured.CatalogMigrationPath),
		playbackMigrationPath: envOr("JSTREAMER_PLAYBACK_MIGRATION", configured.PlaybackMigrationPath),
		playbackExpansionPath: envOr("JSTREAMER_PLAYBACK_EXPANSION", configured.PlaybackExpansionPath),
		setupSecret:           setupSecret, certificateDNS: dnsNames, certificateIPs: certificateIPs, pairingTTL: pairingTTL,
		allowedOrigins: allowedOrigins,
	}, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
