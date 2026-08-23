package playback

import (
	"context"
	"fmt"
)

func loadDecision(db *sqliteDB, key decisionKey) (bool, Decision, error) {
	zoneID, sessionID, boundaryID := key.zoneID, key.sessionID, key.boundaryID
	stmt, err := db.prepare("SELECT d.decision_id,d.kind,d.reason,d.play_id,d.queue_entry_id,d.committed_revision,COALESCE(p.track_id,''),d.previous_play_id FROM playback_decisions d LEFT JOIN playback_plays p ON p.play_id=d.play_id WHERE d.zone_id=? AND d.session_id=? AND d.boundary_id=?")
	if err != nil {
		return false, Decision{}, err
	}
	defer stmt.close()
	values := []string{string(zoneID), string(sessionID), string(boundaryID)}
	for index, value := range values {
		if err := stmt.bindText(index+1, value); err != nil {
			return false, Decision{}, err
		}
	}
	row, err := stmt.step()
	if err != nil || !row {
		return row, Decision{}, err
	}
	if PlayID(stmt.text(7)) != key.previousPlay {
		return false, Decision{}, ErrBoundaryConflict
	}
	decision := Decision{ID: stmt.text(0), Kind: DecisionKind(stmt.text(1)), Reason: stmt.text(2), Revision: Revision(stmt.int64(5)), TrackID: TrackID(stmt.text(6))}
	if !stmt.isNull(3) {
		decision.PlayID = PlayID(stmt.text(3))
	}
	if !stmt.isNull(4) {
		decision.QueueEntryID = QueueEntryID(stmt.text(4))
	}
	return true, decision, nil
}

func completePlay(db *sqliteDB, completion playCompletion) error {
	zoneID, playID, revision := completion.zoneID, completion.playID, completion.revision
	stmt, err := db.prepare("SELECT queue_entry_id,state FROM playback_plays WHERE play_id=? AND zone_id=?")
	if err != nil {
		return err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(playID)); err != nil {
		return err
	}
	if err := stmt.bindText(2, string(zoneID)); err != nil {
		return err
	}
	row, err := stmt.step()
	if err != nil {
		return err
	}
	if !row {
		return fmt.Errorf("previous play %q not found", playID)
	}
	entryID := QueueEntryID(stmt.text(0))
	state := stmt.text(1)
	if state == "completed" {
		return nil
	}
	if state != "playing" {
		return fmt.Errorf("previous play %q is %s", playID, state)
	}
	if err := execBound(db, "UPDATE playback_plays SET state='completed',terminal_revision=? WHERE play_id=?", func(s *sqliteStmt) error {
		if err := s.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return s.bindText(2, string(playID))
	}); err != nil {
		return err
	}
	return setQueueState(db, queueTransition{entryID: entryID, state: QueueCompleted, playID: playID, revision: revision})
}

func endSession(db *sqliteDB, ending sessionEnd) error {
	sessionID, revision, reason := ending.sessionID, ending.revision, ending.reason
	return execBound(db, "UPDATE playback_sessions SET ended_revision=?,end_reason=? WHERE session_id=? AND ended_revision IS NULL", func(s *sqliteStmt) error {
		if err := s.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := s.bindText(2, reason); err != nil {
			return err
		}
		return s.bindText(3, string(sessionID))
	})
}

func (store *Store) ConfirmStart(ctx context.Context, zoneID ZoneID, playID PlayID) (ZoneSnapshot, error) {
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		stmt, err := db.prepare("SELECT queue_entry_id,state FROM playback_plays WHERE play_id=? AND zone_id=?")
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(playID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(zoneID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil {
			return err
		}
		if !row {
			return ErrInvalidObservation
		}
		entryID := QueueEntryID(stmt.text(0))
		state := stmt.text(1)
		if state == "playing" {
			return nil
		}
		if state != "reserved" {
			return ErrInvalidObservation
		}
		revision := zone.revision + 1
		if err := setQueueState(db, queueTransition{entryID: entryID, state: QueuePlaying, playID: playID, revision: revision}); err != nil {
			return err
		}
		if err := execBound(db, "UPDATE playback_plays SET state='playing',started_revision=? WHERE play_id=?", func(s *sqliteStmt) error {
			if err := s.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return s.bindText(2, string(playID))
		}); err != nil {
			return err
		}
		if err := execBound(db, "UPDATE renderer_outbox SET state='confirmed' WHERE zone_id=? AND play_id=?", func(s *sqliteStmt) error {
			if err := s.bindText(1, string(zoneID)); err != nil {
				return err
			}
			return s.bindText(2, string(playID))
		}); err != nil {
			return err
		}
		return execBound(db, "UPDATE playback_zones SET revision=?,transport='playing',current_play_id=? WHERE zone_id=?", func(s *sqliteStmt) error {
			if err := s.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			if err := s.bindText(2, string(playID)); err != nil {
				return err
			}
			return s.bindText(3, string(zoneID))
		})
	})
	if err != nil {
		return ZoneSnapshot{}, err
	}
	return store.Snapshot(ctx, zoneID)
}
