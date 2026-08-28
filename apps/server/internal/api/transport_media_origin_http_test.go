package api_test

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type transportTLSServerConfig struct {
	API           api.Config
	Network       string
	Address       string
	CertificateIP string
}

func trustedIPv4TransportServer(config api.Config) transportTLSServerConfig {
	return transportTLSServerConfig{API: config, Network: "tcp4", Address: "127.0.0.1:0", CertificateIP: "127.0.0.1"}
}

func newTrustedTransportTLSServer(t *testing.T, value transportMediaFixture, input transportTLSServerConfig) *httptest.Server {
	t.Helper()
	identity, err := security.LoadOrCreateIdentity(security.IdentityConfig{
		Directory: t.TempDir(), IPAddresses: []net.IP{net.ParseIP(input.CertificateIP)},
	})
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(identity.Certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	if err := server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener, err = net.Listen(input.Network, input.Address)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := api.NewServerHTTPSOrigin("https://"+server.Listener.Addr().String(), server.Listener.Addr().String(), certificate)
	if err != nil {
		t.Fatal(err)
	}
	input.API.ServerHTTPSOrigin = origin
	server.Config.Handler = value.handler(input.API)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{identity.Certificate}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func Test_Transport_K17_authenticated_start_ignores_host_headers_and_uses_trusted_capability_origin(t *testing.T) {
	tests := []transportTLSServerConfig{
		{Network: "tcp4", Address: "127.0.0.1:0", CertificateIP: "127.0.0.1"},
		{Network: "tcp6", Address: "[::1]:0", CertificateIP: "::1"},
	}
	for _, test := range tests {
		t.Run(test.Network, func(t *testing.T) {
			// Given
			value := newTransportMediaFixture(t, playback.RendererKindK17)
			server := newTrustedTransportTLSServer(t, value, test)

			// When
			startTransportWithHost(t, server, value.controller, "attacker.invalid:9443", "forwarded-attacker.invalid:9554")

			// Then
			if !strings.HasPrefix(value.adapter.mediaURL, server.URL+"/media/v1/") || strings.Contains(value.adapter.mediaURL, "attacker.invalid") {
				t.Fatalf("K17 media URL = %q, want trusted HTTPS origin %q", value.adapter.mediaURL, server.URL)
			}
			token := strings.TrimPrefix(value.adapter.mediaURL, server.URL+"/media/v1/")
			claims, err := value.signer.Verify(token, media.AudienceK17Capability, value.rendererID)
			if err != nil || claims.Audience != media.AudienceK17Capability {
				t.Fatalf("K17 capability claims = %+v (%v)", claims, err)
			}
			pullTransportMedia(t, server.Client(), value.adapter.mediaURL)
		})
	}
}

func Test_Transport_K17_authenticated_start_does_not_call_renderer_without_trusted_HTTPS_origin(t *testing.T) {
	// Given
	value := newTransportMediaFixture(t, playback.RendererKindK17)
	server := httptest.NewTLSServer(value.handler(api.Config{}))
	defer server.Close()

	// When
	startTransportWithHost(t, server, value.controller, "attacker.invalid:9443", "forwarded-attacker.invalid:9554")

	// Then
	if value.adapter.uriCalls != 0 || value.adapter.playCalls != 0 || value.adapter.mediaURL != "" {
		t.Fatalf("unsafe config mutated renderer: URI calls=%d play calls=%d URL=%q", value.adapter.uriCalls, value.adapter.playCalls, value.adapter.mediaURL)
	}
}
