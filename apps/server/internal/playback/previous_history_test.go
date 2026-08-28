package playback

import (
	"context"
	"fmt"
	"testing"
)

func Test_PreviousHistory_and_tombstones_remain_bounded(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	const zoneID ZoneID = "bounded-history"
	if _, err := store.CreateZone(context.Background(), ZoneDefinition{ID: zoneID, DisplayName: "Bounded"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	for index := range playbackTombstoneLimit + 100 {
		if err := execBound(store.db, `INSERT INTO playback_tombstones(
			tombstone_id,zone_id,entity_type,entity_id,revision
		) VALUES (?,?,?,?,?)`, func(stmt *sqliteStmt) error {
			values := []string{fmt.Sprintf("old:%04d", index), string(zoneID), "queue_entry", fmt.Sprintf("entry:%04d", index)}
			for valueIndex, value := range values {
				if err := stmt.bindText(valueIndex+1, value); err != nil {
					return err
				}
			}
			return stmt.bindInt64(5, int64(index+1))
		}); err != nil {
			t.Fatalf("seed tombstone %d: %v", index, err)
		}
	}

	// When
	for index := range previousHistoryLimit + 20 {
		playID := PlayID(fmt.Sprintf("history-play-%04d", index))
		if err := recordPreviousHistory(store.db, playCompletion{
			zoneID: zoneID, playID: playID, revision: Revision(1_000 + index),
		}, previousHistoryEntry{
			historyID: string(playID), sourcePlay: playID,
			sourceQueue: QueueEntryID(fmt.Sprintf("history-entry-%04d", index)),
			trackID:     TrackID(fmt.Sprintf("history-track-%04d", index)),
		}); err != nil {
			t.Fatalf("record history %d: %v", index, err)
		}
	}

	// Then
	historyCount := scalarCount(t, store, "SELECT count(*) FROM playback_previous_history WHERE zone_id=?", string(zoneID))
	tombstoneCount := scalarCount(t, store, "SELECT count(*) FROM playback_tombstones WHERE zone_id=?", string(zoneID))
	if historyCount != previousHistoryLimit || tombstoneCount > playbackTombstoneLimit {
		t.Fatalf("bounded rows = history %d/%d tombstones %d/%d", historyCount, previousHistoryLimit, tombstoneCount, playbackTombstoneLimit)
	}
	latest, err := latestPreviousHistory(store.db, zoneID)
	if err != nil || latest.trackID != "history-track-0275" {
		t.Fatalf("latest retained history = %+v (%v)", latest, err)
	}
}
