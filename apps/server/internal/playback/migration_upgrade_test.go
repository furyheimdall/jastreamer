package playback

import (
	"context"
	"os"
	"testing"
)

func TestMigrationExpandsVersionTwoWithoutRewritingQueue(t *testing.T) {
	// Given
	config := testConfig(t)
	db, err := openSQLite(config.Path)
	if err != nil {
		t.Fatalf("open version two database: %v", err)
	}
	base, err := os.ReadFile(config.MigrationPath)
	if err != nil {
		t.Fatalf("read version two migration: %v", err)
	}
	if err := db.exec(string(base)); err != nil {
		t.Fatalf("apply version two migration: %v", err)
	}
	if err := db.exec(`
		INSERT INTO playback_zones(zone_id,revision,queue_sequence) VALUES ('zone-upgrade',1,1);
		INSERT INTO playback_queue(
			entry_id,zone_id,position,track_id,available,state,created_revision
		) VALUES ('zone-upgrade:q:1','zone-upgrade',1,'preserved',1,'pending',1);
	`); err != nil {
		t.Fatalf("seed version two queue: %v", err)
	}
	if err := db.close(); err != nil {
		t.Fatalf("close version two database: %v", err)
	}

	// When
	store := openTestStore(t, config)
	snapshot, err := store.Snapshot(context.Background(), "zone-upgrade")
	// Then
	if err != nil {
		t.Fatalf("snapshot upgraded queue: %v", err)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].TrackID != "preserved" ||
		snapshot.Queue[0].State != QueuePending {
		t.Fatalf("expanded migration changed queue: %+v", snapshot.Queue)
	}
	policy, err := store.ContinuationPolicy(context.Background(), "zone-upgrade")
	if err != nil || policy.Mode != "stop" {
		t.Fatalf("expanded policy = %+v, %v", policy, err)
	}
}
