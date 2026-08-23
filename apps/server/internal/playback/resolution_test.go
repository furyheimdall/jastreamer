package playback

import (
	"context"
	"testing"
)

func Test_BlockedHead_requires_explicit_retry_or_skip(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	_, err := store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "blocked", Tracks: []QueueTrack{{ID: "missing"}, {ID: "later", Available: true}}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"}); err != nil {
		t.Fatalf("block: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// When
	if err := store.SkipBlocked(context.Background(), "zone-a", before.Revision); err != nil {
		t.Fatalf("skip: %v", err)
	}
	after, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot after skip: %v", err)
	}
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "after-skip"})
	// Then
	if err != nil {
		t.Fatalf("reserve after skip: %v", err)
	}
	if len(after.Queue) != 1 || after.Queue[0].TrackID != "later" || after.Queue[0].State != QueuePending {
		t.Fatalf("queue after explicit skip = %#v", after.Queue)
	}
	current, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("current snapshot: %v", err)
	}
	if decision.TrackID != "later" || current.SessionID != before.SessionID {
		t.Fatalf("skip reset session or missed later entry: decision=%#v session=%q want=%q", decision, current.SessionID, before.SessionID)
	}
}

func Test_Enqueue_during_session_preserves_identity_and_seed(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-a", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// When
	_, err = store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "during-session", ExpectedRevision: before.Revision, Tracks: []QueueTrack{{ID: "c", Available: true}}})
	if err != nil {
		t.Fatalf("enqueue during session: %v", err)
	}
	after, err := store.Snapshot(context.Background(), "zone-a")
	// Then
	if err != nil {
		t.Fatalf("snapshot after enqueue: %v", err)
	}
	if after.SessionID != before.SessionID || after.SessionSeed != before.SessionSeed {
		t.Fatalf("enqueue reset session: before=%#v after=%#v", before, after)
	}
}

func Test_MidTrackFailure_suspends_without_advancing_queue(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-a", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// When
	if err := store.MidTrackFailure(context.Background(), "zone-a"); err != nil {
		t.Fatalf("failure: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-a")
	// Then
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Transport != TransportSuspended || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("mid-track failure advanced: %#v", snapshot)
	}
}

func TestQueuedTrackBecomesBlocked_when_catalogMarksItUnavailable(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueue, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "zone-catalog", IdempotencyKey: "enqueue-1",
		Tracks: []QueueTrack{{ID: "removed-track", Available: true}, {ID: "later", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// When
	revision, err := store.UpdateAvailability(context.Background(), AvailabilityRequest{
		ZoneID: "zone-catalog", TrackID: "removed-track",
		Available: false, ExpectedRevision: enqueue.Revision,
	})
	if err != nil {
		t.Fatalf("update availability: %v", err)
	}
	decision, err := store.ReserveNext(context.Background(), "zone-catalog", Boundary{ID: "boundary-1"})
	// Then
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if decision.Kind != DecisionBlock || decision.Reason != ReasonBlockExplicit {
		t.Fatalf("decision = %+v, want blocked explicit", decision)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-catalog")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if revision != enqueue.Revision+1 || len(snapshot.Queue) != 2 ||
		snapshot.Queue[0].State != QueueBlocked || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("availability update mutated later queue: %+v", snapshot)
	}
}
