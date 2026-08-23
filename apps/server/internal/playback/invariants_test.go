package playback

import (
	"context"
	"testing"
)

func TestPausedBoundaryDoesNotAdvance(t *testing.T) {
	// When
	_, err := Transition(TransportPaused, EventBoundary)

	// Then
	if err == nil {
		t.Fatal("paused boundary advanced, want invalid transition")
	}
}

func TestEmptyQueueDoesNotStartSession(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))

	// When
	decision, err := store.ReserveNext(context.Background(), "zone-empty", Boundary{ID: "boundary-empty"})
	// Then
	if err != nil {
		t.Fatalf("reserve empty: %v", err)
	}
	if decision.Kind != DecisionStop || decision.Reason != ReasonQueueEmpty {
		t.Fatalf("decision = %+v, want queue-empty stop", decision)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-empty")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.SessionID != "" || snapshot.SessionSeed != "" || snapshot.Transport != TransportIdle {
		t.Fatalf("empty queue started session: %+v", snapshot)
	}
}

func TestDuplicateBoundaryConflictDoesNotMutate(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-boundary", "track-a")
	first, err := store.ReserveNext(context.Background(), "zone-boundary", Boundary{ID: "boundary-1"})
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-boundary")
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}

	// When
	_, err = store.ReserveNext(context.Background(), "zone-boundary", Boundary{
		ID: "boundary-1", PreviousPlayID: "different-play",
	})

	// Then
	if err == nil {
		t.Fatal("conflicting boundary replay succeeded")
	}
	after, snapshotErr := store.Snapshot(context.Background(), "zone-boundary")
	if snapshotErr != nil {
		t.Fatalf("after snapshot: %v", snapshotErr)
	}
	if after.Revision != before.Revision || after.CurrentPlay != first.PlayID {
		t.Fatalf("conflicting replay mutated zone: before=%+v after=%+v", before, after)
	}
}

func TestRepeatedUnknownReconciliationIsIdempotent(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-unknown", "track-a")
	if _, err := store.ReserveNext(context.Background(), "zone-unknown", Boundary{ID: "boundary-1"}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.Reconcile(context.Background(), "zone-unknown", RendererObservation{}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-unknown")
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}

	// When
	if _, err := store.Reconcile(context.Background(), "zone-unknown", RendererObservation{}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	// Then
	after, err := store.Snapshot(context.Background(), "zone-unknown")
	if err != nil {
		t.Fatalf("after snapshot: %v", err)
	}
	if after.Revision != before.Revision || after.Transport != TransportSuspended {
		t.Fatalf("repeated unknown mutated state: before=%+v after=%+v", before, after)
	}
}

func TestWrongRendererPlayIsRejectedWithoutMutation(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-wrong", "track-a")
	if _, err := store.ReserveNext(context.Background(), "zone-wrong", Boundary{ID: "boundary-1"}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-wrong")
	if err != nil {
		t.Fatalf("before snapshot: %v", err)
	}

	// When
	_, err = store.Reconcile(context.Background(), "zone-wrong", RendererObservation{
		OutcomeKnown: true, Playing: true, PlayID: "wrong-play",
	})

	// Then
	if err == nil {
		t.Fatal("wrong renderer play was accepted")
	}
	after, snapshotErr := store.Snapshot(context.Background(), "zone-wrong")
	if snapshotErr != nil {
		t.Fatalf("after snapshot: %v", snapshotErr)
	}
	if after.Revision != before.Revision || after.Transport != before.Transport {
		t.Fatalf("wrong observation mutated state: before=%+v after=%+v", before, after)
	}
}

func TestRetryBlockedReturnsToSelection(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueue, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "zone-retry", IdempotencyKey: "enqueue-1",
		Tracks: []QueueTrack{{ID: "missing", Available: false}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := store.ReserveNext(context.Background(), "zone-retry", Boundary{ID: "boundary-1"}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	blocked, err := store.Snapshot(context.Background(), "zone-retry")
	if err != nil {
		t.Fatalf("blocked snapshot: %v", err)
	}

	// When
	err = store.RetryBlocked(context.Background(), "zone-retry", blocked.Revision)
	// Then
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	after, err := store.Snapshot(context.Background(), "zone-retry")
	if err != nil {
		t.Fatalf("after snapshot: %v", err)
	}
	if after.Transport != TransportSelecting || after.Revision != enqueue.Revision+2 {
		t.Fatalf("retry snapshot = %+v, want selecting", after)
	}
}

func TestReserveRequiresConfirmedBoundaryState(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-state", "track-a")
	first, err := store.ReserveNext(context.Background(), "zone-state", Boundary{ID: "boundary-1"})
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	starting, err := store.Snapshot(context.Background(), "zone-state")
	if err != nil {
		t.Fatalf("starting snapshot: %v", err)
	}

	// When
	_, startingErr := store.ReserveNext(context.Background(), "zone-state", Boundary{ID: "boundary-2"})

	// Then
	if startingErr == nil {
		t.Fatal("new boundary reserved while start confirmation was pending")
	}
	afterStarting, err := store.Snapshot(context.Background(), "zone-state")
	if err != nil {
		t.Fatalf("after starting snapshot: %v", err)
	}
	if afterStarting.Revision != starting.Revision || afterStarting.CurrentPlay != first.PlayID {
		t.Fatalf("pending start mutated: before=%+v after=%+v", starting, afterStarting)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-state", first.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	playing, err := store.Snapshot(context.Background(), "zone-state")
	if err != nil {
		t.Fatalf("playing snapshot: %v", err)
	}

	// When
	_, missingPreviousErr := store.ReserveNext(context.Background(), "zone-state", Boundary{ID: "boundary-3"})

	// Then
	if missingPreviousErr == nil {
		t.Fatal("playing boundary without previous play was accepted")
	}
	afterPlaying, err := store.Snapshot(context.Background(), "zone-state")
	if err != nil {
		t.Fatalf("after playing snapshot: %v", err)
	}
	if afterPlaying.Revision != playing.Revision || afterPlaying.Transport != TransportPlaying {
		t.Fatalf("invalid playing boundary mutated: before=%+v after=%+v", playing, afterPlaying)
	}
}

func enqueueAvailable(t *testing.T, store *Store, zone ZoneID, track TrackID) {
	t.Helper()
	if _, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: zone, IdempotencyKey: "enqueue-1", Tracks: []QueueTrack{{ID: track, Available: true}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}
