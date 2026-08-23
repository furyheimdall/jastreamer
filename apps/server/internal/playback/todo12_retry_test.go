package playback_test

import (
	"context"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestExplicitInsertedBeforeGeneratedRetryWins(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-retry")
	request := playback.NextRequest{
		ZoneID:   "zone-retry",
		Boundary: playback.Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("one", "two", "three"),
	}
	generated, err := store.CommitNext(ctx, request)
	if err != nil {
		t.Fatalf("commit generated: %v", err)
	}
	if generated.Source != string(decision.SourceSimilar) {
		t.Fatalf("initial decision = %+v, want generated similar", generated)
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "zone-retry", IdempotencyKey: "explicit-before-retry",
		ExpectedRevision: generated.Revision,
		Tracks:           []playback.QueueTrack{{ID: "explicit", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue explicit: %v", err)
	}
	failure := playback.StartFailureRequest{
		ZoneID: "zone-retry", BoundaryID: request.Boundary.ID,
		PlayID: generated.PlayID, Snapshot: request.Snapshot,
	}

	// When
	retry, err := store.HandleStartFailure(ctx, failure)
	replayed, replayErr := store.HandleStartFailure(ctx, failure)

	// Then
	if err != nil || replayErr != nil {
		t.Fatalf("retry=%v replay=%v", err, replayErr)
	}
	if retry.Reason != string(decision.ReasonPlayExplicit) || retry.Source != string(decision.SourceExplicit) ||
		retry.TrackID != "explicit" || replayed.ID != retry.ID {
		t.Fatalf("retry=%+v replay=%+v, want idempotent explicit decision", retry, replayed)
	}
	commands, err := store.PendingOutbox(ctx, "zone-retry")
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(commands) != 1 || commands[0].ID != retry.ID {
		t.Fatalf("remaining generated retries = %+v, want only explicit command", commands)
	}
}
