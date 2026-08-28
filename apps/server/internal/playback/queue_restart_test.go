package playback

import (
	"context"
	"testing"
)

func Test_QueueMutation_replay_after_restart_preserves_original_revision_and_ids(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	seed, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "restart-zone", IdempotencyKey: "seed", Tracks: []QueueTrack{{ID: "a", Available: true}, {ID: "b", Available: true}},
	})
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	request := QueueMutationRequest{
		ZoneID: "restart-zone", IdempotencyKey: "remove", ExpectedRevision: seed.Revision,
		Command: QueueRemove, EntryID: seed.EntryIDs[0],
	}
	first, err := store.MutateQueue(context.Background(), request)
	if err != nil {
		t.Fatalf("remove before restart: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })

	// When
	replay, err := restarted.MutateQueue(context.Background(), request)
	// Then
	if err != nil {
		t.Fatalf("replay after restart: %v", err)
	}
	snapshot, err := restarted.Snapshot(context.Background(), "restart-zone")
	if err != nil {
		t.Fatalf("snapshot after restart: %v", err)
	}
	if !replay.Replayed || replay.Revision != first.Revision || snapshot.Revision != first.Revision || len(snapshot.Queue) != 1 {
		t.Fatalf("restart replay changed state: first=%+v replay=%+v snapshot=%+v", first, replay, snapshot)
	}
}
