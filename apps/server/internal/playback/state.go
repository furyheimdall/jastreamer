package playback

import "context"

func (store *Store) ReserveNext(ctx context.Context, zoneID ZoneID, boundary Boundary) (Decision, error) {
	return store.CommitNext(ctx, NextRequest{ZoneID: zoneID, Boundary: boundary})
}

func validateReserveState(zone zoneRecord, boundary Boundary) error {
	switch zone.transport {
	case TransportIdle, TransportSelecting:
		if boundary.PreviousPlayID != "" {
			return ErrInvalidObservation
		}
	case TransportPlaying:
		if boundary.PreviousPlayID == "" || boundary.PreviousPlayID != zone.currentPlay {
			return ErrInvalidObservation
		}
	default:
		return ErrInvalidTransition
	}
	return nil
}

func queueHead(db *sqliteDB, zoneID ZoneID) (QueueEntry, bool, bool, error) {
	stmt, err := db.prepare("SELECT entry_id, track_id, available, state, position FROM playback_queue WHERE zone_id = ? AND state IN ('pending','blocked','reserved') ORDER BY position LIMIT 1")
	if err != nil {
		return QueueEntry{}, false, false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return QueueEntry{}, false, false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return QueueEntry{}, false, row, err
	}
	return QueueEntry{ID: QueueEntryID(stmt.text(0)), TrackID: TrackID(stmt.text(1)), State: QueueState(stmt.text(3)), Position: stmt.int64(4)}, stmt.int64(2) == 1, true, nil
}

func setQueueState(db *sqliteDB, change queueTransition) error {
	id, state, playID, revision := change.entryID, change.state, change.playID, change.revision
	return execBound(db, "UPDATE playback_queue SET state = ?, reserved_play_id = ?, terminal_revision = CASE WHEN ? IN ('completed','removed') THEN ? ELSE terminal_revision END WHERE entry_id = ?", func(s *sqliteStmt) error {
		if err := s.bindText(1, string(state)); err != nil {
			return err
		}
		if err := s.bindText(2, string(playID)); err != nil {
			return err
		}
		if err := s.bindText(3, string(state)); err != nil {
			return err
		}
		if err := s.bindInt64(4, int64(revision)); err != nil {
			return err
		}
		return s.bindText(5, string(id))
	})
}

func updateZoneDecision(db *sqliteDB, update zoneDecisionUpdate) error {
	zoneID, sessionID, seed := update.zoneID, update.sessionID, update.seed
	playID, revision, sequence, transport := update.playID, update.revision, update.sequence, update.transport
	return execBound(db, "UPDATE playback_zones SET revision=?,transport=?,session_id=?,session_seed=?,decision_sequence=?,current_play_id=? WHERE zone_id=?", func(s *sqliteStmt) error {
		if err := s.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		values := []string{string(transport), string(sessionID), seed}
		for i, v := range values {
			if err := s.bindText(i+2, v); err != nil {
				return err
			}
		}
		if err := s.bindInt64(5, sequence); err != nil {
			return err
		}
		if err := s.bindText(6, string(playID)); err != nil {
			return err
		}
		return s.bindText(7, string(zoneID))
	})
}
