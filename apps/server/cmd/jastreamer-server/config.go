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

	"github.com/jastreamer/jastreamer-server/internal/security"
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
	tlsCertificatePath    string
	tlsPrivateKeyPath     string
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
	TLSCertificatePath    string   `json:"tls_certificate_path"`
	TLSPrivateKeyPath     string   `json:"tls_private_key_path"`
}

func loadConfig(args []string) (serverConfig, error) {
	options, err := parseStartupOptions(args)
	if err != nil {
		return serverConfig{}, err
	}
	configured := fileConfig{
		Address: ":8443", DataDirectory: "./data", PairingTTL: "5m",
		CatalogMigrationPath:  "migrations/001_catalog.sql",
		PlaybackMigrationPath: "migrations/002_playback.sql",
		PlaybackExpansionPath: "migrations/003_todo12.sql",
		CertificateDNS:        []string{"localhost"}, CertificateIPs: []string{"127.0.0.1", "::1"},
	}
	if options.configPath != "" {
		file, err := os.Open(options.configPath)
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
	dataDirectory := envOr("JASTREAMER_DATA_DIR", configured.DataDirectory)
	catalogRoot := envOr("JASTREAMER_CATALOG_ROOT", configured.CatalogRoot)
	if catalogRoot == "" {
		catalogRoot = filepath.Join(dataDirectory, "catalog")
	}
	setupSecret := os.Getenv("JASTREAMER_SETUP_SECRET")
	if setupSecret == "" {
		bootstrapped, err := security.BootstrapComplete(filepath.Join(dataDirectory, "security", "state.json"))
		if err != nil {
			return serverConfig{}, err
		}
		if !bootstrapped {
			return serverConfig{}, fmt.Errorf("JASTREAMER_SETUP_SECRET is required until first administrator bootstrap completes")
		}
	}
	tlsCertificatePath := firstConfigured(options.tlsCertificatePath, os.Getenv("JASTREAMER_TLS_CERTIFICATE_PATH"), configured.TLSCertificatePath)
	tlsPrivateKeyPath := firstConfigured(options.tlsPrivateKeyPath, os.Getenv("JASTREAMER_TLS_PRIVATE_KEY_PATH"), configured.TLSPrivateKeyPath)
	if (tlsCertificatePath == "") != (tlsPrivateKeyPath == "") {
		return serverConfig{}, fmt.Errorf("TLS certificate and private key paths must be configured together")
	}
	pairingTTL, err := time.ParseDuration(envOr("JASTREAMER_PAIRING_TTL", configured.PairingTTL))
	if err != nil || pairingTTL <= 0 {
		return serverConfig{}, fmt.Errorf("JASTREAMER_PAIRING_TTL must be a positive duration")
	}
	dnsNames := append([]string(nil), configured.CertificateDNS...)
	if configuredDNS := strings.TrimSpace(os.Getenv("JASTREAMER_CERT_DNS")); configuredDNS != "" {
		dnsNames = nil
		for value := range strings.SplitSeq(configuredDNS, ",") {
			if name := strings.TrimSpace(value); name != "" {
				dnsNames = append(dnsNames, name)
			}
		}
	}
	ipValues := configured.CertificateIPs
	if configuredIPs := strings.TrimSpace(os.Getenv("JASTREAMER_CERT_IPS")); configuredIPs != "" {
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
	if configuredOrigins := strings.TrimSpace(os.Getenv("JASTREAMER_ALLOWED_ORIGINS")); configuredOrigins != "" {
		allowedOrigins = strings.Split(configuredOrigins, ",")
	}
	return serverConfig{
		address: envOr("JASTREAMER_ADDR", configured.Address), dataDirectory: dataDirectory, catalogRoot: catalogRoot,
		catalogMigrationPath:  envOr("JASTREAMER_CATALOG_MIGRATION", configured.CatalogMigrationPath),
		playbackMigrationPath: envOr("JASTREAMER_PLAYBACK_MIGRATION", configured.PlaybackMigrationPath),
		playbackExpansionPath: envOr("JASTREAMER_PLAYBACK_EXPANSION", configured.PlaybackExpansionPath),
		setupSecret:           setupSecret, certificateDNS: dnsNames, certificateIPs: certificateIPs, pairingTTL: pairingTTL,
		allowedOrigins: allowedOrigins, tlsCertificatePath: tlsCertificatePath, tlsPrivateKeyPath: tlsPrivateKeyPath,
	}, nil
}

type startupOptions struct {
	configPath, tlsCertificatePath, tlsPrivateKeyPath string
}

func parseStartupOptions(args []string) (startupOptions, error) {
	options := startupOptions{}
	for index := 0; index < len(args); index += 2 {
		if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
			return startupOptions{}, fmt.Errorf("usage: jastreamer-server [--config <path>] [--tls-certificate <path> --tls-private-key <path>]")
		}
		value := strings.TrimSpace(args[index+1])
		switch args[index] {
		case "--config":
			if options.configPath != "" {
				return startupOptions{}, fmt.Errorf("--config may be specified once")
			}
			options.configPath = value
		case "--tls-certificate":
			if options.tlsCertificatePath != "" {
				return startupOptions{}, fmt.Errorf("--tls-certificate may be specified once")
			}
			options.tlsCertificatePath = value
		case "--tls-private-key":
			if options.tlsPrivateKeyPath != "" {
				return startupOptions{}, fmt.Errorf("--tls-private-key may be specified once")
			}
			options.tlsPrivateKeyPath = value
		default:
			return startupOptions{}, fmt.Errorf("usage: jastreamer-server [--config <path>] [--tls-certificate <path> --tls-private-key <path>]")
		}
	}
	return options, nil
}

func firstConfigured(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
