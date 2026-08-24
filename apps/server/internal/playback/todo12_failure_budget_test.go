package playback_test

import (
	"context"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestExplicitStartFailureDoesNotConsumeGeneratedFailureBudget(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-budget")
	next := playback.NextRequest{
		ZoneID:   "zone-budget",
		Boundary: playback.Boundary{ID: "boundary-budget", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("one", "two", "three", "four"),
	}
	first, err := store.CommitNext(ctx, next)
	if err != nil {
		t.Fatalf("first generated decision: %v", err)
	}
	second, err := store.HandleStartFailure(ctx, playback.StartFailureRequest{
		ZoneID: next.ZoneID, BoundaryID: next.Boundary.ID, PlayID: first.PlayID, Snapshot: next.Snapshot,
	})
	if err != nil {
		t.Fatalf("first generated failure: %v", err)
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: next.ZoneID, IdempotencyKey: "explicit-budget", ExpectedRevision: second.Revision,
		Tracks: []playback.QueueTrack{{ID: "explicit", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue explicit before second failure: %v", err)
	}
	explicit, err := store.HandleStartFailure(ctx, playback.StartFailureRequest{
		ZoneID: next.ZoneID, BoundaryID: next.Boundary.ID, PlayID: second.PlayID, Snapshot: next.Snapshot,
	})
	if err != nil {
		t.Fatalf("second generated failure: %v", err)
	}
	blocked, err := store.HandleStartFailure(ctx, playback.StartFailureRequest{
		ZoneID: next.ZoneID, BoundaryID: next.Boundary.ID, PlayID: explicit.PlayID, Snapshot: next.Snapshot,
	})
	if err != nil {
		t.Fatalf("explicit start failure: %v", err)
	}
	if blocked.Kind != playback.DecisionBlock || blocked.Reason != string(decision.ReasonBlockExplicit) {
		t.Fatalf("explicit failure = %+v, want block", blocked)
	}
	if err := store.SkipBlocked(ctx, next.ZoneID, blocked.Revision); err != nil {
		t.Fatalf("resolve blocked explicit: %v", err)
	}

	// When
	third, err := store.CommitNext(ctx, next)
	if err != nil {
		t.Fatalf("resume generated selection: %v", err)
	}
	limit, err := store.HandleStartFailure(ctx, playback.StartFailureRequest{
		ZoneID: next.ZoneID, BoundaryID: next.Boundary.ID, PlayID: third.PlayID, Snapshot: next.Snapshot,
	})
	// Then
	if err != nil {
		t.Fatalf("third generated failure: %v", err)
	}
	if third.Kind != playback.DecisionPlay || third.Source != string(decision.SourceSimilar) ||
		third.TrackID == first.TrackID || third.TrackID == second.TrackID {
		t.Fatalf("third generated attempt = %+v after %+v/%+v", third, first, second)
	}
	if limit.Kind != playback.DecisionStop || limit.Reason != string(decision.ReasonStopAutoFailureLimit) {
		t.Fatalf("failure limit = %+v", limit)
	}
}
