package security_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestIdentity_is_stable_nonCA_and_has_SHA256_fingerprint(t *testing.T) {
	// Given
	directory := t.TempDir()

	// When
	first, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}})
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	second, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}})

	// Then
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if first.Fingerprint != second.Fingerprint || len(first.Fingerprint) != 64 {
		t.Fatalf("fingerprints = %q, %q", first.Fingerprint, second.Fingerprint)
	}
	block, _ := pem.Decode(first.CertificatePEM)
	if block == nil {
		t.Fatal("certificate PEM did not decode")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if certificate.IsCA || certificate.Subject.CommonName != "jastreamer Server" {
		t.Fatalf("certificate identity = CA:%v CN:%q", certificate.IsCA, certificate.Subject.CommonName)
	}
	keyInfo, err := os.Stat(filepath.Join(directory, "tls-key.pem"))
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, error = %v", keyInfo.Mode().Perm(), err)
	}
}

func TestIdentity_recovers_incomplete_crash_state(t *testing.T) {
	// Given
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "tls-key.pem"), []byte("orphan"), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	// When
	first, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}})
	if err != nil {
		t.Fatalf("recover identity: %v", err)
	}
	second, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}})

	// Then
	if err != nil || first.Fingerprint != second.Fingerprint {
		t.Fatalf("restarted identity = %q/%q, %v", first.Fingerprint, second.Fingerprint, err)
	}
}
