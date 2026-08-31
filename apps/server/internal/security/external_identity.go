package security

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	maxExternalCertificateBytes = 1 << 20
	maxExternalPrivateKeyBytes  = 256 << 10
)

type ExternalIdentityConfig struct {
	CertificatePath string
	PrivateKeyPath  string
	DNSNames        []string
	IPAddresses     []net.IP
	Now             time.Time
}

func LoadExternalIdentity(config ExternalIdentityConfig) (Identity, error) {
	if config.CertificatePath == "" || config.PrivateKeyPath == "" {
		return Identity{}, errors.New("external TLS certificate and private key paths are required")
	}
	certificatePEM, err := readExternalIdentityFile(config.CertificatePath, maxExternalCertificateBytes, false, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("load external TLS certificate: %w", err)
	}
	keyPEM, err := readExternalIdentityFile(config.PrivateKeyPath, maxExternalPrivateKeyBytes, true, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("load external TLS private key: %w", err)
	}
	return parseExternalIdentity(certificatePEM, keyPEM, config)
}

func readExternalIdentityFile(path string, limit int64, privateKey bool, afterOpen func()) ([]byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve configured path")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != filepath.Clean(absolute) {
		return nil, errors.New("configured path must be canonical and contain no symlinks")
	}
	before, err := os.Lstat(absolute)
	if err != nil {
		return nil, errors.New("configured file is not readable")
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("configured path must be a regular non-symlink file")
	}
	if privateKey {
		if err := validatePrivateKeyOwnership(absolute, before); err != nil {
			return nil, err
		}
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, errors.New("open configured file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.New("configured file changed before open")
	}
	if afterOpen != nil {
		afterOpen()
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(content) == 0 || int64(len(content)) > limit {
		return nil, errors.New("configured file content is invalid")
	}
	afterResolved, resolveErr := filepath.EvalSymlinks(absolute)
	after, err := os.Lstat(absolute)
	if resolveErr != nil || afterResolved != resolved || err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return nil, errors.New("configured file changed while loading")
	}
	return content, nil
}

func parseExternalIdentity(certificatePEM, keyPEM []byte, config ExternalIdentityConfig) (Identity, error) {
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil || len(certificate.Certificate) == 0 {
		return Identity{}, errors.New("external TLS certificate/private key pair is malformed or mismatched")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return Identity{}, errors.New("external TLS leaf certificate is malformed")
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return Identity{}, errors.New("external TLS certificate is outside its validity period")
	}
	if leaf.IsCA || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !allowsServerAuthentication(leaf.ExtKeyUsage) {
		return Identity{}, errors.New("external TLS certificate profile is not valid for Server authentication")
	}
	if err := validateExternalPublicKey(leaf.PublicKey); err != nil {
		return Identity{}, err
	}
	for _, name := range config.DNSNames {
		if err := leaf.VerifyHostname(name); err != nil {
			return Identity{}, errors.New("external TLS certificate does not cover configured DNS names")
		}
	}
	for _, address := range config.IPAddresses {
		if err := leaf.VerifyHostname(address.String()); err != nil {
			return Identity{}, errors.New("external TLS certificate does not cover configured IP addresses")
		}
	}
	certificate.Leaf = leaf
	digest := sha256.Sum256(leaf.Raw)
	return Identity{Certificate: certificate, CertificatePEM: append([]byte(nil), certificatePEM...), Fingerprint: hex.EncodeToString(digest[:])}, nil
}

func allowsServerAuthentication(usages []x509.ExtKeyUsage) bool {
	for _, usage := range usages {
		if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func validateExternalPublicKey(publicKey any) error {
	switch key := publicKey.(type) {
	case *ecdsa.PublicKey:
		if key.Curve == nil || key.Curve.Params().BitSize < 256 {
			return errors.New("external TLS ECDSA key is too weak")
		}
	case *rsa.PublicKey:
		if key.N.BitLen() < 2048 {
			return errors.New("external TLS RSA key is too weak")
		}
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return errors.New("external TLS Ed25519 key is invalid")
		}
	default:
		return errors.New("external TLS public key algorithm is unsupported")
	}
	return nil
}

func externalPrivateKeyPermissionPolicy(platform string, mode os.FileMode, ownerMatches, broadRead bool) error {
	if !ownerMatches {
		return errors.New("external TLS private key must be owned by the Server account")
	}
	if platform == "windows" {
		if broadRead {
			return errors.New("external TLS private key ACL grants broad read access")
		}
		return nil
	}
	if mode.Perm()&0o177 != 0 {
		return errors.New("external TLS private key permissions must grant only owner read/write access")
	}
	return nil
}
