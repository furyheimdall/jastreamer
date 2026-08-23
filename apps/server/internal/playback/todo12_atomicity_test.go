package playback

import (
	"context"
	"errors"
	"testing"
)

func TestDecisionAndOutboxRollbackTogetherAtCommitFailpoint(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-rollback", "explicit")
	before, err := store.Snapshot(ctx, "zone-rollback")
	if err != nil {
		t.Fatalf("snapshot before commit: %v", err)
	}
	injected := errors.New("injected after decision")
	store.commitHook = func(stage commitStage) error {
		if stage == commitStageAfterDecision {
			return injected
		}
		return nil
	}

	// When
	_, commitErr := store.CommitNext(ctx, NextRequest{
		ZoneID: "zone-rollback", Boundary: Boundary{ID: "boundary-rollback"},
	})
	store.commitHook = nil

	// Then
	if !errors.Is(commitErr, injected) {
		t.Fatalf("commit error = %v, want injected failure", commitErr)
	}
	after, err := store.Snapshot(ctx, "zone-rollback")
	if err != nil {
		t.Fatalf("snapshot after rollback: %v", err)
	}
	if after.Revision != before.Revision || len(after.Queue) != 1 || after.Queue[0] != before.Queue[0] {
		t.Fatalf("rollback changed queue: before=%+v after=%+v", before, after)
	}
	key := boundaryKey{zoneID: "zone-rollback", boundaryID: "boundary-rollback"}
	decisions, outbox, plays := todo12TransactionCounts(t, store, key)
	if decisions != 0 || outbox != 0 || plays != 0 {
		t.Fatalf("rolled-back rows decisions=%d outbox=%d plays=%d", decisions, outbox, plays)
	}

	committed, err := store.CommitNext(ctx, NextRequest{
		ZoneID: "zone-rollback", Boundary: Boundary{ID: "boundary-rollback"},
	})
	if err != nil {
		t.Fatalf("retry commit: %v", err)
	}
	decisions, outbox, plays = todo12TransactionCounts(t, store, key)
	if decisions != 1 || outbox != 1 || plays != 1 || committed.PlayID == "" {
		t.Fatalf("committed=%+v rows decisions=%d outbox=%d plays=%d", committed, decisions, outbox, plays)
	}
}

func todo12TransactionCounts(
	t *testing.T,
	store *Store,
	key boundaryKey,
) (int64, int64, int64) {
	t.Helper()
	counts := [3]int64{}
	err := store.read(context.Background(), func(db *sqliteDB) error {
		queries := []string{
			"SELECT count(*) FROM playback_decision_attempts WHERE zone_id=? AND boundary_id=?",
			"SELECT count(*) FROM renderer_outbox WHERE zone_id=?",
			"SELECT count(*) FROM playback_plays WHERE zone_id=? AND boundary_id=?",
		}
		for index, query := range queries {
			stmt, err := db.prepare(query)
			if err != nil {
				return err
			}
			if err := stmt.bindText(1, string(key.zoneID)); err != nil {
				stmt.close()
				return err
			}
			if index != 1 {
				if err := stmt.bindText(2, string(key.boundaryID)); err != nil {
					stmt.close()
					return err
				}
			}
			row, err := stmt.step()
			if err != nil || !row {
				stmt.close()
				return err
			}
			counts[index] = stmt.int64(0)
			stmt.close()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count transaction rows: %v", err)
	}
	return counts[0], counts[1], counts[2]
}
