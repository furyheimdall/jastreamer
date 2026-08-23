package playback

import (
	"context"
	"fmt"
)

func (store *Store) UpdateAvailability(ctx context.Context, request AvailabilityRequest) (Revision, error) {
	var revision Revision
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		if zone.revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		count, current, err := availabilityState(db, request.ZoneID, request.TrackID)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrInvalidRequest
		}
		desired := int64(0)
		if request.Available {
			desired = 1
		}
		if current == desired {
			revision = zone.revision
			return nil
		}
		revision = zone.revision + 1
		if err := execBound(db, "UPDATE playback_queue SET available=? WHERE zone_id=? AND track_id=? AND state IN ('pending','blocked')", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, desired); err != nil {
				return err
			}
			if err := stmt.bindText(2, string(request.ZoneID)); err != nil {
				return err
			}
			return stmt.bindText(3, string(request.TrackID))
		}); err != nil {
			return err
		}
		return execBound(db, "UPDATE playback_zones SET revision=? WHERE zone_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return stmt.bindText(2, string(request.ZoneID))
		})
	})
	return revision, err
}

func availabilityState(db *sqliteDB, zoneID ZoneID, trackID TrackID) (int64, int64, error) {
	stmt, err := db.prepare("SELECT count(*),min(available),max(available) FROM playback_queue WHERE zone_id=? AND track_id=? AND state IN ('pending','blocked')")
	if err != nil {
		return 0, 0, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return 0, 0, err
	}
	if err := stmt.bindText(2, string(trackID)); err != nil {
		return 0, 0, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return 0, 0, err
	}
	count, minimum := stmt.int64(0), stmt.int64(1)
	if count > 0 && minimum != stmt.int64(2) {
		return 0, 0, ErrInvalidObservation
	}
	return count, minimum, nil
}

func (store *Store) RetryBlocked(ctx context.Context, zoneID ZoneID, expected Revision) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		if zone.revision != expected {
			return ErrRevisionConflict
		}
		head, _, found, err := queueHead(db, zoneID)
		if err != nil {
			return err
		}
		if !found || head.State != QueueBlocked {
			return ErrInvalidTransition
		}
		revision := zone.revision + 1
		if err := setQueueState(db, queueTransition{entryID: head.ID, state: QueuePending, revision: revision}); err != nil {
			return err
		}
		return execBound(db, "UPDATE playback_zones SET revision=?,transport='selecting',current_play_id=NULL WHERE zone_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return stmt.bindText(2, string(zoneID))
		})
	})
}

func (store *Store) SkipBlocked(ctx context.Context, zoneID ZoneID, expected Revision) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		if zone.revision != expected {
			return ErrRevisionConflict
		}
		head, _, found, err := queueHead(db, zoneID)
		if err != nil {
			return err
		}
		if !found || head.State != QueueBlocked {
			return ErrInvalidTransition
		}
		revision := zone.revision + 1
		if err := setQueueState(db, queueTransition{entryID: head.ID, state: QueueRemoved, revision: revision}); err != nil {
			return err
		}
		tombstoneID := fmt.Sprintf("%s:queue:%020d", zoneID, revision)
		if err := execBound(db, "INSERT INTO playback_tombstones(tombstone_id,zone_id,entity_type,entity_id,revision) VALUES (?,?,'queue_entry',?,?)", func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, tombstoneID); err != nil {
				return err
			}
			if err := stmt.bindText(2, string(zoneID)); err != nil {
				return err
			}
			if err := stmt.bindText(3, string(head.ID)); err != nil {
				return err
			}
			return stmt.bindInt64(4, int64(revision))
		}); err != nil {
			return err
		}
		return execBound(db, "UPDATE playback_zones SET revision=?,transport='selecting',current_play_id=NULL WHERE zone_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return stmt.bindText(2, string(zoneID))
		})
	})
}
