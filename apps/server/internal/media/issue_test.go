package media_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
)

func TestService_Issue_selects_original_and_returns_opaque_identity_bound_URL(t *testing.T) {
	t.Parallel()
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "issue", Key: []byte(strings.Repeat("i", 32)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	track := catalog.Track{RootID: "root", TrackID: "track", RelativePath: "private/album.flac", Format: catalog.FormatFLAC, FileVersion: catalog.FileVersion{Size: 123, Modified: time.Unix(0, 456)}, Available: true}
	service, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: authorizer{},
		Snapshot: func(context.Context) catalog.Snapshot {
			return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{"track": track}}
		},
		Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"root": t.TempDir()} },
	})
	if err != nil {
		t.Fatal(err)
	}

	// When
	issued, err := service.Issue(context.Background(), media.IssueRequest{
		Audience: media.AudienceK17Capability, BaseURL: "https://server.example", RendererID: "renderer", ZoneID: "zone", PlayID: "play",
		TrackID: "track", Capabilities: []string{"http-get:*:audio/L16:*", "http-get:*:audio/flac:*"},
	})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(issued)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.RawQuery != "" || strings.Contains(issued, track.RelativePath) || strings.Contains(issued, "bearer") {
		t.Fatalf("issued URL leaks transport detail: %q", issued)
	}
	token := strings.TrimPrefix(parsed.Path, "/media/v1/")
	claims, err := signer.Verify(token, media.AudienceK17Capability, "renderer")
	if err != nil {
		t.Fatal(err)
	}
	if claims.ZoneID != "zone" || claims.PlayID != "play" || claims.TrackID != "track" || claims.Representation != media.Original || claims.FileSize != 123 || claims.ModifiedNS != 456 || claims.ExpiresAt != now.Add(10*time.Minute).Unix() {
		t.Fatalf("claims = %+v", claims)
	}
}
