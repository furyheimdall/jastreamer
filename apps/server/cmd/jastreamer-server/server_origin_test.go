package main

import (
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func serverOriginCertificate(t *testing.T, addresses ...string) *x509.Certificate {
	t.Helper()
	ips := make([]net.IP, len(addresses))
	for index, address := range addresses {
		ips[index] = net.ParseIP(address)
	}
	identity, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: t.TempDir(), DNSNames: []string{"server.lan"}, IPAddresses: ips})
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(identity.Certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func assignedAddresses(values ...localInterfaceAddress) interfaceAddressEnumerator {
	return func() ([]localInterfaceAddress, error) { return values, nil }
}

func originSelection(listener net.Addr, certificate *x509.Certificate, interfaces []string, enumerate interfaceAddressEnumerator) serverHTTPSOriginConfig {
	return serverHTTPSOriginConfig{
		Listener: listener, Certificate: certificate, K17Interfaces: interfaces, Enumerate: enumerate,
	}
}

func Test_ServerHTTPSOriginFromListener_exact_listener_keeps_certificate_bound_rule_without_enumeration(t *testing.T) {
	// Given
	listener := &net.TCPAddr{IP: net.ParseIP("192.168.20.8"), Port: 8443}
	certificate := serverOriginCertificate(t, "192.168.20.8")
	enumeratorFailure := errors.New("must not enumerate exact listener")

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, nil, func() ([]localInterfaceAddress, error) {
		return nil, enumeratorFailure
	}))

	// Then
	if err != nil || origin.String() != "https://192.168.20.8:8443" {
		t.Fatalf("origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_skips_stale_private_SAN_before_valid_assigned_SAN(t *testing.T) {
	// Given
	listener := &net.TCPAddr{IP: net.IPv4zero, Port: 8443}
	certificate := serverOriginCertificate(t, "192.168.20.99", "192.168.20.8")
	enumerate := assignedAddresses(localInterfaceAddress{Name: "ethernet", Address: netip.MustParseAddr("192.168.20.8")})

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, nil, enumerate))

	// Then
	if err != nil || origin.String() != "https://192.168.20.8:8443" {
		t.Fatalf("stale SAN selected: origin=%q error=%v", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_accepts_assigned_IPv4_and_canonical_IPv6(t *testing.T) {
	tests := []struct {
		name     string
		listener net.Addr
		address  string
		want     string
	}{
		{name: "assigned IPv4", listener: &net.TCPAddr{IP: net.IPv4zero, Port: 8443}, address: "10.0.0.8", want: "https://10.0.0.8:8443"},
		{name: "canonical assigned IPv6", listener: &net.TCPAddr{IP: net.IPv6zero, Port: 8443}, address: "fd00::8", want: "https://[fd00::8]:8443"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			certificate := serverOriginCertificate(t, test.address)
			enumerate := assignedAddresses(localInterfaceAddress{Name: "lan", Address: netip.MustParseAddr(test.address)})

			// When
			origin, err := serverHTTPSOriginFromListener(originSelection(test.listener, certificate, nil, enumerate))

			// Then
			if err != nil || origin.String() != test.want {
				t.Fatalf("origin = %q (%v), want %q", origin.String(), err, test.want)
			}
		})
	}
}

func Test_ServerHTTPSOriginFromListener_rejects_no_assigned_private_SAN_intersection(t *testing.T) {
	// Given: hostname SAN, public SAN, and stale private SAN cannot prove a local private listener identity.
	listener := &net.TCPAddr{IP: net.IPv6zero, Port: 8443}
	certificate := serverOriginCertificate(t, "203.0.113.8", "192.168.20.99")
	enumerate := assignedAddresses(
		localInterfaceAddress{Name: "wan", Address: netip.MustParseAddr("203.0.113.8")},
		localInterfaceAddress{Name: "lan", Address: netip.MustParseAddr("192.168.20.8")},
	)

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, nil, enumerate))

	// Then
	if err == nil || origin.String() != "" {
		t.Fatalf("unsafe origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_uses_certificate_order_for_multi_homed_assigned_SANs(t *testing.T) {
	// Given: enumeration order must not override certificate SAN order.
	listener := &net.TCPAddr{IP: net.IPv4zero, Port: 8443}
	certificate := serverOriginCertificate(t, "192.168.20.8", "10.0.0.9")
	enumerate := assignedAddresses(
		localInterfaceAddress{Name: "second", Address: netip.MustParseAddr("10.0.0.9")},
		localInterfaceAddress{Name: "first", Address: netip.MustParseAddr("192.168.20.8")},
	)

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, nil, enumerate))

	// Then
	if err != nil || origin.String() != "https://192.168.20.8:8443" {
		t.Fatalf("multi-homed origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_uses_assigned_loopback_only_after_private_SANs(t *testing.T) {
	// Given
	listener := &net.TCPAddr{IP: net.IPv4zero, Port: 8443}
	certificate := serverOriginCertificate(t, "127.0.0.1", "192.168.20.8")
	enumerate := assignedAddresses(localInterfaceAddress{Name: "loopback", Address: netip.MustParseAddr("127.0.0.1")})

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, nil, enumerate))

	// Then
	if err != nil || origin.String() != "https://127.0.0.1:8443" {
		t.Fatalf("loopback origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_uses_certificate_order_within_configured_K17_interfaces(t *testing.T) {
	// Given
	listener := &net.TCPAddr{IP: net.IPv6zero, Port: 8443}
	certificate := serverOriginCertificate(t, "10.0.0.9", "fd00::8", "192.168.20.8")
	enumerate := assignedAddresses(
		localInterfaceAddress{Name: "office", Address: netip.MustParseAddr("10.0.0.9")},
		localInterfaceAddress{Name: "k17", Address: netip.MustParseAddr("fd00::8")},
		localInterfaceAddress{Name: "k17", Address: netip.MustParseAddr("192.168.20.8")},
	)

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, []string{"k17"}, enumerate))

	// Then: the first assigned private SAN on a configured K17 interface wins.
	if err != nil || origin.String() != "https://[fd00::8]:8443" {
		t.Fatalf("multi-homed origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_returns_enumerator_failure(t *testing.T) {
	// Given
	enumeratorFailure := errors.New("interface enumeration failed")
	listener := &net.TCPAddr{IP: net.IPv4zero, Port: 8443}
	certificate := serverOriginCertificate(t, "192.168.20.8")

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener, certificate, nil, func() ([]localInterfaceAddress, error) {
		return nil, enumeratorFailure
	}))

	// Then
	if !errors.Is(err, enumeratorFailure) || origin.String() != "" {
		t.Fatalf("enumerator failure = origin %q error %v", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_rejects_persisted_certificate_after_interface_change(t *testing.T) {
	// Given
	listener := &net.TCPAddr{IP: net.IPv4zero, Port: 8443}
	certificate := serverOriginCertificate(t, "192.168.20.8")
	selection := originSelection(listener, certificate, nil, assignedAddresses(
		localInterfaceAddress{Name: "lan", Address: netip.MustParseAddr("192.168.20.8")},
	))
	first, firstErr := serverHTTPSOriginFromListener(selection)

	// When: the persisted certificate remains, but the address moved off this host before restart.
	selection.Enumerate = assignedAddresses(localInterfaceAddress{Name: "lan", Address: netip.MustParseAddr("192.168.20.9")})
	second, secondErr := serverHTTPSOriginFromListener(selection)

	// Then
	if firstErr != nil || first.String() != "https://192.168.20.8:8443" || secondErr == nil || second.String() != "" {
		t.Fatalf("before=%q/%v after=%q/%v", first.String(), firstErr, second.String(), secondErr)
	}
}
