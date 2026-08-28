package api_test

import (
	"crypto/x509"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func originCertificate(t *testing.T, addresses ...string) *x509.Certificate {
	t.Helper()
	ips := make([]net.IP, len(addresses))
	for index, address := range addresses {
		ips[index] = net.ParseIP(address)
	}
	identity, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: t.TempDir(), IPAddresses: ips})
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(identity.Certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func Test_ServerHTTPSOrigin_accepts_private_certificate_bound_IP_listeners_with_canonical_formatting(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		listener string
		identity string
	}{
		{name: "IPv4 exact listener", origin: "https://192.168.10.8:8443", listener: "192.168.10.8:8443", identity: "192.168.10.8"},
		{name: "IPv4 wildcard listener", origin: "https://10.0.0.8:8443", listener: "0.0.0.0:8443", identity: "10.0.0.8"},
		{name: "IPv6 exact listener", origin: "https://[fd00::8]:8443", listener: "[fd00::8]:8443", identity: "fd00::8"},
		{name: "IPv6 wildcard listener", origin: "https://[fd00::9]:8443", listener: "[::]:8443", identity: "fd00::9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			certificate := originCertificate(t, test.identity)

			// When
			_, err := api.NewServerHTTPSOrigin(test.origin, test.listener, certificate)
			// Then
			if err != nil {
				t.Fatalf("trusted origin rejected: %v", err)
			}
		})
	}
}

func Test_ServerHTTPSOrigin_rejects_missing_unsafe_or_certificate_mismatched_configuration(t *testing.T) {
	certificate := originCertificate(t, "192.168.10.8")
	tests := []struct {
		name     string
		origin   string
		listener string
		cert     *x509.Certificate
	}{
		{name: "missing origin", listener: "192.168.10.8:8443", cert: certificate},
		{name: "missing listener", origin: "https://192.168.10.8:8443", cert: certificate},
		{name: "missing certificate", origin: "https://192.168.10.8:8443", listener: "192.168.10.8:8443"},
		{name: "plaintext", origin: "http://192.168.10.8:8443", listener: "192.168.10.8:8443", cert: certificate},
		{name: "public address", origin: "https://203.0.113.8:8443", listener: "203.0.113.8:8443", cert: originCertificate(t, "203.0.113.8")},
		{name: "wildcard origin", origin: "https://0.0.0.0:8443", listener: "0.0.0.0:8443", cert: originCertificate(t, "0.0.0.0")},
		{name: "listener address mismatch", origin: "https://192.168.10.8:8443", listener: "192.168.10.9:8443", cert: certificate},
		{name: "listener port mismatch", origin: "https://192.168.10.8:8443", listener: "192.168.10.8:9443", cert: certificate},
		{name: "certificate identity mismatch", origin: "https://192.168.10.9:8443", listener: "192.168.10.9:8443", cert: certificate},
		{name: "DNS identity is not a private address proof", origin: "https://server.lan:8443", listener: "0.0.0.0:8443", cert: certificate},
		{name: "noncanonical IPv6", origin: "https://[fd00:0:0:0:0:0:0:8]:8443", listener: "[fd00::8]:8443", cert: originCertificate(t, "fd00::8")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := api.NewServerHTTPSOrigin(test.origin, test.listener, test.cert)

			// Then
			if !errors.Is(err, media.ErrInvalidConfig) {
				t.Fatalf("unsafe origin error = %v", err)
			}
		})
	}
}

func Test_ServerHTTPSOrigin_rejection_does_not_mutate_certificate_identity(t *testing.T) {
	// Given
	certificate := originCertificate(t, "192.168.10.8")
	before := *certificate
	before.IPAddresses = append([]net.IP(nil), certificate.IPAddresses...)
	before.DNSNames = append([]string(nil), certificate.DNSNames...)

	// When
	_, err := api.NewServerHTTPSOrigin("https://192.168.10.9:8443", "192.168.10.9:8443", certificate)

	// Then
	if !errors.Is(err, media.ErrInvalidConfig) || !reflect.DeepEqual(before.IPAddresses, certificate.IPAddresses) || !reflect.DeepEqual(before.DNSNames, certificate.DNSNames) {
		t.Fatalf("unsafe origin changed certificate identity: %v", err)
	}
}
