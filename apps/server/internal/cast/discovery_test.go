package cast

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/enbility/zeroconf/v3"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

func TestAdvertisementUsesUUIDAdvertisedPortAndReceivingInterface(t *testing.T) {
	now := time.Unix(100, 0)
	network := localNetwork{
		iface: net.Interface{Index: 7, Name: "lan0"},
		local: netip.MustParseAddr("192.168.50.2"), prefix: netip.MustParsePrefix("192.168.50.0/24"),
	}
	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: "Living Room", Service: castService, Domain: "local."},
		HostName:      "cast.local.", Port: 9443,
		AddrIPv4: []net.IP{net.ParseIP("10.0.0.8"), net.ParseIP("192.168.50.10")},
		Text:     []string{"id=11111111-2222-4333-8444-555555555555", "fn=Living Room", "md=Cast Audio"},
		Expiry:   now.Add(2 * time.Minute),
	}
	value, ok := advertisementFromEntry(entry, network, now)
	if !ok {
		t.Fatal("valid Cast advertisement was rejected")
	}
	if value.identity != "11111111-2222-4333-8444-555555555555" || value.port != 9443 ||
		value.address.String() != "192.168.50.10" || value.localAddress != network.local {
		t.Fatalf("advertisement = %+v", value)
	}
}

func TestAdvertisementDecodesUTF8FriendlyNameFromDNS(t *testing.T) {
	network := localNetwork{local: netip.MustParseAddr("192.168.50.2"), prefix: netip.MustParsePrefix("192.168.50.0/24")}
	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: "Home", Service: castService, Domain: "local."},
		HostName:      "cast.local.", Port: 8009, AddrIPv4: []net.IP{net.ParseIP("192.168.50.10")},
		Text: []string{"id=11111111-2222-4333-8444-555555555555", `fn=\236\149\136\235\176\169`, "md=Google Home Mini"},
	}
	value, ok := advertisementFromEntry(entry, network, time.Now())
	if !ok || value.name != "안방" {
		t.Fatalf("DNS-escaped friendly name was not decoded: %+v, accepted=%v", value, ok)
	}
}

func TestAdvertisementRejectsMissingUUIDAndOffSubnetAddress(t *testing.T) {
	network := localNetwork{
		iface: net.Interface{Index: 7, Name: "lan0"},
		local: netip.MustParseAddr("192.168.50.2"), prefix: netip.MustParsePrefix("192.168.50.0/24"),
	}
	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: "Receiver", Service: castService, Domain: "local."},
		HostName:      "cast.local.", Port: 8009, AddrIPv4: []net.IP{net.ParseIP("10.0.0.8")},
		Text: []string{"fn=Receiver"},
	}
	if _, ok := advertisementFromEntry(entry, network, time.Now()); ok {
		t.Fatal("unidentified off-subnet advertisement was accepted")
	}
}

func TestExpiredCastDeviceTransitionsOfflineOnce(t *testing.T) {
	notifications := 0
	manager := &Manager{
		config: Config{Notify: func(topic string) {
			if topic == "renderers" {
				notifications++
			}
		}},
		devices: map[string]deviceRecord{
			"cast:test": {
				device:    output.Device{ID: "cast:test", Online: true},
				expiresAt: time.Unix(100, 0), session: &playbackSession{},
			},
		},
	}
	manager.expire(time.Unix(101, 0))
	manager.expire(time.Unix(102, 0))
	device, ok := manager.Device("cast:test")
	if !ok || device.Online || notifications != 1 {
		t.Fatalf("expired device = %+v, found=%v, notifications=%d", device, ok, notifications)
	}
}
