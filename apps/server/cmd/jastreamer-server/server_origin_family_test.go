package main

import (
	"net"
	"net/netip"
	"testing"
)

func Test_ServerHTTPSOriginFromListener_wildcard_selects_only_assigned_same_family_SANs(t *testing.T) {
	tests := []struct {
		name        string
		listenerIP  net.IP
		certificate []string
		assigned    []string
		want        string
	}{
		{name: "IPv4 skips IPv6 first", listenerIP: net.IPv4zero, certificate: []string{"fd00::8", "192.168.20.8"}, assigned: []string{"fd00::8", "192.168.20.8"}, want: "https://192.168.20.8:8443"},
		{name: "IPv4 rejects IPv6-only intersection", listenerIP: net.IPv4zero, certificate: []string{"fd00::8"}, assigned: []string{"fd00::8"}},
		{name: "IPv6 skips IPv4 first", listenerIP: net.IPv6zero, certificate: []string{"192.168.20.8", "fd00::8"}, assigned: []string{"192.168.20.8", "fd00::8"}, want: "https://[fd00::8]:8443"},
		{name: "IPv6 rejects IPv4-only intersection", listenerIP: net.IPv6zero, certificate: []string{"192.168.20.8"}, assigned: []string{"192.168.20.8"}},
		{name: "IPv4 loopback stays IPv4", listenerIP: net.IPv4zero, certificate: []string{"::1", "127.0.0.1"}, assigned: []string{"::1", "127.0.0.1"}, want: "https://127.0.0.1:8443"},
		{name: "IPv6 loopback stays IPv6", listenerIP: net.IPv6zero, certificate: []string{"127.0.0.1", "::1"}, assigned: []string{"127.0.0.1", "::1"}, want: "https://[::1]:8443"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			certificate := serverOriginCertificate(t, test.certificate...)
			addresses := make([]localInterfaceAddress, len(test.assigned))
			for index, address := range test.assigned {
				addresses[index] = localInterfaceAddress{Name: "lan", Address: netip.MustParseAddr(address)}
			}

			// When
			origin, err := serverHTTPSOriginFromListener(originSelection(
				&net.TCPAddr{IP: test.listenerIP, Port: 8443}, certificate, nil, assignedAddresses(addresses...),
			))

			// Then
			if test.want == "" {
				if err == nil || origin.String() != "" {
					t.Fatalf("cross-family origin = %q (%v)", origin.String(), err)
				}
				return
			}
			if err != nil || origin.String() != test.want {
				t.Fatalf("origin = %q (%v), want %q", origin.String(), err, test.want)
			}
		})
	}
}

func Test_ServerHTTPSOriginFromListener_applies_interface_filter_before_same_family_selection(t *testing.T) {
	// Given
	certificate := serverOriginCertificate(t, "10.0.0.9", "192.168.20.8", "fd00::8")
	enumerate := assignedAddresses(
		localInterfaceAddress{Name: "office", Address: netip.MustParseAddr("10.0.0.9")},
		localInterfaceAddress{Name: "k17", Address: netip.MustParseAddr("192.168.20.8")},
		localInterfaceAddress{Name: "k17", Address: netip.MustParseAddr("fd00::8")},
	)

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(
		&net.TCPAddr{IP: net.IPv4zero, Port: 8443}, certificate, []string{"k17"}, enumerate,
	))

	// Then
	if err != nil || origin.String() != "https://192.168.20.8:8443" {
		t.Fatalf("K17 interface origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_rejects_IPv4_mapped_IPv6_SAN_and_assignment(t *testing.T) {
	// Given
	certificate := serverOriginCertificate(t, "192.168.20.8")
	mapped := netip.MustParseAddr("::ffff:192.168.20.8")
	certificate.IPAddresses[0] = net.IP(mapped.AsSlice())
	enumerate := assignedAddresses(localInterfaceAddress{Name: "lan", Address: mapped})

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(
		&net.TCPAddr{IP: net.IPv4zero, Port: 8443}, certificate, nil, enumerate,
	))

	// Then
	if err == nil || origin.String() != "" {
		t.Fatalf("mapped address origin = %q (%v)", origin.String(), err)
	}
}

func Test_ServerHTTPSOriginFromListener_live_tcp4_listener_advertises_only_IPv4(t *testing.T) {
	// Given
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	certificate := serverOriginCertificate(t, "::1", "127.0.0.1")
	enumerate := assignedAddresses(
		localInterfaceAddress{Name: "loopback", Address: netip.MustParseAddr("::1")},
		localInterfaceAddress{Name: "loopback", Address: netip.MustParseAddr("127.0.0.1")},
	)

	// When
	origin, err := serverHTTPSOriginFromListener(originSelection(listener.Addr(), certificate, nil, enumerate))

	// Then
	_, port, splitErr := net.SplitHostPort(listener.Addr().String())
	want := "https://127.0.0.1:" + port
	if splitErr != nil || err != nil || origin.String() != want {
		t.Fatalf("live tcp4 origin = %q (%v/%v), want %q", origin.String(), err, splitErr, want)
	}
}
