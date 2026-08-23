package playback

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	directory := t.TempDir()
	return Config{
		Path:            filepath.Join(directory, "playback.sqlite"),
		MigrationPath:   "../../migrations/002_playback.sql",
		BackupDirectory: filepath.Join(directory, "backups"),
		SupportedSchema: CurrentSchemaVersion,
		JournalMode:     JournalRollback,
	}
}

func openTestStore(t *testing.T, config Config) *Store {
	t.Helper()
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func Test_Queue_preserves_ten_thousand_entries_in_order(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	tracks := make([]QueueTrack, 10_000)
	for index := range tracks {
		tracks[index] = QueueTrack{ID: TrackID(fmt.Sprintf("track-%05d", index)), Available: true}
	}

	// When
	result, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "zone-a", IdempotencyKey: "bulk", ExpectedRevision: 0, Tracks: tracks,
	})
	// Then
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(result.EntryIDs) != 10_000 {
		t.Fatalf("entry count = %d, want 10000", len(result.EntryIDs))
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Queue[9_999].TrackID != "track-09999" {
		t.Fatalf("last track = %q", snapshot.Queue[9_999].TrackID)
	}
}

func Test_Enqueue_is_idempotent_and_rejects_conflicting_reuse(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	request := EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "request-1", Tracks: []QueueTrack{{ID: "a", Available: true}}}

	// When
	first, err := store.Enqueue(context.Background(), request)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := store.Enqueue(context.Background(), request)
	// Then
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if first.Revision != second.Revision || first.EntryIDs[0] != second.EntryIDs[0] {
		t.Fatalf("replay result differs: %#v %#v", first, second)
	}
	revisionConflict := request
	revisionConflict.ExpectedRevision = 99
	_, err = store.Enqueue(context.Background(), revisionConflict)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("revision reuse error = %v", err)
	}
	request.Tracks[0].ID = "b"
	_, err = store.Enqueue(context.Background(), request)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting reuse error = %v", err)
	}
}

func Test_Enqueue_rejects_stale_revision_without_mutation(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	_, err := store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "one", Tracks: []QueueTrack{{ID: "a", Available: true}}})
	if err != nil {
		t.Fatalf("seed enqueue: %v", err)
	}

	// When
	_, err = store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "two", ExpectedRevision: 0, Tracks: []QueueTrack{{ID: "b", Available: true}}})

	// Then
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
	snapshot, snapshotErr := store.Snapshot(context.Background(), "zone-a")
	if snapshotErr != nil {
		t.Fatalf("snapshot: %v", snapshotErr)
	}
	if len(snapshot.Queue) != 1 {
		t.Fatalf("queue mutated after conflict: %d", len(snapshot.Queue))
	}
}

func TestDuplicateEntriesStartOnce(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	_, err := store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "duplicates", Tracks: []QueueTrack{{ID: "same", Available: true}, {ID: "same", Available: true}}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start-1"})
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-a", first.PlayID); err != nil {
		t.Fatalf("confirm first: %v", err)
	}

	// When
	second, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "end-1", PreviousPlayID: first.PlayID})
	// Then
	if err != nil {
		t.Fatalf("reserve second: %v", err)
	}
	if first.QueueEntryID == second.QueueEntryID || second.TrackID != "same" {
		t.Fatalf("duplicate entries not independently ordered: %#v %#v", first, second)
	}
}

func TestUnavailableHeadBlocksWithoutMutation(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	_, err := store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "blocked", Tracks: []QueueTrack{{ID: "missing"}, {ID: "later", Available: true}}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// When
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	// Then
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if decision.Kind != DecisionBlock || decision.Reason != ReasonBlockExplicit {
		t.Fatalf("decision = %#v", decision)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Queue[1].State != QueuePending {
		t.Fatalf("later entry state = %s", snapshot.Queue[1].State)
	}
}

func TestConcurrentIdempotentEnqueueCreatesOneEntry(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	request := EnqueueRequest{
		ZoneID: "zone-concurrent", IdempotencyKey: "same-request",
		Tracks: []QueueTrack{{ID: "track-a", Available: true}},
	}
	const workers = 32
	start := make(chan struct{})
	results := make(chan EnqueueResult, workers)
	errorsChannel := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			<-start
			result, err := store.Enqueue(context.Background(), request)
			results <- result
			errorsChannel <- err
		})
	}

	// When
	close(start)
	group.Wait()
	close(results)
	close(errorsChannel)

	// Then
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent enqueue: %v", err)
		}
	}
	var first *EnqueueResult
	for result := range results {
		if first == nil {
			copy := result
			first = &copy
			continue
		}
		if result.Revision != first.Revision || result.EntryIDs[0] != first.EntryIDs[0] {
			t.Fatalf("idempotent results differ: first=%+v result=%+v", *first, result)
		}
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-concurrent")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Queue) != 1 || snapshot.Revision != 1 {
		t.Fatalf("concurrent queue = %+v", snapshot)
	}
}
