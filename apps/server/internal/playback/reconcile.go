package playback

import "context"

func (store *Store) Reconcile(
	ctx context.Context,
	zoneID ZoneID,
	observation RendererObservation,
) (ReconcileResult, error) {
	result := ReconcileResult{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		result.PlayID = zone.currentPlay
		if observation.Playing && (!observation.OutcomeKnown || observation.PlayID == "" ||
			observation.PlayID != zone.currentPlay) {
			return ErrInvalidObservation
		}
		if !observation.Playing && observation.PlayID != "" && observation.PlayID != zone.currentPlay {
			return ErrInvalidObservation
		}
		if zone.currentPlay == "" {
			if observation.Playing {
				return ErrInvalidObservation
			}
			result.Transport = zone.transport
			return nil
		}
		if observation.OutcomeKnown && observation.Playing {
			result.Transport = TransportPlaying
			if zone.transport == TransportPlaying {
				return nil
			}
			return reconcilePlaying(db, zoneID, zone)
		}
		result.Transport = TransportSuspended
		if zone.transport == TransportSuspended {
			return nil
		}
		revision := zone.revision + 1
		return execBound(db, "UPDATE playback_zones SET revision=?,transport='suspended' WHERE zone_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return stmt.bindText(2, string(zoneID))
		})
	})
	return result, err
}

func reconcilePlaying(db *sqliteDB, zoneID ZoneID, zone zoneRecord) error {
	revision := zone.revision + 1
	if err := execBound(db, `
		UPDATE playback_plays SET state='playing',started_revision=COALESCE(started_revision,?)
		WHERE play_id=? AND state='reserved'`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zone.currentPlay))
	}); err != nil {
		return err
	}
	if err := recordStartedPlay(db, zone.currentPlay); err != nil {
		return err
	}
	if err := advanceAlbumState(db, zone.currentPlay); err != nil {
		return err
	}
	if err := execBound(db, `
		UPDATE playback_queue SET state='playing'
		WHERE reserved_play_id=? AND state='reserved'`, func(stmt *sqliteStmt) error {
		return stmt.bindText(1, string(zone.currentPlay))
	}); err != nil {
		return err
	}
	if err := execBound(db, `
		UPDATE renderer_outbox SET state='confirmed' WHERE zone_id=? AND play_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zone.currentPlay))
	}); err != nil {
		return err
	}
	return execBound(db, `
		UPDATE playback_zones SET revision=?,transport='playing' WHERE zone_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	})
}
