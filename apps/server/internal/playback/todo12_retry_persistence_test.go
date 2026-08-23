package playback_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestThreeGeneratedFailuresStopWithExactReasonAcrossRestarts(t *testing.T) {
	// Given
	ctx := context.Background()
	config := todo12Config(t)
	store := openTodo12Store(t, config)
	playing := startTodo12Session(t, store, "zone-limit")
	request := playback.NextRequest{
		ZoneID:   "zone-limit",
		Boundary: playback.Boundary{ID: "automatic-limit", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("one", "two", "three", "four"),
	}
	current, err := store.CommitNext(ctx, request)
	if err != nil {
		t.Fatalf("initial generated decision: %v", err)
	}
	selected := make([]playback.TrackID, 0, decision.MaxGeneratedAttempts)
	var finalFailure playback.StartFailureRequest

	// When
	for failure := range decision.MaxGeneratedAttempts {
		if slices.Contains(selected, current.TrackID) {
			t.Fatalf("failure %d repeated generated track %q", failure+1, current.TrackID)
		}
		selected = append(selected, current.TrackID)
		finalFailure = playback.StartFailureRequest{
			ZoneID: request.ZoneID, BoundaryID: request.Boundary.ID,
			PlayID: current.PlayID, Snapshot: request.Snapshot,
		}
		current, err = store.HandleStartFailure(ctx, finalFailure)
		if err != nil {
			t.Fatalf("failure %d: %v", failure+1, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close after failure %d: %v", failure+1, err)
		}
		store = openTodo12Store(t, config)
	}
	replayed, replayErr := store.HandleStartFailure(ctx, finalFailure)

	// Then
	if replayErr != nil {
		t.Fatalf("replay final failure: %v", replayErr)
	}
	if len(selected) != decision.MaxGeneratedAttempts || current.Kind != playback.DecisionStop ||
		current.Reason != string(decision.ReasonStopAutoFailureLimit) || replayed.ID != current.ID {
		t.Fatalf("selected=%v stop=%+v replay=%+v", selected, current, replayed)
	}
	commands, err := store.PendingOutbox(ctx, "zone-limit")
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("failed generated commands remain dispatchable: %+v", commands)
	}
}

func TestGeneratedRetryPreservesFullExplicitQueueRows(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-preserve")
	request := playback.NextRequest{
		ZoneID:   "zone-preserve",
		Boundary: playback.Boundary{ID: "automatic-preserve", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("one", "two"),
	}
	generated, err := store.CommitNext(ctx, request)
	if err != nil {
		t.Fatalf("commit generated: %v", err)
	}
	before, err := store.Snapshot(ctx, "zone-preserve")
	if err != nil {
		t.Fatalf("snapshot before failure: %v", err)
	}

	// When
	retry, err := store.HandleStartFailure(ctx, playback.StartFailureRequest{
		ZoneID: request.ZoneID, BoundaryID: request.Boundary.ID,
		PlayID: generated.PlayID, Snapshot: request.Snapshot,
	})
	// Then
	if err != nil {
		t.Fatalf("handle generated failure: %v", err)
	}
	after, err := store.Snapshot(ctx, "zone-preserve")
	if err != nil {
		t.Fatalf("snapshot after failure: %v", err)
	}
	if retry.Source != string(decision.SourceSimilar) || !slices.Equal(before.Queue, after.Queue) {
		t.Fatalf("retry=%+v queue before=%+v after=%+v", retry, before.Queue, after.Queue)
	}
}

func TestDuplicateGeneratedBoundaryAfterRestartReturnsSameDecisionAndOutbox(t *testing.T) {
	// Given
	ctx := context.Background()
	config := todo12Config(t)
	store := openTodo12Store(t, config)
	playing := startTodo12Session(t, store, "zone-duplicate")
	request := playback.NextRequest{
		ZoneID:   "zone-duplicate",
		Boundary: playback.Boundary{ID: "automatic-duplicate", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	first, err := store.CommitNext(ctx, request)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted := openTodo12Store(t, config)

	// When
	replayed, err := restarted.CommitNext(ctx, request)
	// Then
	if err != nil {
		t.Fatalf("replay commit: %v", err)
	}
	commands, err := restarted.PendingOutbox(ctx, request.ZoneID)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if replayed.ID != first.ID || replayed.PlayID != first.PlayID || len(commands) != 1 ||
		commands[0].ID != first.ID {
		t.Fatalf("first=%+v replay=%+v outbox=%+v", first, replayed, commands)
	}
}
