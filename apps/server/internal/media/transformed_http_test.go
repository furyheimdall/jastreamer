package media_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
)

type transformProvider struct{ opens int }

func (provider *transformProvider) Open(context.Context, io.Reader, catalog.Format) (io.ReadCloser, error) {
	provider.opens++
	return io.NopCloser(strings.NewReader("pcm-l16")), nil
}

func TestHandler_serves_L16_only_through_configured_provider_and_rejects_Range(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	path := filepath.Join(root, "source.opus")
	if err := os.WriteFile(path, []byte("encoded-opus"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	track := catalog.Track{RootID: "root", TrackID: "track", RelativePath: "source.opus", Format: catalog.FormatOpus, FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()}, Available: true}
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "transform", Key: []byte(strings.Repeat("t", 32)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	provider := &transformProvider{}
	service, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: authorizer{}, Transformer: provider,
		Snapshot: func(context.Context) catalog.Snapshot {
			return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{"track": track}}
		},
		Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"root": root} },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(media.MediaOnlyHandler(service.K17Handler()))
	defer server.Close()
	issued, err := service.Issue(context.Background(), media.IssueRequest{BaseURL: server.URL, Audience: media.AudienceK17Capability, RendererID: "renderer", ZoneID: "zone", PlayID: "play", TrackID: "track", Capabilities: []string{"http-get:*:audio/L16:*"}})
	if err != nil {
		t.Fatal(err)
	}

	// When
	response, err := http.Get(issued)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	rangeRequest, _ := http.NewRequest(http.MethodGet, issued, nil)
	rangeRequest.Header.Set("Range", "bytes=0-0")
	rangeResponse, err := server.Client().Do(rangeRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = rangeResponse.Body.Close()

	// Then
	if readErr != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "audio/L16;rate=44100;channels=2" || string(body) != "pcm-l16" {
		t.Fatalf("transformed response = %d type=%q body=%q err=%v", response.StatusCode, response.Header.Get("Content-Type"), body, readErr)
	}
	if rangeResponse.StatusCode != http.StatusRequestedRangeNotSatisfiable || rangeResponse.Header.Get("Accept-Ranges") != "" || provider.opens != 1 {
		t.Fatalf("range response = %d headers=%#v provider opens=%d", rangeResponse.StatusCode, rangeResponse.Header, provider.opens)
	}
}
