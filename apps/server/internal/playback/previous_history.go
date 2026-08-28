package playback

const (
	previousHistoryLimit   = 256
	playbackTombstoneLimit = 512
)

type previousHistoryEntry struct {
	historyID   string
	sourcePlay  PlayID
	sourceQueue QueueEntryID
	trackID     TrackID
}

func recordPreviousHistory(db *sqliteDB, completion playCompletion, entry previousHistoryEntry) error {
	if err := execBound(db, `INSERT OR IGNORE INTO playback_previous_history(
		history_id,zone_id,source_play_id,source_queue_entry_id,track_id,completed_revision
	) VALUES (?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		values := []string{entry.historyID, string(completion.zoneID), string(entry.sourcePlay), string(entry.sourceQueue), string(entry.trackID)}
		for index, value := range values {
			if value == "" && index == 3 {
				continue
			}
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		return stmt.bindInt64(6, int64(completion.revision))
	}); err != nil {
		return err
	}
	if err := execBound(db, `INSERT OR IGNORE INTO playback_tombstones(
		tombstone_id,zone_id,entity_type,entity_id,revision
	) SELECT zone_id||':history:'||source_play_id,zone_id,'playback_history',source_play_id,?
	FROM playback_previous_history WHERE zone_id=? AND history_id NOT IN (
		SELECT history_id FROM playback_previous_history WHERE zone_id=?
		ORDER BY completed_revision DESC,history_id DESC LIMIT ?
	)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(completion.revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(completion.zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(3, string(completion.zoneID)); err != nil {
			return err
		}
		return stmt.bindInt64(4, previousHistoryLimit)
	}); err != nil {
		return err
	}
	if err := execBound(db, `DELETE FROM playback_previous_history WHERE zone_id=? AND history_id NOT IN (
		SELECT history_id FROM playback_previous_history WHERE zone_id=?
		ORDER BY completed_revision DESC,history_id DESC LIMIT ?
	)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(completion.zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(completion.zoneID)); err != nil {
			return err
		}
		return stmt.bindInt64(3, previousHistoryLimit)
	}); err != nil {
		return err
	}
	return prunePlaybackTombstones(db, completion.zoneID)
}

func latestPreviousHistory(db *sqliteDB, zoneID ZoneID) (previousHistoryEntry, error) {
	stmt, err := db.prepare(`SELECT history_id,source_play_id,COALESCE(source_queue_entry_id,''),track_id
		FROM playback_previous_history WHERE zone_id=? AND consumed_revision IS NULL
		ORDER BY completed_revision DESC,history_id DESC LIMIT 1`)
	if err != nil {
		return previousHistoryEntry{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return previousHistoryEntry{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return previousHistoryEntry{}, err
	}
	if !row {
		return previousHistoryEntry{}, ErrPlaybackHistoryEmpty
	}
	return previousHistoryEntry{
		historyID: stmt.text(0), sourcePlay: PlayID(stmt.text(1)),
		sourceQueue: QueueEntryID(stmt.text(2)), trackID: TrackID(stmt.text(3)),
	}, nil
}

type historyConsumption struct {
	entry        previousHistoryEntry
	revision     Revision
	replayPlayID PlayID
}

func consumePreviousHistory(db *sqliteDB, consumption historyConsumption) error {
	return execBound(db, `UPDATE playback_previous_history SET consumed_revision=?,replay_play_id=?
		WHERE history_id=? AND consumed_revision IS NULL`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(consumption.revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(consumption.replayPlayID)); err != nil {
			return err
		}
		return stmt.bindText(3, consumption.entry.historyID)
	})
}

func previousReplayPlay(db *sqliteDB, playID PlayID) (bool, error) {
	stmt, err := db.prepare("SELECT count(*) FROM playback_previous_history WHERE replay_play_id=?")
	if err != nil {
		return false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(playID)); err != nil {
		return false, err
	}
	row, err := stmt.step()
	if err != nil {
		return false, err
	}
	if !row {
		return false, ErrCorruptDatabase
	}
	return stmt.int64(0) == 1, nil
}
