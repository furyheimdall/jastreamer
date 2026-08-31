package pairing_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/web/pairing"
)

func TestAssets_expose_only_pairing_administration_API_routes(t *testing.T) {
	// Given
	javascript, err := fs.ReadFile(pairing.Assets, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}

	// When
	routes := string(javascript)

	// Then
	for _, required := range []string{"/api/v1/identity", "/api/v1/bootstrap", "/api/v1/pairing-codes", "/api/v1/devices"} {
		if !strings.Contains(routes, required) {
			t.Fatalf("missing administrative route %q", required)
		}
	}
	for _, forbidden := range []string{"/api/v1/catalog", "/api/v1/zones", "playback-state", "continuation-policy", "queue"} {
		if strings.Contains(routes, forbidden) {
			t.Fatalf("portal contains Control route token %q", forbidden)
		}
	}
}

func TestAssets_declare_embedded_favicon(t *testing.T) {
	index, err := fs.ReadFile(pairing.Assets, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if !strings.Contains(string(index), `rel="icon" href="favicon.svg"`) {
		t.Fatal("pairing portal does not declare its embedded favicon")
	}
	if _, err := fs.ReadFile(pairing.Assets, "favicon.svg"); err != nil {
		t.Fatalf("read favicon.svg: %v", err)
	}
}
