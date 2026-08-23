package playback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func TestEnqueueBeforeAutomaticCommitCancelsPreview(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-before-preview")
	request := playback.NextRequest{
		ZoneID:   "zone-before-preview",
		Boundary: playback.Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	preview, err := store.PreviewNext(ctx, request)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// When
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: request.ZoneID, IdempotencyKey: "explicit-before", ExpectedRevision: preview.Revision,
		Tracks: []playback.QueueTrack{{ID: "explicit", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue before cutoff: %v", err)
	}
	_, previewErr := store.PreviewNext(ctx, request)
	committed, commitErr := store.CommitNext(ctx, request)

	// Then
	if !errors.Is(previewErr, playback.ErrAutomaticPreempted) || commitErr != nil {
		t.Fatalf("preview error=%v commit error=%v", previewErr, commitErr)
	}
	if committed.Source != string(decision.SourceExplicit) || committed.TrackID != "explicit" {
		t.Fatalf("committed = %+v, want explicit", committed)
	}
}

func TestEnqueueAfterAutomaticCommitStaysBehindCommittedPlay(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	playing := startTodo12Session(t, store, "zone-after-preview")
	request := playback.NextRequest{
		ZoneID:   "zone-after-preview",
		Boundary: playback.Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	committed, err := store.CommitNext(ctx, request)
	if err != nil {
		t.Fatalf("commit automatic: %v", err)
	}

	// When
	enqueue, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: request.ZoneID, IdempotencyKey: "explicit-after", ExpectedRevision: committed.Revision,
		Tracks: []playback.QueueTrack{{ID: "explicit", Available: true}},
	})
	// Then
	if err != nil {
		t.Fatalf("enqueue after cutoff: %v", err)
	}
	snapshot, err := store.Snapshot(ctx, request.ZoneID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if committed.Source != string(decision.SourceSimilar) || snapshot.CurrentPlay != committed.PlayID ||
		len(snapshot.Queue) != 2 || snapshot.Queue[1].TrackID != "explicit" ||
		snapshot.Queue[1].State != playback.QueuePending || enqueue.Revision != committed.Revision+1 {
		t.Fatalf("decision=%+v enqueue=%+v snapshot=%+v", committed, enqueue, snapshot)
	}
}

func TestConcurrentEnqueueAndAutomaticCommitHaveOneSerializedCutoff(t *testing.T) {
	TestConcurrentEnqueueAndAutomaticCommitWinnerStateIsSerialized(t)
}

func TestAutomaticPreviewSurvivesRestartBeforeCommit(t *testing.T) {
	// Given
	ctx := context.Background()
	config := todo12Config(t)
	store := openTodo12Store(t, config)
	playing := startTodo12Session(t, store, "zone-restart-preview")
	request := playback.NextRequest{
		ZoneID:   "zone-restart-preview",
		Boundary: playback.Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	if _, err := store.PreviewNext(ctx, request); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted := openTodo12Store(t, config)

	// When
	committed, err := restarted.CommitNext(ctx, request)

	// Then
	if err != nil || committed.Source != string(decision.SourceSimilar) || committed.TrackID != "generated" {
		t.Fatalf("commit after restart = %+v, %v", committed, err)
	}
}
