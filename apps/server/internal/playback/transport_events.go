package playback

import (
	"context"
	"fmt"
)

func (store *Store) applyEvent(ctx context.Context, zoneID ZoneID, event TransportEvent) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		transport, err := Transition(zone.transport, event)
		if err != nil {
			return err
		}
		revision := zone.revision + 1
		if err := execBound(db, "UPDATE playback_zones SET revision=?,transport=? WHERE zone_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(zone.revision+1)); err != nil {
				return err
			}
			if err := stmt.bindText(2, string(transport)); err != nil {
				return err
			}
			return stmt.bindText(3, string(zoneID))
		}); err != nil {
			return err
		}
		if event != EventPause && event != EventResume {
			return nil
		}
		if zone.currentPlay == "" || zone.sessionID == "" {
			return ErrInvalidTransition
		}
		commandID := fmt.Sprintf("%s:%s:%020d", zone.sessionID, event, revision)
		return execBound(db, "INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision) VALUES (?,?,?,?,?)", func(stmt *sqliteStmt) error {
			values := []string{commandID, string(zoneID), string(zone.currentPlay), string(event)}
			for index, value := range values {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return stmt.bindInt64(5, int64(revision))
		})
	})
}

func (store *Store) Pause(ctx context.Context, zoneID ZoneID) error {
	return store.applyEvent(ctx, zoneID, EventPause)
}

func (store *Store) Resume(ctx context.Context, zoneID ZoneID) error {
	return store.applyEvent(ctx, zoneID, EventResume)
}

func (store *Store) Disconnect(ctx context.Context, zoneID ZoneID) error {
	return store.applyEvent(ctx, zoneID, EventDisconnect)
}

func (store *Store) MidTrackFailure(ctx context.Context, zoneID ZoneID) error {
	return store.applyEvent(ctx, zoneID, EventFailure)
}

func (store *Store) ExternalOverride(ctx context.Context, zoneID ZoneID) error {
	return store.applyEvent(ctx, zoneID, EventExternalOverride)
}

func (store *Store) Stop(ctx context.Context, zoneID ZoneID) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		if zone.transport == TransportIdle && zone.sessionID == "" && zone.currentPlay == "" {
			return nil
		}
		revision := zone.revision + 1
		if zone.currentPlay != "" {
			if err := execBound(db, "UPDATE renderer_outbox SET state='confirmed' WHERE zone_id=? AND play_id=? AND command_type='play' AND state!='confirmed'", func(stmt *sqliteStmt) error {
				if err := stmt.bindText(1, string(zoneID)); err != nil {
					return err
				}
				return stmt.bindText(2, string(zone.currentPlay))
			}); err != nil {
				return err
			}
			commandID := fmt.Sprintf("%s:stop:%020d", zone.sessionID, revision)
			if err := execBound(db, "INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision) VALUES (?,?,?,'stop',?)", func(stmt *sqliteStmt) error {
				if err := stmt.bindText(1, commandID); err != nil {
					return err
				}
				if err := stmt.bindText(2, string(zoneID)); err != nil {
					return err
				}
				if err := stmt.bindText(3, string(zone.currentPlay)); err != nil {
					return err
				}
				return stmt.bindInt64(4, int64(revision))
			}); err != nil {
				return err
			}
			if err := execBound(db, "UPDATE playback_plays SET state='stopped',terminal_revision=? WHERE play_id=? AND state IN ('reserved','playing')", func(s *sqliteStmt) error {
				if err := s.bindInt64(1, int64(revision)); err != nil {
					return err
				}
				return s.bindText(2, string(zone.currentPlay))
			}); err != nil {
				return err
			}
			if err := execBound(db, "UPDATE playback_queue SET state=CASE state WHEN 'reserved' THEN 'pending' WHEN 'playing' THEN 'completed' ELSE state END,reserved_play_id=NULL,terminal_revision=CASE state WHEN 'playing' THEN ? ELSE terminal_revision END WHERE reserved_play_id=?", func(s *sqliteStmt) error {
				if err := s.bindInt64(1, int64(revision)); err != nil {
					return err
				}
				return s.bindText(2, string(zone.currentPlay))
			}); err != nil {
				return err
			}
		}
		if zone.sessionID != "" {
			if err := endSession(db, sessionEnd{sessionID: zone.sessionID, revision: revision, reason: "stop"}); err != nil {
				return err
			}
		}
		return execBound(db, "UPDATE playback_zones SET revision=?,transport='idle',session_id=NULL,session_seed=NULL,current_play_id=NULL WHERE zone_id=?", func(s *sqliteStmt) error {
			if err := s.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return s.bindText(2, string(zoneID))
		})
	})
}

func (store *Store) PendingOutbox(ctx context.Context, zoneID ZoneID) ([]OutboxCommand, error) {
	var commands []OutboxCommand
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT command_id,play_id,command_type,state FROM renderer_outbox WHERE zone_id=? AND state='pending' AND failed_revision IS NULL ORDER BY created_revision,command_id")
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		for {
			row, err := stmt.step()
			if err != nil {
				return err
			}
			if !row {
				break
			}
			commands = append(commands, OutboxCommand{ID: stmt.text(0), PlayID: PlayID(stmt.text(1)), Type: stmt.text(2), State: stmt.text(3)})
		}
		return nil
	})
	return commands, err
}

func (store *Store) AcknowledgeOutbox(ctx context.Context, zoneID ZoneID, commandID string) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT command_type,state FROM renderer_outbox WHERE zone_id=? AND command_id=?")
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, commandID); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil {
			return err
		}
		if !row {
			return ErrInvalidObservation
		}
		if stmt.text(0) == "play" {
			return ErrInvalidObservation
		}
		if stmt.text(1) == "confirmed" {
			return nil
		}
		return execBound(db, "UPDATE renderer_outbox SET state='confirmed' WHERE zone_id=? AND command_id=?", func(update *sqliteStmt) error {
			if err := update.bindText(1, string(zoneID)); err != nil {
				return err
			}
			return update.bindText(2, commandID)
		})
	})
}
