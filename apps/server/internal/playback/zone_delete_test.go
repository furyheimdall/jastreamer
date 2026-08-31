package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestDeleteZone_removes_populated_zone_atomically_and_preserves_unrelated_state(t *testing.T) {
	store := openTestStore(t, testConfig(t))
	ctx := context.Background()
	for _, zone := range []ZoneDefinition{{ID: "baseline", DisplayName: "Baseline"}, {ID: "owned", DisplayName: "Owned"}} {
		if _, err := store.CreateZone(ctx, zone); err != nil {
			t.Fatalf("create zone: %v", err)
		}
	}
	if _, err := store.UpsertCustomRenderer(ctx, CustomRenderer{ID: "renderer", DisplayName: "Renderer", State: RendererConnected}); err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "owned", RendererID: "renderer", ExpectedRevision: 0}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "owned", ExpectedRevision: 1}); err != nil {
		t.Fatalf("unassign renderer: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "owned", RendererID: "renderer", ExpectedRevision: 2}); err != nil {
		t.Fatalf("reassign renderer: %v", err)
	}
	tracks := make([]QueueTrack, 10_000)
	for index := range tracks {
		tracks[index] = QueueTrack{ID: TrackID("track")}
	}
	queued, err := store.MutateQueue(ctx, QueueMutationRequest{ZoneID: "owned", IdempotencyKey: "populate", ExpectedRevision: 0, Command: QueueAppend, Tracks: tracks})
	if err != nil || len(queued.EntryIDs) != 10_000 {
		t.Fatalf("populate queue = %d entries, %v", len(queued.EntryIDs), err)
	}

	deleted, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "owned", IdempotencyKey: "delete-owned", ExpectedRevision: queued.Revision})
	if err != nil {
		t.Fatalf("delete zone: %v", err)
	}
	if deleted.ZoneID != "owned" || deleted.Revision != 4 || deleted.Replayed {
		t.Fatalf("delete result = %+v", deleted)
	}
	zones, err := store.Zones(ctx)
	if err != nil || len(zones.Zones) != 1 || zones.Zones[0].ID != "baseline" || len(zones.Renderers) != 1 {
		t.Fatalf("remaining inventory = %+v, %v", zones, err)
	}
	for _, table := range []string{"server_zones", "renderer_assignments", "playback_zones", "playback_queue", "playback_sessions", "playback_plays", "playback_decisions", "playback_decision_attempts", "playback_automatic_previews", "playback_start_failures", "renderer_outbox", "playback_continuation_policies", "playback_tombstones", "playback_previous_history"} {
		if count := zoneRowCount(t, store, table, "owned"); count != 0 {
			t.Fatalf("%s retained %d owned rows", table, count)
		}
	}
	replay, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "owned", IdempotencyKey: "delete-owned", ExpectedRevision: queued.Revision})
	if err != nil || !replay.Replayed || replay.Revision != deleted.Revision {
		t.Fatalf("delete replay = %+v, %v", replay, err)
	}
	if _, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "owned", IdempotencyKey: "delete-again", ExpectedRevision: deleted.Revision}); !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("double delete = %v", err)
	}
}

func TestDeleteZone_preconditions_and_last_zone_safety(t *testing.T) {
	store := openTestStore(t, testConfig(t))
	ctx := context.Background()
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "only", DisplayName: "Only"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "only", IdempotencyKey: "stale", ExpectedRevision: 1}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale delete = %v", err)
	}
	if _, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "only", ExpectedRevision: 0}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing key = %v", err)
	}
	if _, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "missing", IdempotencyKey: "missing", ExpectedRevision: 0}); !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("unknown zone = %v", err)
	}
	if _, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "only", IdempotencyKey: "last", ExpectedRevision: 0}); err != nil {
		t.Fatalf("delete last zone: %v", err)
	}
	zones, err := store.Zones(ctx)
	if err != nil || len(zones.Zones) != 0 {
		t.Fatalf("zones after last delete = %+v, %v", zones, err)
	}
}

func TestDeleteZone_storage_failure_rolls_back_and_concurrent_mutation_is_revision_safe(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		store := openTestStore(t, testConfig(t))
		ctx := context.Background()
		if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "owned", DisplayName: "Owned"}); err != nil {
			t.Fatal(err)
		}
		queued, err := store.MutateQueue(ctx, QueueMutationRequest{ZoneID: "owned", IdempotencyKey: "queue", Command: QueueAppend, Tracks: []QueueTrack{{ID: "track"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.db.exec("CREATE TRIGGER fail_zone_delete BEFORE DELETE ON server_zones BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "owned", IdempotencyKey: "delete", ExpectedRevision: queued.Revision}); err == nil {
			t.Fatal("delete unexpectedly succeeded")
		}
		snapshot, err := store.Snapshot(ctx, "owned")
		if err != nil || len(snapshot.Queue) != 1 || snapshot.Revision != queued.Revision {
			t.Fatalf("rollback snapshot = %+v, %v", snapshot, err)
		}
	})

	t.Run("concurrent queue mutation", func(t *testing.T) {
		store := openTestStore(t, testConfig(t))
		ctx := context.Background()
		if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "owned", DisplayName: "Owned"}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errorsOut := make(chan error, 2)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			_, err := store.DeleteZone(ctx, DeleteZoneRequest{ZoneID: "owned", IdempotencyKey: "delete", ExpectedRevision: 0})
			errorsOut <- err
		}()
		go func() {
			defer group.Done()
			<-start
			_, err := store.MutateQueue(ctx, QueueMutationRequest{ZoneID: "owned", IdempotencyKey: "queue", ExpectedRevision: 0, Command: QueueAppend, Tracks: []QueueTrack{{ID: "track"}}})
			errorsOut <- err
		}()
		close(start)
		group.Wait()
		close(errorsOut)
		successes := 0
		for err := range errorsOut {
			if err == nil {
				successes++
				continue
			}
			if !errors.Is(err, ErrRevisionConflict) && !errors.Is(err, ErrZoneNotFound) {
				t.Fatalf("concurrent error = %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("successful concurrent mutations = %d", successes)
		}
	})
}

func zoneRowCount(t *testing.T, store *Store, table string, zoneID ZoneID) int64 {
	t.Helper()
	var count int64
	err := store.read(context.Background(), func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT count(*) FROM " + table + " WHERE zone_id=?")
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err == nil && row {
			count = stmt.int64(0)
		}
		return err
	})
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
