package playstats

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/database"
)

func TestRecordPersistsAndIsIdempotentPerPlayIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.sqlite")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Initialize(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	for _, playID := range []string{"play-one", "play-one", "play-two"} {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Record(context.Background(), tx, "track-a", playID); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Initialize(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var count int64
	var lastPlayID string
	if err := db.QueryRow("SELECT play_count,last_play_id FROM track_play_counts WHERE track_id='track-a'").Scan(&count, &lastPlayID); err != nil {
		t.Fatal(err)
	}
	if count != 2 || lastPlayID != "play-two" {
		t.Fatalf("persisted count=%d last_play_id=%q", count, lastPlayID)
	}
}
