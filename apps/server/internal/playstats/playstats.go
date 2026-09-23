package playstats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Initialize creates the durable server-wide play count storage.
func Initialize(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("playstats: database is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	const schema = `CREATE TABLE IF NOT EXISTS track_play_counts (
  track_id TEXT PRIMARY KEY,
  play_count INTEGER NOT NULL CHECK(play_count >= 0),
  last_play_id TEXT NOT NULL
) STRICT;`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("playstats: initialize storage: %w", err)
	}
	return nil
}

// Record increments trackID unless playID is its most recently counted play.
// The caller must reject stale playback identities before recording; its
// transaction atomically updates the count and the last-play retry fence.
func Record(ctx context.Context, tx *sql.Tx, trackID, playID string) (bool, error) {
	if tx == nil || trackID == "" || playID == "" {
		return false, errors.New("playstats: transaction, track identity, and play identity are required")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO track_play_counts(track_id,play_count,last_play_id)
VALUES(?,1,?)
ON CONFLICT(track_id) DO UPDATE SET
  play_count=track_play_counts.play_count+1,
  last_play_id=excluded.last_play_id
WHERE track_play_counts.last_play_id<>excluded.last_play_id`, trackID, playID)
	if err != nil {
		return false, fmt.Errorf("playstats: record play: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("playstats: inspect recorded play: %w", err)
	}
	return changed == 1, nil
}
