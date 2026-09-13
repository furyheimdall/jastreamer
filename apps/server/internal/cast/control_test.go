package cast

import (
	"net/netip"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func TestValidateResourceConfinesMediaAndArtworkToDiscoveredNetwork(t *testing.T) {
	target := endpoint{
		address: netip.MustParseAddr("192.168.20.10"), local: netip.MustParseAddr("192.168.20.2"),
		port: 8009, network: netip.MustParsePrefix("192.168.20.0/24"),
	}
	resource := output.Resource{
		URL: "http://192.168.20.2:8080/media/play", ArtworkURL: "http://192.168.20.2:8080/artwork/cover",
		Mime: "audio/flac", Title: "Track", DurationMS: 1000,
	}
	if err := validateResource(resource, target); err != nil {
		t.Fatalf("local media resource rejected: %v", err)
	}
	resource.URL = "http://10.0.0.2:8080/media/play"
	if err := validateResource(resource, target); err == nil {
		t.Fatal("off-subnet media resource was accepted")
	}
	resource.URL = "http://192.168.20.2:8080/media/play"
	resource.ArtworkURL = "https://203.0.113.1/cover"
	if err := validateResource(resource, target); err == nil {
		t.Fatal("public artwork resource was accepted")
	}
}
