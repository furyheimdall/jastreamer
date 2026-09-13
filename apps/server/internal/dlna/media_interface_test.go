package dlna

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func TestDiscoveredInterfaceBuildsAcceptedMediaURIWithoutWeakeningAdmission(t *testing.T) {
	const udn = "uuid:media-interface-fixture"
	var requests atomic.Int32
	uris := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/description.xml":
			_, _ = io.WriteString(w, rendererDescription(udn, "media-interface"))
		case "/avtransport.xml":
			_, _ = io.WriteString(w, avTransportDescription())
		case "/control":
			requests.Add(1)
			var envelope struct {
				URI string `xml:"Body>SetAVTransportURI>CurrentURI"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&envelope); err != nil {
				http.Error(w, "invalid SOAP", http.StatusBadRequest)
				return
			}
			uris <- envelope.URI
			_, _ = io.WriteString(w, soapResponse("urn:schemas-upnp-org:service:AVTransport:1", "SetAVTransportURI"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	manager := fixtureManager(server.Client())
	candidate := fixtureAdvertisement(server.URL+"/description.xml", udn, "1")
	manager.acceptAdvertisement(context.Background(), candidate)
	discovered := waitForDevice(t, manager, func(device output.Device) bool { return device.Online })
	device, found := manager.Device(discovered.ID)
	if !found {
		t.Fatal("discovered renderer disappeared")
	}
	resource := output.Resource{URL: "http://" + net.JoinHostPort(device.LocalAddress, "8080") + "/media/test", Mime: "audio/flac", Title: "That's Not…", Size: 22973126, DurationMS: 228840}
	if err := manager.SetURI(context.Background(), device.ID, resource); err != nil {
		t.Fatalf("media URI could not use the discovered interface: %v", err)
	}
	if received := <-uris; received != "http://127.0.0.1:8080/media/test" {
		t.Fatalf("renderer received wrong media origin: %s", received)
	}
	resource.URL = "http://10.77.0.1:8080/media/test"
	if err := manager.SetURI(context.Background(), device.ID, resource); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("media URI on an unrelated network was accepted: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatal("invalid media origin reached the renderer")
	}
}
