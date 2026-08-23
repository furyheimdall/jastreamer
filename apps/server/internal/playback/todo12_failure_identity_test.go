package playback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestStartFailureReplayRejectsWrongZoneAndBoundaryWithoutMutation(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-failure-identity")
	next := playback.NextRequest{
		ZoneID:   "zone-failure-identity",
		Boundary: playback.Boundary{ID: "boundary-failure-identity", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("one", "two"),
	}
	generated, err := store.CommitNext(ctx, next)
	if err != nil {
		t.Fatalf("commit generated: %v", err)
	}
	valid := playback.StartFailureRequest{
		ZoneID: next.ZoneID, BoundaryID: next.Boundary.ID,
		PlayID: generated.PlayID, Snapshot: next.Snapshot,
	}
	if _, err := store.HandleStartFailure(ctx, valid); err != nil {
		t.Fatalf("record valid failure: %v", err)
	}
	before, err := store.Snapshot(ctx, next.ZoneID)
	if err != nil {
		t.Fatalf("snapshot before invalid replay: %v", err)
	}
	beforeCommands, err := store.PendingOutbox(ctx, next.ZoneID)
	if err != nil {
		t.Fatalf("outbox before invalid replay: %v", err)
	}
	tests := []struct {
		name    string
		request playback.StartFailureRequest
	}{
		{
			name: "wrong zone",
			request: playback.StartFailureRequest{
				ZoneID: "other-zone", BoundaryID: valid.BoundaryID,
				PlayID: valid.PlayID, Snapshot: valid.Snapshot,
			},
		},
		{
			name: "wrong boundary",
			request: playback.StartFailureRequest{
				ZoneID: valid.ZoneID, BoundaryID: "other-boundary",
				PlayID: valid.PlayID, Snapshot: valid.Snapshot,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, replayErr := store.HandleStartFailure(ctx, test.request)

			// Then
			if !errors.Is(replayErr, playback.ErrStartFailure) {
				t.Fatalf("replay error = %v, want start failure mismatch", replayErr)
			}
		})
	}
	after, err := store.Snapshot(ctx, next.ZoneID)
	if err != nil {
		t.Fatalf("snapshot after invalid replay: %v", err)
	}
	afterCommands, err := store.PendingOutbox(ctx, next.ZoneID)
	if err != nil {
		t.Fatalf("outbox after invalid replay: %v", err)
	}
	if after.Revision != before.Revision || len(afterCommands) != len(beforeCommands) {
		t.Fatalf("invalid replay mutated state: before=%+v/%+v after=%+v/%+v", before, beforeCommands, after, afterCommands)
	}
}
