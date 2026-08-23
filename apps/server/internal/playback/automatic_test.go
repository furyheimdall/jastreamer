package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestEnqueueBeforeAutomaticCommitCancelsPreview(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	playing := startPlaying(t, store, "zone-before")
	preview, err := store.PreviewAutomatic(context.Background(), AutomaticPreviewRequest{
		ZoneID: "zone-before", Boundary: Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		TrackID: "generated", ExpectedRevision: playing.Revision,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// When
	enqueue, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "zone-before", IdempotencyKey: "explicit-before", ExpectedRevision: preview,
		Tracks: []QueueTrack{{ID: "explicit", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue before cutoff: %v", err)
	}
	_, commitErr := store.CommitAutomatic(context.Background(), "zone-before", "automatic-1", enqueue.Revision)

	// Then
	if !errors.Is(commitErr, ErrAutomaticPreempted) {
		t.Fatalf("commit error = %v, want automatic preempted", commitErr)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-before")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Transport != TransportPlaying || snapshot.CurrentPlay != playing.CurrentPlay ||
		len(snapshot.Queue) != 2 || snapshot.Queue[0].State != QueuePlaying ||
		snapshot.Queue[1].TrackID != "explicit" || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("before-cutoff enqueue state = %+v", snapshot)
	}
}

func TestEnqueueAfterAutomaticCommitStaysBehindCommittedPlay(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	playing := startPlaying(t, store, "zone-after")
	preview, err := store.PreviewAutomatic(context.Background(), AutomaticPreviewRequest{
		ZoneID: "zone-after", Boundary: Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		TrackID: "generated", ExpectedRevision: playing.Revision,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	automatic, err := store.CommitAutomatic(context.Background(), "zone-after", "automatic-1", preview)
	if err != nil {
		t.Fatalf("commit automatic: %v", err)
	}

	// When
	enqueue, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "zone-after", IdempotencyKey: "explicit-after", ExpectedRevision: automatic.Revision,
		Tracks: []QueueTrack{{ID: "explicit", Available: true}},
	})
	// Then
	if err != nil {
		t.Fatalf("enqueue after cutoff: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-after")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-after")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if automatic.Reason != ReasonPlayAutomatic || automatic.TrackID != "generated" ||
		snapshot.CurrentPlay != automatic.PlayID || len(snapshot.Queue) != 2 ||
		snapshot.Queue[0].State != QueueCompleted || snapshot.Queue[1].TrackID != "explicit" ||
		snapshot.Queue[1].State != QueuePending || enqueue.Revision != automatic.Revision+1 ||
		len(commands) != 1 || commands[0].PlayID != automatic.PlayID {
		t.Fatalf("after-cutoff state/decision/outbox = %+v / %+v / %+v", snapshot, automatic, commands)
	}
}

func TestConcurrentEnqueueAndAutomaticCommitHaveOneSerializedCutoff(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	playing := startPlaying(t, store, "zone-race")
	preview, err := store.PreviewAutomatic(context.Background(), AutomaticPreviewRequest{
		ZoneID: "zone-race", Boundary: Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		TrackID: "generated", ExpectedRevision: playing.Revision,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Go(func() {
		<-start
		_, commitErr := store.CommitAutomatic(context.Background(), "zone-race", "automatic-1", preview)
		results <- commitErr
	})
	group.Go(func() {
		<-start
		_, enqueueErr := store.Enqueue(context.Background(), EnqueueRequest{
			ZoneID: "zone-race", IdempotencyKey: "explicit-race", ExpectedRevision: preview,
			Tracks: []QueueTrack{{ID: "explicit", Available: true}},
		})
		results <- enqueueErr
	})

	// When
	close(start)
	group.Wait()
	close(results)

	// Then
	successes := 0
	for resultErr := range results {
		if resultErr == nil {
			successes++
		} else if !errors.Is(resultErr, ErrRevisionConflict) && !errors.Is(resultErr, ErrAutomaticPreempted) {
			t.Fatalf("unexpected cutoff error: %v", resultErr)
		}
	}
	if successes != 1 {
		t.Fatalf("cutoff successes = %d, want exactly one", successes)
	}
}

func TestAutomaticPreviewSurvivesRestartBeforeCommit(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	playing := startPlaying(t, store, "zone-restart-preview")
	preview, err := store.PreviewAutomatic(context.Background(), AutomaticPreviewRequest{
		ZoneID: "zone-restart-preview", Boundary: Boundary{ID: "automatic-1", PreviousPlayID: playing.CurrentPlay},
		TrackID: "generated", ExpectedRevision: playing.Revision,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before commit: %v", err)
	}
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted: %v", err)
		}
	})

	// When
	decision, err := restarted.CommitAutomatic(context.Background(), "zone-restart-preview", "automatic-1", preview)

	// Then
	if err != nil || decision.TrackID != "generated" || decision.Reason != ReasonPlayAutomatic {
		t.Fatalf("commit after restart = %+v, %v", decision, err)
	}
}

func startPlaying(t *testing.T, store *Store, zone ZoneID) ZoneSnapshot {
	t.Helper()
	enqueueAvailable(t, store, zone, "seed")
	decision, err := store.ReserveNext(context.Background(), zone, Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve seed: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), zone, decision.PlayID); err != nil {
		t.Fatalf("confirm seed: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background(), zone)
	if err != nil {
		t.Fatalf("playing snapshot: %v", err)
	}
	return snapshot
}
