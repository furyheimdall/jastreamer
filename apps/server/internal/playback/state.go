package playback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func (store *Store) ReserveNext(ctx context.Context, zoneID ZoneID, boundary Boundary) (Decision, error) {
	if boundary.ID == "" {
		return Decision{}, ErrInvalidObservation
	}
	decision := Decision{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := ensureZone(db, zoneID); err != nil {
			return err
		}
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		if zone.sessionID != "" {
			found, existing, err := loadDecision(db, decisionKey{
				zoneID: zoneID, sessionID: zone.sessionID,
				boundaryID: boundary.ID, previousPlay: boundary.PreviousPlayID,
			})
			if err != nil {
				return err
			}
			if found {
				decision = existing
				return nil
			}
		}
		if err := validateReserveState(zone, boundary); err != nil {
			return err
		}
		if boundary.PreviousPlayID != "" {
			if err := completePlay(db, playCompletion{zoneID: zoneID, playID: boundary.PreviousPlayID, revision: zone.revision + 1}); err != nil {
				return err
			}
		}
		if zone.transport == TransportIdle {
			_, _, found, err := queueHead(db, zoneID)
			if err != nil {
				return err
			}
			if !found {
				decision = Decision{
					ID:   fmt.Sprintf("%s:idle:%s", zoneID, boundary.ID),
					Kind: DecisionStop, Reason: ReasonQueueEmpty, Revision: zone.revision,
				}
				return nil
			}
		}
		if zone.sessionID == "" || zone.transport == TransportIdle {
			if zone.sessionID != "" {
				if err := endSession(db, sessionEnd{sessionID: zone.sessionID, revision: zone.revision + 1, reason: "new_idle_play"}); err != nil {
					return err
				}
			}
			seedBytes := make([]byte, 16)
			if _, err := rand.Read(seedBytes); err != nil {
				return fmt.Errorf("generate session seed: %w", err)
			}
			zone.sessionID = SessionID(fmt.Sprintf("%s:s:%020d", zoneID, zone.revision+1))
			zone.seed = hex.EncodeToString(seedBytes)
			if err := execBound(db, "INSERT INTO playback_sessions(session_id, zone_id, seed, started_revision) VALUES (?, ?, ?, ?)", func(stmt *sqliteStmt) error {
				if err := stmt.bindText(1, string(zone.sessionID)); err != nil {
					return err
				}
				if err := stmt.bindText(2, string(zoneID)); err != nil {
					return err
				}
				if err := stmt.bindText(3, zone.seed); err != nil {
					return err
				}
				return stmt.bindInt64(4, int64(zone.revision+1))
			}); err != nil {
				return err
			}
		}
		revision := zone.revision + 1
		sequence := zone.decisionSequence + 1
		head, available, found, err := queueHead(db, zoneID)
		if err != nil {
			return err
		}
		decision.ID = fmt.Sprintf("%s:d:%020d", zone.sessionID, sequence)
		decision.Revision = revision
		switch {
		case !found:
			decision.Kind = DecisionStop
			decision.Reason = ReasonQueueEmpty
		case !available:
			decision.Kind = DecisionBlock
			decision.Reason = ReasonBlockExplicit
			decision.QueueEntryID = head.ID
			decision.TrackID = head.TrackID
			if err := setQueueState(db, queueTransition{entryID: head.ID, state: QueueBlocked, revision: revision}); err != nil {
				return err
			}
		default:
			decision.Kind = DecisionPlay
			decision.Reason = ReasonPlayExplicit
			decision.QueueEntryID = head.ID
			decision.TrackID = head.TrackID
			decision.PlayID = PlayID(fmt.Sprintf("%s:p:%020d", zone.sessionID, sequence))
			if err := reservePlay(db, reservation{zoneID: zoneID, sessionID: zone.sessionID, boundary: boundary, decision: decision, revision: revision}); err != nil {
				return err
			}
		}
		if err := insertDecision(db, decisionRecord{
			zoneID: zoneID, sessionID: zone.sessionID, boundaryID: boundary.ID,
			previousPlayID: boundary.PreviousPlayID, sequence: sequence, decision: decision,
		}); err != nil {
			return err
		}
		transport := TransportIdle
		switch decision.Kind {
		case DecisionPlay:
			transport = TransportStarting
		case DecisionBlock:
			transport = TransportBlocked
		}
		return updateZoneDecision(db, zoneDecisionUpdate{zoneID: zoneID, sessionID: zone.sessionID, seed: zone.seed, playID: decision.PlayID, revision: revision, sequence: sequence, transport: transport})
	})
	return decision, err
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

func reservePlay(db *sqliteDB, record reservation) error {
	zoneID, sessionID, boundary, d, revision := record.zoneID, record.sessionID, record.boundary, record.decision, record.revision
	if err := setQueueState(db, queueTransition{entryID: d.QueueEntryID, state: QueueReserved, playID: d.PlayID, revision: revision}); err != nil {
		return err
	}
	if err := execBound(db, "INSERT INTO playback_plays(play_id,zone_id,session_id,queue_entry_id,track_id,state,boundary_id) VALUES (?,?,?,?,?,'reserved',?)", func(s *sqliteStmt) error {
		values := []string{string(d.PlayID), string(zoneID), string(sessionID), string(d.QueueEntryID), string(d.TrackID), string(boundary.ID)}
		for i, v := range values {
			if err := s.bindText(i+1, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return execBound(db, "INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision) VALUES (?,?,?,'play',?)", func(s *sqliteStmt) error {
		if err := s.bindText(1, d.ID); err != nil {
			return err
		}
		if err := s.bindText(2, string(zoneID)); err != nil {
			return err
		}
		if err := s.bindText(3, string(d.PlayID)); err != nil {
			return err
		}
		return s.bindInt64(4, int64(revision))
	})
}

func insertDecision(db *sqliteDB, record decisionRecord) error {
	zoneID, sessionID, boundaryID, sequence, d := record.zoneID, record.sessionID, record.boundaryID, record.sequence, record.decision
	return execBound(db, "INSERT INTO playback_decisions(decision_id,zone_id,session_id,boundary_id,previous_play_id,sequence,kind,reason,play_id,queue_entry_id,committed_revision) VALUES (?,?,?,?,?,?,?,?,?,?,?)", func(s *sqliteStmt) error {
		values := []string{d.ID, string(zoneID), string(sessionID), string(boundaryID), string(record.previousPlayID), string(d.Kind), d.Reason, string(d.PlayID), string(d.QueueEntryID)}
		indexes := []int{1, 2, 3, 4, 5, 7, 8, 9, 10}
		for i, v := range values {
			if err := s.bindText(indexes[i], v); err != nil {
				return err
			}
		}
		if err := s.bindInt64(6, sequence); err != nil {
			return err
		}
		return s.bindInt64(11, int64(d.Revision))
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
