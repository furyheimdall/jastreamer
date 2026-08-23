package playback_test

import (
	"context"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestExplicitEnqueueBeforeAutomaticCommitWins(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-before")
	request := playback.NextRequest{
		ZoneID:   "zone-before",
		Boundary: playback.Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	preview, err := store.PreviewNext(ctx, request)
	if err != nil {
		t.Fatalf("preview next: %v", err)
	}
	if preview.Source != string(decision.SourceSimilar) || preview.TrackID != "generated" {
		t.Fatalf("preview = %+v, want generated similar track", preview)
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "zone-before", IdempotencyKey: "explicit-before", ExpectedRevision: preview.Revision,
		Tracks: []playback.QueueTrack{{ID: "explicit", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue before cutoff: %v", err)
	}

	// When
	committed, err := store.CommitNext(ctx, request)
	// Then
	if err != nil {
		t.Fatalf("commit next: %v", err)
	}
	if committed.Reason != string(decision.ReasonPlayExplicit) || committed.Source != string(decision.SourceExplicit) ||
		committed.TrackID != "explicit" {
		t.Fatalf("committed = %+v, want explicit head", committed)
	}
	commands, err := store.PendingOutbox(ctx, "zone-before")
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(commands) != 1 || commands[0].ID != committed.ID || commands[0].PlayID != committed.PlayID {
		t.Fatalf("outbox = %+v, want one command for committed explicit play", commands)
	}
}
