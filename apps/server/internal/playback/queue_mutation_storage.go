package playback

import "fmt"

func normalizeQueuePositions(db *sqliteDB, zoneID ZoneID) error {
	entries, err := activeQueueEntries(db, zoneID)
	if err != nil {
		return err
	}
	stmt, err := db.prepare("SELECT COALESCE(MAX(position),0) FROM playback_queue WHERE zone_id=? AND state='completed'")
	if err != nil {
		return err
	}
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		stmt.close()
		return err
	}
	row, err := stmt.step()
	base := int64(0)
	if err == nil && row {
		base = stmt.int64(0)
	}
	stmt.close()
	if err != nil {
		return err
	}
	for index, entry := range entries {
		if err := execBound(db, "UPDATE playback_queue SET position=? WHERE entry_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, base+int64(index+1)); err != nil {
				return err
			}
			return stmt.bindText(2, string(entry.ID))
		}); err != nil {
			return err
		}
	}
	return nil
}

type queueRemoval struct {
	zoneID   ZoneID
	entryID  QueueEntryID
	revision Revision
}

func removeQueueEntry(db *sqliteDB, removal queueRemoval) error {
	zoneID, entryID, revision := removal.zoneID, removal.entryID, removal.revision
	if err := execBound(db, "UPDATE playback_queue SET state='removed',terminal_revision=?,position=? WHERE zone_id=? AND entry_id=?", func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindInt64(2, -1_000_000_000-int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(3, string(zoneID)); err != nil {
			return err
		}
		return stmt.bindText(4, string(entryID))
	}); err != nil {
		return err
	}
	if err := execBound(db, "INSERT INTO playback_tombstones(tombstone_id,zone_id,entity_type,entity_id,revision) VALUES (?,?,'queue_entry',?,?)", func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, fmt.Sprintf("%s:queue:%020d", zoneID, revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(3, string(entryID)); err != nil {
			return err
		}
		return stmt.bindInt64(4, int64(revision))
	}); err != nil {
		return err
	}
	return prunePlaybackTombstones(db, zoneID)
}

func clearQueue(db *sqliteDB, zoneID ZoneID, revision Revision) error {
	if err := execBound(db, `INSERT OR IGNORE INTO playback_tombstones(
		tombstone_id,zone_id,entity_type,entity_id,revision
	) SELECT zone_id||':queue:'||entry_id,zone_id,'queue_entry',entry_id,?
	FROM playback_queue WHERE zone_id=? AND state IN ('pending','blocked')`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	}); err != nil {
		return err
	}
	if err := execBound(db, `UPDATE playback_queue SET state='removed',terminal_revision=?,
		position=-1000000000-?-position WHERE zone_id=? AND state IN ('pending','blocked')`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindInt64(2, int64(revision)*10_001); err != nil {
			return err
		}
		return stmt.bindText(3, string(zoneID))
	}); err != nil {
		return err
	}
	return prunePlaybackTombstones(db, zoneID)
}

func prunePlaybackTombstones(db *sqliteDB, zoneID ZoneID) error {
	return execBound(db, `DELETE FROM playback_tombstones WHERE zone_id=? AND tombstone_id NOT IN (
		SELECT tombstone_id FROM playback_tombstones WHERE zone_id=? ORDER BY revision DESC,tombstone_id DESC LIMIT ?
	)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(zoneID)); err != nil {
			return err
		}
		return stmt.bindInt64(3, playbackTombstoneLimit)
	})
}
