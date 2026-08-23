package playback_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestConcurrentEnqueueAndAutomaticCommitWinnerStateIsSerialized(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-race-state")
	request := playback.NextRequest{
		ZoneID:   "zone-race-state",
		Boundary: playback.Boundary{ID: "automatic-race", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	preview, err := store.PreviewNext(ctx, request)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	start := make(chan struct{})
	committed := make(chan playback.Decision, 1)
	commitErrors := make(chan error, 1)
	enqueueErrors := make(chan error, 1)
	var group sync.WaitGroup
	group.Go(func() {
		<-start
		result, commitErr := store.CommitNext(ctx, request)
		committed <- result
		commitErrors <- commitErr
	})
	group.Go(func() {
		<-start
		_, enqueueErr := store.Enqueue(ctx, playback.EnqueueRequest{
			ZoneID: request.ZoneID, IdempotencyKey: "explicit-race", ExpectedRevision: preview.Revision,
			Tracks: []playback.QueueTrack{{ID: "explicit", Available: true}},
		})
		enqueueErrors <- enqueueErr
	})

	// When
	close(start)
	group.Wait()
	decisionResult := <-committed
	commitErr := <-commitErrors
	enqueueErr := <-enqueueErrors

	// Then
	if commitErr != nil {
		t.Fatalf("commit failed: %v", commitErr)
	}
	snapshot, err := store.Snapshot(ctx, request.ZoneID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	commands, err := store.PendingOutbox(ctx, request.ZoneID)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	switch {
	case enqueueErr == nil:
		if decisionResult.Source != string(decision.SourceExplicit) || decisionResult.TrackID != "explicit" ||
			len(snapshot.Queue) != 2 || snapshot.Queue[1].State != playback.QueueReserved ||
			len(commands) != 1 || commands[0].ID != decisionResult.ID {
			t.Fatalf("enqueue winner: decision=%+v snapshot=%+v commands=%+v", decisionResult, snapshot, commands)
		}
	case errors.Is(enqueueErr, playback.ErrRevisionConflict):
		if decisionResult.Source != string(decision.SourceSimilar) || decisionResult.TrackID != "generated" ||
			len(snapshot.Queue) != 1 || len(commands) != 1 || commands[0].ID != decisionResult.ID {
			t.Fatalf("commit winner: decision=%+v snapshot=%+v commands=%+v", decisionResult, snapshot, commands)
		}
	default:
		t.Fatalf("enqueue error = %v, want success or revision conflict", enqueueErr)
	}
}
