package httpapi

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"
)

func TestNetworkInterfacesRouteRequiresAuthentication(t *testing.T) {
	fixture := startAPI(t, false)
	expectStatus(t, fixture.request(t, http.MethodGet, "/api/v1/network/interfaces", "", nil), http.StatusUnauthorized)
}

func TestNetworkInterfacesRouteReturnsServerInterfaceMetadata(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	response := fixture.request(t, http.MethodGet, "/api/v1/network/interfaces", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("network interfaces status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	var body struct {
		Interfaces []networkInterface `json:"interfaces"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Interfaces == nil {
		t.Fatal("network interfaces response used null instead of an array")
	}
	for _, iface := range body.Interfaces {
		if iface.Name == "" || iface.Index <= 0 || iface.Addresses == nil {
			t.Fatalf("invalid network interface metadata=%#v", iface)
		}
		for _, address := range iface.Addresses {
			parsed, err := netip.ParseAddr(address.Address)
			if err != nil || address.PrefixLength < 0 || address.PrefixLength > parsed.BitLen() {
				t.Fatalf("invalid network address metadata=%#v", address)
			}
			if address.UPnPUsable && (!iface.Up || !iface.Multicast || iface.Loopback || !parsed.Is4()) {
				t.Fatalf("ineligible address marked UPnP usable: interface=%#v address=%#v", iface, address)
			}
		}
	}
}

func TestUPnPUsableAddressMatchesDLNALANPolicy(t *testing.T) {
	tests := []struct {
		name      string
		up        bool
		multicast bool
		loopback  bool
		prefix    string
		want      bool
	}{
		{name: "private 10", up: true, multicast: true, prefix: "10.20.30.40/24", want: true},
		{name: "private 172", up: true, multicast: true, prefix: "172.20.1.5/16", want: true},
		{name: "private 192", up: true, multicast: true, prefix: "192.168.1.98/24", want: true},
		{name: "link local", up: true, multicast: true, prefix: "169.254.2.3/16", want: true},
		{name: "CGNAT", up: true, multicast: true, prefix: "100.105.182.12/10", want: false},
		{name: "public", up: true, multicast: true, prefix: "203.0.113.10/24", want: false},
		{name: "IPv6", up: true, multicast: true, prefix: "fd00::1/64", want: false},
		{name: "loopback adapter", up: true, multicast: true, loopback: true, prefix: "127.0.0.1/8", want: false},
		{name: "down adapter", multicast: true, prefix: "192.168.1.98/24", want: false},
		{name: "non multicast adapter", up: true, prefix: "192.168.1.98/24", want: false},
		{name: "prefix outside private scope", up: true, multicast: true, prefix: "10.20.30.40/4", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefix := netip.MustParsePrefix(test.prefix)
			if got := upnpUsableAddress(test.up, test.multicast, test.loopback, prefix); got != test.want {
				t.Fatalf("upnpUsableAddress(%q)=%t, want %t", test.prefix, got, test.want)
			}
		})
	}
}
