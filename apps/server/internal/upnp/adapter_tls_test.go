package upnp_test

import (
	"context"
	"html"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

type fixtureMediaAuthorizer struct{}

func (fixtureMediaAuthorizer) AuthorizeMedia(context.Context, playback.MediaAuthorization) error {
	return nil
}

func TestAdapter_TLS_SOAP_fixture_pulls_signed_HTTPS_media_and_receives_escaped_DIDL(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "fixture.flac")
	if err := os.WriteFile(path, []byte("signed-tls-media"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	track := catalog.Track{
		RootID: "root", TrackID: "track&one", RelativePath: "fixture.flac", Format: catalog.FormatFLAC,
		Fingerprint: strings.Repeat("a", 64), FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()},
		Metadata: catalog.Metadata{Title: "Rock & <Roll>"}, Available: true,
	}
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "tls", Key: []byte(strings.Repeat("t", 32)), Clock: adapterClock{now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	mediaService, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: fixtureMediaAuthorizer{},
		Snapshot: func(context.Context) catalog.Snapshot {
			return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{track.TrackID: track}}
		},
		Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"root": root} },
	})
	if err != nil {
		t.Fatal(err)
	}
	mediaServer := httptest.NewTLSServer(media.MediaOnlyHandler(mediaService.K17Handler()))
	defer mediaServer.Close()
	issued, err := mediaService.IssueMedia(context.Background(), media.IssueRequest{
		BaseURL: mediaServer.URL, Audience: media.AudienceK17Capability, RendererID: device.ID,
		ZoneID: "living", PlayID: "play", TrackID: track.TrackID, Capabilities: []string{fixtureProtocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.pullClient = mediaServer.Client()
	fixture.mu.Unlock()
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: device.ID, ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = adapter.SetAVTransportURI(context.Background(), playback.MediaResource{
		URL: issued.URL, MimeType: issued.MimeType, TrackID: playback.TrackID(issued.TrackID),
		Title: issued.Title, Representation: playback.MediaOriginal,
	})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	metadata := fixture.mediaMetadata
	pulled := fixture.pulledMedia
	fixture.mu.Unlock()
	didl := html.UnescapeString(metadata)
	if pulled != "signed-tls-media" || !strings.HasPrefix(issued.URL, "https://") ||
		!strings.Contains(metadata, "&lt;DIDL-Lite") || !strings.Contains(didl, `id="track&amp;one"`) ||
		!strings.Contains(didl, "Rock &amp; &lt;Roll&gt;") || !strings.Contains(didl, `protocolInfo="http-get:*:audio/flac:*"`) ||
		!strings.Contains(didl, `jas:representation="original"`) || !strings.Contains(didl, ">"+issued.URL+"</res>") {
		t.Fatalf("url=%q pulled=%q metadata=%q didl=%q", issued.URL, pulled, metadata, didl)
	}
}

type adapterClock struct{ now time.Time }

func (clock adapterClock) Now() time.Time { return clock.now }
