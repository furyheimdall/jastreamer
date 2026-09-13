package main

import (
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

func TestMediaOriginUsesDiscoveredInterfaceBeforeDefaultRoute(t *testing.T) {
	value := config.Default()
	value.HTTP.Address = ":8080"
	device := output.Device{Address: "127.0.0.1", LocalAddress: "127.0.0.2", Protocol: output.ProtocolUPnP}
	origin, err := mediaOrigin(value)(device)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "http://127.0.0.2:8080" {
		t.Fatalf("media origin ignored the discovered interface: %s", origin)
	}
}

func TestExplicitMediaOriginAndListenerOverrideDiscoveredInterface(t *testing.T) {
	device := output.Device{Address: "127.0.0.1", LocalAddress: "127.0.0.2", Protocol: output.ProtocolUPnP}
	value := config.Default()
	value.HTTP.Address = "127.0.0.3:8080"
	origin, err := mediaOrigin(value)(device)
	if err != nil || origin != "http://127.0.0.3:8080" {
		t.Fatalf("media URL did not respect the bound listener: origin=%s err=%v", origin, err)
	}
	value.Media.BaseURL = "https://127.0.0.4:9443"
	origin, err = mediaOrigin(value)(device)
	if err != nil || origin != value.Media.BaseURL {
		t.Fatalf("explicit media origin did not take precedence: origin=%s err=%v", origin, err)
	}
}
