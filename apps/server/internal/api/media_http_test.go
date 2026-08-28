package api_test

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

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestRendererMedia_serves_signed_HTTPS_and_enforces_renderer_bearer_on_custom_route(t *testing.T) {
	// Given
	value := newFixture(t)
	renderer := pairRole(t, value, security.RoleRenderer, "Renderer")
	other := pairRole(t, value, security.RoleRenderer, "Other renderer")
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	if _, err := value.store.UpsertCustomRenderer(context.Background(), playback.CustomRenderer{
		ID: playback.RendererID(renderer.Device.ID), DisplayName: "Renderer", State: playback.RendererConnected,
		ProtocolMajor: 3, Capabilities: []string{"audio/flac"}, LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.CreateZone(context.Background(), playback.ZoneDefinition{ID: "zone-media", DisplayName: "Media"}); err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.AssignRenderer(context.Background(), playback.AssignmentRequest{ZoneID: "zone-media", RendererID: playback.RendererID(renderer.Device.ID)}); err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.Enqueue(context.Background(), playback.EnqueueRequest{ZoneID: "zone-media", IdempotencyKey: "media-queue", Tracks: []playback.QueueTrack{{ID: "track-media", Available: true}}}); err != nil {
		t.Fatal(err)
	}
	decision, err := value.store.ReserveNext(context.Background(), "zone-media", playback.Boundary{ID: "media-start"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	if err := os.WriteFile(path, []byte("encoded-flac"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	track := catalog.Track{RootID: "root-media", TrackID: "track-media", RelativePath: "track.flac", Format: catalog.FormatFLAC, Fingerprint: strings.Repeat("b", 64), FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()}, Available: true}
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "key", Key: []byte(strings.Repeat("m", 32)), Clock: &apiClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	mediaService, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: value.store,
		Snapshot: func(context.Context) catalog.Snapshot {
			return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{"track-media": track}}
		},
		Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"root-media": root} },
	})
	if err != nil {
		t.Fatal(err)
	}
	grant := media.Grant{
		RendererID: playback.RendererID(renderer.Device.ID), ZoneID: "zone-media", PlayID: decision.PlayID,
		TrackID: "track-media", Representation: media.Original, FileSize: info.Size(), ModifiedNS: info.ModTime().UnixNano(),
	}
	grant.Audience = media.AudienceCustomRenderer
	token, err := signer.Sign(grant)
	if err != nil {
		t.Fatal(err)
	}
	grant.Audience = media.AudienceK17Capability
	k17Token, err := signer.Sign(grant)
	if err != nil {
		t.Fatal(err)
	}
	handler := api.New(api.Config{Security: value.manager, Queue: value.store, Catalog: catalog.EmptySnapshot(), Media: mediaService})
	server := httptest.NewTLSServer(handler)
	defer server.Close()

	// When
	customRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+token, nil)
	customRequest.Header.Set("Authorization", "Bearer "+renderer.Token)
	customResponse, err := server.Client().Do(customRequest)
	if err != nil {
		t.Fatal(err)
	}
	customBody, _ := io.ReadAll(customResponse.Body)
	_ = customResponse.Body.Close()
	headRequest, _ := http.NewRequest(http.MethodHead, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+token, nil)
	headRequest.Header.Set("Authorization", "Bearer "+renderer.Token)
	headResponse, err := server.Client().Do(headRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = headResponse.Body.Close()
	rangeRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+token, nil)
	rangeRequest.Header.Set("Authorization", "Bearer "+renderer.Token)
	rangeRequest.Header.Set("Range", "bytes=0-6")
	rangeResponse, err := server.Client().Do(rangeRequest)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, _ := io.ReadAll(rangeResponse.Body)
	_ = rangeResponse.Body.Close()
	k17Response, err := server.Client().Get(server.URL + "/media/v1/" + token)
	if err != nil {
		t.Fatal(err)
	}
	k17Body, _ := io.ReadAll(k17Response.Body)
	_ = k17Response.Body.Close()
	missingRequest, _ := http.NewRequest(http.MethodHead, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+token, nil)
	missingResponse, err := server.Client().Do(missingRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = missingResponse.Body.Close()
	k17ValidResponse, err := server.Client().Get(server.URL + "/media/v1/" + k17Token)
	if err != nil {
		t.Fatal(err)
	}
	k17ValidBody, _ := io.ReadAll(k17ValidResponse.Body)
	_ = k17ValidResponse.Body.Close()
	k17ReplayRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+k17Token, nil)
	k17ReplayRequest.Header.Set("Authorization", "Bearer "+renderer.Token)
	k17ReplayResponse, err := server.Client().Do(k17ReplayRequest)
	if err != nil {
		t.Fatal(err)
	}
	k17ReplayBody, _ := io.ReadAll(k17ReplayResponse.Body)
	_ = k17ReplayResponse.Body.Close()
	wrongRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+token, nil)
	wrongRequest.Header.Set("Authorization", "Bearer "+other.Token)
	wrongResponse, err := server.Client().Do(wrongRequest)
	if err != nil {
		t.Fatal(err)
	}
	wrongBody, _ := io.ReadAll(wrongResponse.Body)
	_ = wrongResponse.Body.Close()
	if err := value.manager.Revoke(context.Background(), value.admin.Token, renderer.Device.ID); err != nil {
		t.Fatal(err)
	}
	revokedRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/renderers/"+string(renderer.Device.ID)+"/media/"+token, nil)
	revokedRequest.Header.Set("Authorization", "Bearer "+renderer.Token)
	revokedResponse, err := server.Client().Do(revokedRequest)
	if err != nil {
		t.Fatal(err)
	}
	revokedBody, _ := io.ReadAll(revokedResponse.Body)
	_ = revokedResponse.Body.Close()

	// Then
	if customResponse.StatusCode != http.StatusOK || string(customBody) != "encoded-flac" {
		t.Fatalf("custom = %d %q", customResponse.StatusCode, customBody)
	}
	if headResponse.StatusCode != http.StatusOK || headResponse.Header.Get("Content-Length") != "12" {
		t.Fatalf("custom HEAD = %d length=%q", headResponse.StatusCode, headResponse.Header.Get("Content-Length"))
	}
	if rangeResponse.StatusCode != http.StatusPartialContent || string(rangeBody) != "encoded" {
		t.Fatalf("custom range = %d %q", rangeResponse.StatusCode, rangeBody)
	}
	if k17Response.StatusCode != http.StatusUnauthorized || strings.Contains(string(k17Body), "encoded-flac") {
		t.Fatalf("custom token on K17 route = %d %q", k17Response.StatusCode, k17Body)
	}
	if k17ValidResponse.StatusCode != http.StatusOK || string(k17ValidBody) != "encoded-flac" {
		t.Fatalf("K17 capability = %d %q", k17ValidResponse.StatusCode, k17ValidBody)
	}
	if missingResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing renderer bearer = %d", missingResponse.StatusCode)
	}
	if k17ReplayResponse.StatusCode != http.StatusUnauthorized || strings.Contains(string(k17ReplayBody), "encoded-flac") {
		t.Fatalf("K17 token on custom route = %d %q", k17ReplayResponse.StatusCode, k17ReplayBody)
	}
	if wrongResponse.StatusCode != http.StatusForbidden || strings.Contains(string(wrongBody), "encoded-flac") {
		t.Fatalf("wrong renderer = %d %q", wrongResponse.StatusCode, wrongBody)
	}
	if revokedResponse.StatusCode != http.StatusUnauthorized || strings.Contains(string(revokedBody), "encoded-flac") {
		t.Fatalf("revoked renderer = %d %q", revokedResponse.StatusCode, revokedBody)
	}
}
