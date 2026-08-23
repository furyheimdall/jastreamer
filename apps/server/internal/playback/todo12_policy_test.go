package playback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/curation/ranking"
	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestContinuationPolicySurvivesRestart(t *testing.T) {
	// Given
	ctx := context.Background()
	config := todo12Config(t)
	store := openTodo12Store(t, config)
	updated, err := store.UpdateContinuationPolicy(ctx, playback.PolicyUpdate{
		ZoneID: "zone-policy", Mode: decision.PolicySimilar,
		ArtistGap: 3, AlbumGap: 8,
	})
	if err != nil {
		t.Fatalf("update policy: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted := openTodo12Store(t, config)

	// When
	persisted, err := restarted.ContinuationPolicy(ctx, "zone-policy")
	// Then
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if persisted != updated || persisted.Mode != decision.PolicySimilar || persisted.Revision != 1 {
		t.Fatalf("persisted policy = %+v, want %+v", persisted, updated)
	}
}

func TestContinuationPolicyRejectsOffWithoutMutation(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	before, err := store.ContinuationPolicy(ctx, "zone-policy-off")
	if err != nil {
		t.Fatalf("load default policy: %v", err)
	}

	// When
	_, updateErr := store.UpdateContinuationPolicy(ctx, playback.PolicyUpdate{
		ZoneID: "zone-policy-off", ExpectedRevision: before.Revision,
		Mode: decision.Policy("off"), ArtistGap: ranking.DefaultArtistGap, AlbumGap: ranking.DefaultAlbumGap,
	})

	// Then
	if !errors.Is(updateErr, playback.ErrInvalidPolicy) {
		t.Fatalf("update error = %v, want invalid policy", updateErr)
	}
	after, err := store.ContinuationPolicy(ctx, "zone-policy-off")
	if err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if after != before {
		t.Fatalf("invalid off mutated policy: before=%+v after=%+v", before, after)
	}
}
