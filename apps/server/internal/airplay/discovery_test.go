package airplay

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/enbility/zeroconf/v3"
)

func TestAdvertisementDecodesDNSPresentationEscapesInDeviceName(t *testing.T) {
	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: `021122334455@Living\032Room\.\032Hi\\Fi`, Service: raopService, Domain: "local."},
		HostName:      "receiver.local.", Port: 7000,
		AddrIPv4: []net.IP{net.ParseIP("192.168.99.10")},
	}
	networks := []localNetwork{{local: netip.MustParseAddr("192.168.99.1"), prefix: netip.MustParsePrefix("192.168.99.0/24")}}
	device, ok := advertisementFromEntry(entry, networks, time.Unix(100, 0))
	if !ok {
		t.Fatal("valid receiver with an escaped instance name was rejected")
	}
	if device.name != `Living Room. Hi\Fi` {
		t.Fatalf("device name = %q, want human-readable DNS-SD label", device.name)
	}
}
