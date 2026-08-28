package playback

import (
	"context"
	"errors"
	"testing"
)

func Test_QueueMutation_move_preserves_stable_ids_and_replays_without_revision_change(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	seed, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "move-zone", IdempotencyKey: "seed", Tracks: []QueueTrack{
			{ID: "a", Available: true}, {ID: "b", Available: true}, {ID: "c", Available: true},
		},
	})
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	request := QueueMutationRequest{
		ZoneID: "move-zone", IdempotencyKey: "move-b", ExpectedRevision: seed.Revision,
		Command: QueueMove, EntryID: seed.EntryIDs[1], BeforeEntryID: seed.EntryIDs[0],
	}

	// When
	first, err := store.MutateQueue(context.Background(), request)
	if err != nil {
		t.Fatalf("move queue: %v", err)
	}
	replay, err := store.MutateQueue(context.Background(), request)
	if err != nil {
		t.Fatalf("replay move: %v", err)
	}

	// Then
	snapshot, err := store.Snapshot(context.Background(), "move-zone")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !replay.Replayed || replay.Revision != first.Revision || snapshot.Revision != first.Revision {
		t.Fatalf("replay mutated revision: first=%+v replay=%+v snapshot=%+v", first, replay, snapshot)
	}
	want := []QueueEntryID{seed.EntryIDs[1], seed.EntryIDs[0], seed.EntryIDs[2]}
	for index, entry := range snapshot.Queue {
		if entry.ID != want[index] || entry.Position != int64(index+1) {
			t.Fatalf("entry %d = %+v, want id=%s position=%d", index, entry, want[index], index+1)
		}
	}
}

func Test_QueueMutation_active_entry_failure_is_typed_and_has_zero_mutation(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve active entry: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}

	// When
	_, err = store.MutateQueue(context.Background(), QueueMutationRequest{
		ZoneID: "zone-a", IdempotencyKey: "remove-active", ExpectedRevision: before.Revision,
		Command: QueueRemove, EntryID: decision.QueueEntryID,
	})

	// Then
	if !errors.Is(err, ErrQueueEntryActive) {
		t.Fatalf("remove active error = %v", err)
	}
	after, snapshotErr := store.Snapshot(context.Background(), "zone-a")
	if snapshotErr != nil {
		t.Fatalf("snapshot after: %v", snapshotErr)
	}
	if after.Revision != before.Revision || len(after.Queue) != len(before.Queue) {
		t.Fatalf("active failure mutated queue: before=%+v after=%+v", before, after)
	}
}

func Test_QueueMutation_conflicting_idempotency_key_has_zero_mutation(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	seed, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "clear-zone", IdempotencyKey: "seed", Tracks: []QueueTrack{{ID: "a", Available: true}, {ID: "b", Available: true}},
	})
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	first := QueueMutationRequest{ZoneID: "clear-zone", IdempotencyKey: "mutation", ExpectedRevision: seed.Revision, Command: QueueRemove, EntryID: seed.EntryIDs[0]}
	if _, err := store.MutateQueue(context.Background(), first); err != nil {
		t.Fatalf("first mutation: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "clear-zone")
	if err != nil {
		t.Fatalf("snapshot before conflict: %v", err)
	}

	// When
	first.EntryID = seed.EntryIDs[1]
	_, err = store.MutateQueue(context.Background(), first)

	// Then
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting key error = %v", err)
	}
	after, snapshotErr := store.Snapshot(context.Background(), "clear-zone")
	if snapshotErr != nil {
		t.Fatalf("snapshot after conflict: %v", snapshotErr)
	}
	if after.Revision != before.Revision || len(after.Queue) != len(before.Queue) || after.Queue[0].ID != before.Queue[0].ID {
		t.Fatalf("conflicting replay mutated queue: before=%+v after=%+v", before, after)
	}
}
