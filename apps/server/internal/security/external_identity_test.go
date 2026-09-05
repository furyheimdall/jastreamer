package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadExternalIdentity_accepts_secure_pair_without_mutating_local_identity(t *testing.T) {
	localDirectory := externalIdentityTestDirectory(t)
	local, err := LoadOrCreateIdentity(IdentityConfig{Directory: localDirectory, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	externalDirectory := externalIdentityTestDirectory(t)
	external, err := LoadOrCreateIdentity(IdentityConfig{Directory: externalDirectory, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	protectExternalIdentityTestKey(t, filepath.Join(externalDirectory, "tls-key.pem"))
	loaded, err := LoadExternalIdentity(ExternalIdentityConfig{CertificatePath: filepath.Join(externalDirectory, "tls-cert.pem"), PrivateKeyPath: filepath.Join(externalDirectory, "tls-key.pem"), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, Now: time.Now()})
	if err != nil || loaded.Fingerprint != external.Fingerprint || loaded.Fingerprint == local.Fingerprint {
		t.Fatalf("external identity = %q, %v", loaded.Fingerprint, err)
	}
	reloaded, err := LoadOrCreateIdentity(IdentityConfig{Directory: localDirectory, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil || reloaded.Fingerprint != local.Fingerprint {
		t.Fatalf("local identity changed = %q/%q, %v", local.Fingerprint, reloaded.Fingerprint, err)
	}
}

func TestLoadExternalIdentity_rejects_symlink_permissions_mismatch_and_malformed_without_key_leakage(t *testing.T) {
	left := externalIdentityTestDirectory(t)
	right := externalIdentityTestDirectory(t)
	if _, err := LoadOrCreateIdentity(IdentityConfig{Directory: left, DNSNames: []string{"localhost"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateIdentity(IdentityConfig{Directory: right, DNSNames: []string{"localhost"}}); err != nil {
		t.Fatal(err)
	}
	certificatePath, keyPath := filepath.Join(left, "tls-cert.pem"), filepath.Join(left, "tls-key.pem")
	protectExternalIdentityTestKey(t, keyPath)
	protectExternalIdentityTestKey(t, filepath.Join(right, "tls-key.pem"))
	tests := []struct {
		name    string
		prepare func(*testing.T) (string, string)
	}{
		{name: "certificate symlink", prepare: func(t *testing.T) (string, string) {
			path := filepath.Join(externalIdentityTestDirectory(t), "cert.pem")
			if err := os.Symlink(certificatePath, path); err != nil {
				t.Fatal(err)
			}
			return path, keyPath
		}},
		{name: "special certificate path", prepare: func(t *testing.T) (string, string) { return externalIdentityTestDirectory(t), keyPath }},
		{name: "mismatched key", prepare: func(*testing.T) (string, string) { return certificatePath, filepath.Join(right, "tls-key.pem") }},
		{name: "malformed certificate", prepare: func(t *testing.T) (string, string) {
			path := filepath.Join(externalIdentityTestDirectory(t), "cert.pem")
			if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path, keyPath
		}},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests, struct {
			name    string
			prepare func(*testing.T) (string, string)
		}{name: "insecure key permissions", prepare: func(t *testing.T) (string, string) {
			directory := externalIdentityTestDirectory(t)
			cert := filepath.Join(directory, "cert.pem")
			key := filepath.Join(directory, "key.pem")
			certBytes, _ := os.ReadFile(certificatePath)
			keyBytes, _ := os.ReadFile(keyPath)
			if err := os.WriteFile(cert, certBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(key, keyBytes, 0o644); err != nil {
				t.Fatal(err)
			}
			return cert, key
		}})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cert, key := test.prepare(t)
			_, err := LoadExternalIdentity(ExternalIdentityConfig{CertificatePath: cert, PrivateKeyPath: key, DNSNames: []string{"localhost"}, Now: time.Now()})
			if err == nil {
				t.Fatal("external identity unexpectedly loaded")
			}
			keyBytes, _ := os.ReadFile(key)
			if len(keyBytes) > 16 && strings.Contains(err.Error(), string(keyBytes[:16])) {
				t.Fatalf("error leaked key bytes: %v", err)
			}
		})
	}
}

func TestReadExternalIdentityFile_rejects_path_drift_after_open(t *testing.T) {
	directory := externalIdentityTestDirectory(t)
	path := filepath.Join(directory, "key.pem")
	replacement := filepath.Join(directory, "replacement.pem")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	protectExternalIdentityTestKey(t, path)
	if err := os.WriteFile(replacement, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	var hookErr error
	content, err := readExternalIdentityFile(path, 1024, true, func() { hookErr = os.Rename(replacement, path) })
	if runtime.GOOS == "windows" {
		if !errors.Is(hookErr, os.ErrPermission) || err != nil || string(content) != "first" {
			t.Fatalf("open-file replacement = %q, load=%v, replace=%v", content, err, hookErr)
		}
		return
	}
	if hookErr != nil {
		t.Fatalf("replace configured path: %v", hookErr)
	}
	if err == nil || !strings.Contains(err.Error(), "changed while loading") {
		t.Fatalf("path drift error = %v", err)
	}
}

func TestLoadExternalIdentity_rejects_missing_configured_SAN(t *testing.T) {
	directory := externalIdentityTestDirectory(t)
	if _, err := LoadOrCreateIdentity(IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}}); err != nil {
		t.Fatal(err)
	}
	protectExternalIdentityTestKey(t, filepath.Join(directory, "tls-key.pem"))
	_, err := LoadExternalIdentity(ExternalIdentityConfig{CertificatePath: filepath.Join(directory, "tls-cert.pem"), PrivateKeyPath: filepath.Join(directory, "tls-key.pem"), DNSNames: []string{"other.invalid"}, Now: time.Now()})
	if err == nil {
		t.Fatal("certificate without configured SAN was accepted")
	}
}

func TestParseExternalIdentity_rejects_expired_notYetValid_weak_and_wrongUsage(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		before, after time.Time
		weak          bool
		usage         []x509.ExtKeyUsage
	}{
		{name: "expired", before: now.Add(-2 * time.Hour), after: now.Add(-time.Hour), usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "not yet valid", before: now.Add(time.Hour), after: now.Add(2 * time.Hour), usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "weak key", before: now.Add(-time.Hour), after: now.Add(time.Hour), weak: true, usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{name: "wrong usage", before: now.Add(-time.Hour), after: now.Add(time.Hour), usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert, key := externalTestPair(t, test.before, test.after, test.weak, test.usage)
			if _, err := parseExternalIdentity(cert, key, ExternalIdentityConfig{DNSNames: []string{"localhost"}, Now: now}); err == nil {
				t.Fatal("invalid external pair accepted")
			}
		})
	}
}

func TestPrivateKeyPermissionPolicy_modelsWindowsAndUnix(t *testing.T) {
	if err := externalPrivateKeyPermissionPolicy("linux", 0o600, true, false); err != nil {
		t.Fatal(err)
	}
	if err := externalPrivateKeyPermissionPolicy("linux", 0o640, true, false); err == nil {
		t.Fatal("unix group-readable key accepted")
	}
	if err := externalPrivateKeyPermissionPolicy("linux", 0o600, false, false); err == nil {
		t.Fatal("foreign-owned unix key accepted")
	}
	if err := externalPrivateKeyPermissionPolicy("windows", 0o666, true, false); err != nil {
		t.Fatalf("Windows ACL-owned path rejected: %v", err)
	}
	if err := externalPrivateKeyPermissionPolicy("windows", 0o600, true, true); err == nil {
		t.Fatal("Windows broadly readable ACL accepted")
	}
	if err := externalPrivateKeyPermissionPolicy("windows", 0o600, false, false); err == nil {
		t.Fatal("foreign-owned Windows key accepted")
	}
}

func externalIdentityTestDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := secureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	return directory
}

func externalTestPair(t *testing.T, before, after time.Time, weak bool, usages []x509.ExtKeyUsage) ([]byte, []byte) {
	t.Helper()
	var private any
	var public any
	if weak {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			t.Fatal(err)
		}
		private, public = key, &key.PublicKey
	} else {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		private, public = key, &key.PublicKey
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "external"}, DNSNames: []string{"localhost"}, NotBefore: before, NotAfter: after, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
}
