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

func TestRAOPAndAirPlayAdvertisementsShareEscapedReceiverIdentity(t *testing.T) {
	networks := []localNetwork{{local: netip.MustParseAddr("192.168.99.1"), prefix: netip.MustParsePrefix("192.168.99.0/24")}}
	now := time.Unix(100, 0)
	raop := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: `021122334455\@Living\ Room`, Service: raopService, Domain: "local."},
		HostName:      "receiver.local.", Port: 5000,
		AddrIPv4: []net.IP{net.ParseIP("192.168.99.10")},
	}
	airplay := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: `Living\ Room`, Service: airplayService, Domain: "local."},
		HostName:      "receiver.local.", Port: 7000,
		AddrIPv4: []net.IP{net.ParseIP("192.168.99.10")},
		Text:     []string{"deviceid=02:11:22:33:44:55", "pi=11111111-2222-4333-8444-555555555555"},
	}
	audio, audioOK := advertisementFromEntry(raop, networks, now)
	control, controlOK := advertisementFromEntry(airplay, networks, now)
	if !audioOK || !controlOK {
		t.Fatal("valid receiver advertisements were rejected")
	}
	if audio.identity != control.identity {
		t.Fatalf("one receiver has separate RAOP and AirPlay identities: %q != %q", audio.identity, control.identity)
	}
	airplay.Text = []string{"deviceid=02:11:22:33:44:66"}
	other, ok := advertisementFromEntry(airplay, networks, now)
	if !ok || other.identity == audio.identity {
		t.Fatal("different receivers sharing a display name were merged")
	}
}
