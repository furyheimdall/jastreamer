package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type IdentityConfig struct {
	Directory   string
	DNSNames    []string
	IPAddresses []net.IP
}

type Identity struct {
	Certificate    tls.Certificate
	CertificatePEM []byte
	Fingerprint    string
}

func LoadOrCreateIdentity(config IdentityConfig) (Identity, error) {
	if config.Directory == "" {
		return Identity{}, fmt.Errorf("identity directory is required")
	}
	certificatePath := filepath.Join(config.Directory, "tls-cert.pem")
	keyPath := filepath.Join(config.Directory, "tls-key.pem")
	certificatePEM, certErr := os.ReadFile(certificatePath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		return parseIdentity(certificatePEM, keyPEM)
	}
	if (certErr == nil) != (keyErr == nil) {
		if err := os.Remove(certificatePath); err != nil && !os.IsNotExist(err) {
			return Identity{}, fmt.Errorf("remove incomplete certificate: %w", err)
		}
		if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
			return Identity{}, fmt.Errorf("remove incomplete certificate key: %w", err)
		}
		certErr, keyErr = os.ErrNotExist, os.ErrNotExist
	}
	if !os.IsNotExist(certErr) || !os.IsNotExist(keyErr) {
		return Identity{}, fmt.Errorf("read local certificate identity: %v; %v", certErr, keyErr)
	}
	return createIdentity(config, certificatePath, keyPath)
}

func createIdentity(config IdentityConfig, certificatePath, keyPath string) (Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate certificate key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return Identity{}, fmt.Errorf("generate certificate serial: %w", err)
	}
	now := time.Now().UTC()
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "jastreamer Server", Organization: []string{"jastreamer"}},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), DNSNames: config.DNSNames,
		IPAddresses: config.IPAddresses, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: false,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return Identity{}, fmt.Errorf("create certificate: %w", err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Identity{}, fmt.Errorf("encode certificate key: %w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	if err := os.MkdirAll(config.Directory, 0o700); err != nil {
		return Identity{}, fmt.Errorf("create identity directory: %w", err)
	}
	certificateTemporary := certificatePath + ".tmp"
	keyTemporary := keyPath + ".tmp"
	defer os.Remove(certificateTemporary)
	defer os.Remove(keyTemporary)
	keyFile, err := os.OpenFile(keyTemporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Identity{}, fmt.Errorf("create temporary certificate key: %w", err)
	}
	if _, err := keyFile.Write(keyPEM); err != nil {
		_ = keyFile.Close()
		return Identity{}, fmt.Errorf("write certificate key: %w", err)
	}
	if err := keyFile.Sync(); err != nil {
		_ = keyFile.Close()
		return Identity{}, fmt.Errorf("sync certificate key: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		return Identity{}, fmt.Errorf("close certificate key: %w", err)
	}
	certificateFile, err := os.OpenFile(certificateTemporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Identity{}, fmt.Errorf("create temporary certificate: %w", err)
	}
	if _, err := certificateFile.Write(certificatePEM); err != nil {
		_ = certificateFile.Close()
		return Identity{}, fmt.Errorf("write certificate: %w", err)
	}
	if err := certificateFile.Sync(); err != nil {
		_ = certificateFile.Close()
		return Identity{}, fmt.Errorf("sync certificate: %w", err)
	}
	if err := certificateFile.Close(); err != nil {
		return Identity{}, fmt.Errorf("close certificate: %w", err)
	}
	if err := os.Rename(keyTemporary, keyPath); err != nil {
		return Identity{}, fmt.Errorf("install certificate key: %w", err)
	}
	if err := os.Rename(certificateTemporary, certificatePath); err != nil {
		return Identity{}, fmt.Errorf("install certificate: %w", err)
	}
	if runtime.GOOS != "windows" {
		directory, err := os.Open(config.Directory)
		if err != nil {
			return Identity{}, fmt.Errorf("open identity directory: %w", err)
		}
		if err := directory.Sync(); err != nil {
			_ = directory.Close()
			return Identity{}, fmt.Errorf("sync identity directory: %w", err)
		}
		if err := directory.Close(); err != nil {
			return Identity{}, fmt.Errorf("close identity directory: %w", err)
		}
	}
	return parseIdentity(certificatePEM, keyPEM)
}

func parseIdentity(certificatePEM, keyPEM []byte) (Identity, error) {
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return Identity{}, fmt.Errorf("parse certificate identity: %w", err)
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return Identity{}, fmt.Errorf("parse leaf certificate: %w", err)
	}
	if parsed.IsCA || parsed.Subject.CommonName != "jastreamer Server" {
		return Identity{}, fmt.Errorf("invalid local certificate profile")
	}
	digest := sha256.Sum256(parsed.Raw)
	return Identity{Certificate: certificate, CertificatePEM: certificatePEM, Fingerprint: hex.EncodeToString(digest[:])}, nil
}
