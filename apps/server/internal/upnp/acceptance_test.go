package upnp_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

func TestK17Fixture_discovers_inspects_controls_and_pulls_server_media(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol, seek: true})
	ssdp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer ssdp.Close()
	served := make(chan struct{})
	go func() {
		defer close(served)
		buffer := make([]byte, 2048)
		_, source, readErr := ssdp.ReadFromUDP(buffer)
		if readErr != nil {
			return
		}
		response := "HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nUSN: uuid:k17\r\nLOCATION: " + fixture.server.URL + "/device.xml\r\n\r\n"
		_, _ = ssdp.WriteToUDP([]byte(response), source)
	}()
	network, err := upnp.NewNetwork("fixture", "127.0.0.1", "127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	discoverer, err := upnp.NewDiscoverer(upnp.DiscoveryConfig{Networks: []upnp.Network{network}, SearchAddress: ssdp.LocalAddr().String(), ResponseWindow: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	// When
	candidates, err := discoverer.Discover(context.Background())
	if err != nil || len(candidates) != 1 {
		t.Fatalf("discover = %+v, %v", candidates, err)
	}
	device, err := fixture.inspector(t).InspectK17(context.Background(), candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	mediaURL := fixture.server.URL + "/media/v1/signed-fixture"
	if err := adapter.SetAVTransportURI(context.Background(), fixtureMediaResource(mediaURL)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := fixture.server.Client().Get(mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}

	// Then
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("SSDP fixture did not finish")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.mediaURL != mediaURL || fixture.mediaBody != "fixture-media" {
		t.Fatalf("media URI/pull = %q/%q", fixture.mediaURL, fixture.mediaBody)
	}
}
