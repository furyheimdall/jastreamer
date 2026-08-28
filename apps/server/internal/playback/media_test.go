package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMediaAuthorization_accepts_only_active_assigned_renderer_play_identity(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "media-zone", DisplayName: "Media"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertCustomRenderer(ctx, CustomRenderer{ID: "renderer", DisplayName: "Renderer", State: RendererConnected, ProtocolMajor: 3, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "media-zone", RendererID: "renderer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enqueue(ctx, EnqueueRequest{ZoneID: "media-zone", IdempotencyKey: "media", Tracks: []QueueTrack{{ID: "track", Available: true}}}); err != nil {
		t.Fatal(err)
	}
	decision, err := store.ReserveNext(ctx, "media-zone", Boundary{ID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	valid := MediaAuthorization{RendererID: "renderer", ZoneID: "media-zone", PlayID: decision.PlayID, TrackID: "track"}

	// When / Then
	if err := store.AuthorizeMedia(ctx, valid); err != nil {
		t.Fatalf("valid authorization: %v", err)
	}
	for name, request := range map[string]MediaAuthorization{
		"renderer": {RendererID: "other", ZoneID: valid.ZoneID, PlayID: valid.PlayID, TrackID: valid.TrackID},
		"zone":     {RendererID: valid.RendererID, ZoneID: "other", PlayID: valid.PlayID, TrackID: valid.TrackID},
		"play":     {RendererID: valid.RendererID, ZoneID: valid.ZoneID, PlayID: "other", TrackID: valid.TrackID},
		"track":    {RendererID: valid.RendererID, ZoneID: valid.ZoneID, PlayID: valid.PlayID, TrackID: "other"},
	} {
		if err := store.AuthorizeMedia(ctx, request); !errors.Is(err, ErrMediaUnauthorized) {
			t.Fatalf("wrong %s error = %v", name, err)
		}
	}
	if err := store.RevokeRenderer(ctx, "renderer"); err != nil {
		t.Fatal(err)
	}
	if err := store.AuthorizeMedia(ctx, valid); !errors.Is(err, ErrMediaUnauthorized) {
		t.Fatalf("revoked error = %v", err)
	}
}

func TestMediaSigningKey_persists_in_server_state_across_restart(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	created := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	key := EncryptedMediaSigningKey{ID: "key-1", Digest: "digest", Ciphertext: []byte("ciphertext"), Nonce: []byte("nonce"), WrappingKeyID: "tls-identity", CreatedAt: created}
	if err := store.InsertMediaSigningKey(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// When
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	loaded, found, err := restarted.ActiveMediaSigningKey(context.Background())

	// Then
	if err != nil || !found {
		t.Fatalf("load = %+v, %t, %v", loaded, found, err)
	}
	if loaded.ID != key.ID || loaded.Digest != key.Digest || string(loaded.Ciphertext) != "ciphertext" || string(loaded.Nonce) != "nonce" || loaded.WrappingKeyID != key.WrappingKeyID || !loaded.CreatedAt.Equal(created) {
		t.Fatalf("loaded = %+v", loaded)
	}
	if err := restarted.InsertMediaSigningKey(context.Background(), key); !errors.Is(err, ErrMediaSigningKeyConflict) {
		t.Fatalf("second active key error = %v", err)
	}
}
