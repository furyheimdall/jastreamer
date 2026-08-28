package upnp_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

func TestNewNetwork_accepts_only_private_link_local_and_loopback_scopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, address, subnet string
	}{
		{name: "RFC1918 10", address: "10.1.2.3", subnet: "10.1.2.0/24"},
		{name: "RFC1918 172", address: "172.16.2.3", subnet: "172.16.0.0/16"},
		{name: "RFC1918 192", address: "192.168.2.3", subnet: "192.168.2.0/24"},
		{name: "IPv4 link local", address: "169.254.2.3", subnet: "169.254.0.0/16"},
		{name: "loopback test", address: "127.0.0.2", subnet: "127.0.0.0/8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			network, err := upnp.NewNetwork("fixture", test.address, test.subnet)
			// Then
			if err != nil || network.LocalIP.String() != test.address {
				t.Fatalf("network = %+v, error = %v", network, err)
			}
		})
	}
}

func TestNewNetwork_rejects_public_and_overbroad_interface_networks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, address, subnet string
	}{
		{name: "public IPv4", address: "203.0.113.10", subnet: "203.0.113.0/24"},
		{name: "public IPv6", address: "2001:db8::10", subnet: "2001:db8::/64"},
		{name: "unspecified", address: "0.0.0.0", subnet: "0.0.0.0/8"},
		{name: "multicast", address: "239.1.1.1", subnet: "239.0.0.0/8"},
		{name: "private address in public network", address: "10.0.0.1", subnet: "0.0.0.0/0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := upnp.NewNetwork("fixture", test.address, test.subnet)
			// Then
			if !errors.Is(err, upnp.ErrInvalidConfig) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestNewDiscoverer_rejects_public_interface_network_at_startup_boundary(t *testing.T) {
	// Given
	configured := upnp.Network{
		Name: "public", LocalIP: netip.MustParseAddr("203.0.113.10"), Subnet: netip.MustParsePrefix("203.0.113.0/24"),
	}

	// When
	_, err := upnp.NewDiscoverer(upnp.DiscoveryConfig{Networks: []upnp.Network{configured}, ResponseWindow: time.Second})

	// Then
	if !errors.Is(err, upnp.ErrInvalidConfig) {
		t.Fatalf("error = %v", err)
	}
}

func TestParseAdvertisement_rejects_spoofed_and_off_subnet_responses(t *testing.T) {
	t.Parallel()
	// Given
	network, err := upnp.NewNetwork("fixture", "127.0.0.1", "127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte("HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nUSN: uuid:k17::urn:schemas-upnp-org:device:MediaRenderer:1\r\nLOCATION: http://127.0.0.1:49152/device.xml\r\n\r\n")

	tests := []struct {
		name   string
		source netip.AddrPort
		data   []byte
	}{
		{name: "off subnet source", source: netip.MustParseAddrPort("192.0.2.10:1900"), data: packet},
		{name: "location host differs from source", source: netip.MustParseAddrPort("127.0.0.1:1900"), data: []byte("HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nUSN: uuid:k17\r\nLOCATION: http://127.0.0.2:49152/device.xml\r\n\r\n")},
		{name: "wrong search target", source: netip.MustParseAddrPort("127.0.0.1:1900"), data: []byte("HTTP/1.1 200 OK\r\nST: ssdp:all\r\nUSN: uuid:k17\r\nLOCATION: http://127.0.0.1:49152/device.xml\r\n\r\n")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, parseErr := upnp.ParseAdvertisement(test.data, test.source, []upnp.Network{network})
			// Then
			if !errors.Is(parseErr, upnp.ErrUntrustedAdvertisement) {
				t.Fatalf("error = %v", parseErr)
			}
		})
	}
}

func TestDiscover_returns_when_context_is_cancelled_after_search_is_sent(t *testing.T) {
	// Given
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	searchReceived := make(chan struct{})
	go func() {
		buffer := make([]byte, 2048)
		_, _, readErr := server.ReadFromUDP(buffer)
		if readErr == nil {
			close(searchReceived)
		}
	}()
	network, err := upnp.NewNetwork("lo", "127.0.0.1", "127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	discoverer, err := upnp.NewDiscoverer(upnp.DiscoveryConfig{Networks: []upnp.Network{network}, SearchAddress: server.LocalAddr().String(), ResponseWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, discoverErr := discoverer.Discover(ctx)
		result <- discoverErr
	}()
	select {
	case <-searchReceived:
	case <-time.After(time.Second):
		t.Fatal("search was not sent")
	}

	// When
	cancel()

	// Then
	select {
	case discoverErr := <-result:
		if !errors.Is(discoverErr, context.Canceled) {
			t.Fatalf("error = %v", discoverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery did not observe cancellation")
	}
}

func TestDiscover_binds_configured_network_and_ignores_other_sources(t *testing.T) {
	// Given
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	received := make(chan *net.UDPAddr, 1)
	go func() {
		buffer := make([]byte, 2048)
		_, source, readErr := server.ReadFromUDP(buffer)
		if readErr != nil {
			return
		}
		received <- source
		response := "HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nUSN: uuid:k17\r\nLOCATION: http://127.0.0.1:49152/device.xml\r\n\r\n"
		_, _ = server.WriteToUDP([]byte(response), source)
	}()
	network, err := upnp.NewNetwork("lo", "127.0.0.1", "127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	discoverer, err := upnp.NewDiscoverer(upnp.DiscoveryConfig{Networks: []upnp.Network{network}, SearchAddress: server.LocalAddr().String(), ResponseWindow: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// When
	candidates, discoverErr := discoverer.Discover(ctx)

	// Then
	if discoverErr != nil {
		t.Fatal(discoverErr)
	}
	if len(candidates) != 1 || candidates[0].USN != "uuid:k17" {
		t.Fatalf("candidates = %+v", candidates)
	}
	select {
	case source := <-received:
		if !source.IP.Equal(net.ParseIP("127.0.0.1")) {
			t.Fatalf("source = %s", source)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
