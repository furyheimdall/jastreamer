package media_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
)

func TestHandler_serves_generated_five_codec_original_fixtures(t *testing.T) {
	t.Parallel()
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "fixtures", Key: []byte(strings.Repeat("f", 32)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	snapshot := catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{}}
	fixtures := []struct {
		name   string
		format catalog.Format
		mime   string
	}{
		{name: "flac", format: catalog.FormatFLAC, mime: "audio/flac"},
		{name: "mp3", format: catalog.FormatMP3, mime: "audio/mpeg"},
		{name: "ogg", format: catalog.FormatOggVorbis, mime: "audio/ogg"},
		{name: "opus", format: catalog.FormatOpus, mime: "audio/ogg"},
		{name: "wav", format: catalog.FormatPCMWAV, mime: "audio/wav"},
	}
	for _, fixture := range fixtures {
		encoded, readErr := os.ReadFile(filepath.Join("../../../../tooling/fixtures/music", "real."+fixture.name+".b64"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		content, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		path := filepath.Join(root, fixture.name)
		if writeErr := os.WriteFile(path, content, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		id := catalog.TrackID(fixture.name)
		snapshot.Tracks[id] = catalog.Track{RootID: "fixtures", TrackID: id, RelativePath: fixture.name, Format: fixture.format, Fingerprint: strings.Repeat(fixture.name[:1], 64), FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()}, Available: true}
	}
	service, err := media.NewService(media.ServiceConfig{Signer: signer, Authorizer: authorizer{}, Snapshot: func(context.Context) catalog.Snapshot { return snapshot }, Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"fixtures": root} }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(media.MediaOnlyHandler(service.K17Handler()))
	defer server.Close()

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			track := snapshot.Tracks[catalog.TrackID(fixture.name)]
			token, signErr := signer.Sign(media.Grant{Audience: media.AudienceK17Capability, RendererID: "renderer", ZoneID: "zone", PlayID: "play", TrackID: track.TrackID, Representation: media.Original, FileSize: track.FileVersion.Size, ModifiedNS: track.FileVersion.Modified.UnixNano()})
			if signErr != nil {
				t.Fatal(signErr)
			}
			// When
			response, getErr := http.Get(server.URL + "/media/v1/" + token)
			if getErr != nil {
				t.Fatal(getErr)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			// Then
			if readErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != fixture.mime || int64(len(body)) != track.FileVersion.Size {
				t.Fatalf("response = %d type=%q bytes=%d err=%v", response.StatusCode, response.Header.Get("Content-Type"), len(body), readErr)
			}
		})
	}
}

func TestMediaOnlyHandler_exposes_only_signed_media(t *testing.T) {
	t.Parallel()
	// Given
	handler := media.MediaOnlyHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
	server := httptest.NewServer(handler)
	defer server.Close()

	// When / Then
	for _, path := range []string{"/api/v1/catalog/tracks", "/admin/", "/pair/", "/media/v1/not-signed"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		want := http.StatusNotFound
		if strings.HasPrefix(path, "/media/v1/") {
			want = http.StatusNoContent
		}
		if response.StatusCode != want {
			t.Fatalf("%s status = %d, want %d", path, response.StatusCode, want)
		}
	}
	parsed, _ := url.Parse(server.URL)
	if parsed.Scheme != "http" {
		t.Fatalf("scheme = %q", parsed.Scheme)
	}
}
