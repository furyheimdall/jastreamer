package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func runtimeSettingsConfig(config serverConfig, certificateFingerprint string) (settings.Config, error) {
	lockedFields := make([]string, 0, 3)
	if os.Getenv("JASTREAMER_ALLOWED_ORIGINS") != "" {
		lockedFields = append(lockedFields, "control_origins")
	}
	if os.Getenv("JASTREAMER_PAIRING_TTL") != "" {
		lockedFields = append(lockedFields, "pairing_ttl_seconds")
	}
	certificateSANs := append([]string(nil), config.certificateDNS...)
	for _, address := range config.certificateIPs {
		certificateSANs = append(certificateSANs, address.String())
	}
	dataDirectory, err := filepath.Abs(config.dataDirectory)
	if err != nil {
		return settings.Config{}, fmt.Errorf("absolute data directory: %w", err)
	}
	catalogRoot, err := filepath.Abs(config.catalogRoot)
	if err != nil {
		return settings.Config{}, fmt.Errorf("absolute catalog root: %w", err)
	}
	environment := strings.TrimSpace(os.Getenv("JASTREAMER_ENVIRONMENT"))
	if environment == "" {
		environment = "production"
	}
	return settings.Config{
		Path: filepath.Join(dataDirectory, "config", "settings.json"),
		Defaults: settings.Values{
			DisplayName:       "Jake Streamer",
			CatalogRoots:      []settings.CatalogRoot{{ID: "default", DisplayName: "Music", Path: catalogRoot}},
			ControlOrigins:    append([]string(nil), config.allowedOrigins...),
			PairingTTLSeconds: int(config.pairingTTL.Seconds()),
		},
		Locks: settings.Locks{
			ListenAddress: config.address, CertificateFingerprint: certificateFingerprint,
			CertificateSANs: certificateSANs, DataDirectory: dataDirectory,
			AllowedCatalogBases: []string{catalogRoot}, Environment: environment,
			EnvironmentLockedFields: lockedFields,
		},
	}, nil
}
