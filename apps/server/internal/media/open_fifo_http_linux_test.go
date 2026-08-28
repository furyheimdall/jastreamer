package media_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
)

func TestHandler_rejects_FIFO_catalog_path_over_real_HTTP(t *testing.T) {
	// Given
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	if err := os.WriteFile(path, []byte("catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "fifo", Key: []byte(strings.Repeat("f", 32)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	track := catalog.Track{RootID: "root", TrackID: "track", RelativePath: "track.flac", Format: catalog.FormatFLAC, Fingerprint: strings.Repeat("a", 64), FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()}, Available: true}
	service, err := media.NewService(media.ServiceConfig{Signer: signer, Authorizer: authorizer{}, Snapshot: func(context.Context) catalog.Snapshot {
		return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{"track": track}}
	}, Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"root": root} }})
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign(media.Grant{Audience: media.AudienceK17Capability, RendererID: "renderer", ZoneID: "zone", PlayID: "play", TrackID: "track", Representation: media.Original, FileSize: info.Size(), ModifiedNS: info.ModTime().UnixNano()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(media.MediaOnlyHandler(service.K17Handler()))
	defer server.Close()

	// When
	response, err := server.Client().Get(server.URL + "/media/v1/" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if response.StatusCode != http.StatusConflict || string(body) != "MEDIA_STALE\n" {
		t.Fatalf("response = %d %q; want %d MEDIA_STALE", response.StatusCode, body, http.StatusConflict)
	}
}
