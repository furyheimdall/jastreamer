package playback

import "time"

type rendererSessionWrite struct {
	rendererID   RendererID
	epoch        SessionEpoch
	generation   int64
	nextSequence CommandSequence
	cursor       CommandSequence
	connectedAt  string
}

func rendererHighestSequence(db *sqliteDB, rendererID RendererID) (CommandSequence, error) {
	stmt, err := db.prepare("SELECT COALESCE(MAX(sequence),0) FROM renderer_outbox WHERE renderer_id=?")
	if err != nil {
		return 0, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return 0, err
	}
	row, err := stmt.step()
	if err != nil {
		return 0, err
	}
	if !row {
		return 0, ErrCorruptDatabase
	}
	return CommandSequence(stmt.int64(0)), nil
}

func rendererSessionCounters(db *sqliteDB, rendererID RendererID) (int64, CommandSequence, bool, error) {
	stmt, err := db.prepare("SELECT generation,next_sequence FROM renderer_session_state WHERE renderer_id=?")
	if err != nil {
		return 0, 0, false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return 0, 0, false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return 0, 1, false, err
	}
	return stmt.int64(0), CommandSequence(stmt.int64(1)), true, nil
}

func upsertRendererSession(db *sqliteDB, write rendererSessionWrite) error {
	return execBound(db, `INSERT INTO renderer_session_state(
		renderer_id,generation,current_epoch,connection_state,next_sequence,reconnect_cursor,connected_at
	) VALUES (?,?,?,'connected',?,?,?) ON CONFLICT(renderer_id) DO UPDATE SET
		generation=excluded.generation,current_epoch=excluded.current_epoch,connection_state='connected',
		next_sequence=excluded.next_sequence,reconnect_cursor=excluded.reconnect_cursor,
		connected_at=excluded.connected_at,disconnected_at=NULL`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(write.rendererID)); err != nil {
			return err
		}
		if err := stmt.bindInt64(2, write.generation); err != nil {
			return err
		}
		if err := stmt.bindText(3, string(write.epoch)); err != nil {
			return err
		}
		if err := stmt.bindInt64(4, int64(write.nextSequence)); err != nil {
			return err
		}
		if err := stmt.bindInt64(5, int64(write.cursor)); err != nil {
			return err
		}
		return stmt.bindText(6, write.connectedAt)
	})
}

type rendererConnectionUpdate struct {
	rendererID RendererID
	state      RendererState
	observedAt string
}

func setRendererConnectionState(db *sqliteDB, update rendererConnectionUpdate) error {
	return execBound(db, `UPDATE renderer_registry SET state=?,revision=revision+1,updated_at=?
		WHERE renderer_id=? AND state<>'revoked'`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(update.state)); err != nil {
			return err
		}
		if err := stmt.bindText(2, update.observedAt); err != nil {
			return err
		}
		return stmt.bindText(3, string(update.rendererID))
	})
}

func updateRendererObservation(db *sqliteDB, event RendererPlaybackEvent) error {
	return execBound(db, `UPDATE renderer_session_state SET observed_play_id=?,observed_state=?,
		observed_position_ms=?,observed_at=? WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(event.PlayID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(event.Kind)); err != nil {
			return err
		}
		if event.PositionMS == nil {
			if err := stmt.bind(3, nil); err != nil {
				return err
			}
		} else if err := stmt.bindInt64(3, *event.PositionMS); err != nil {
			return err
		}
		if err := stmt.bindText(4, event.ObservedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return stmt.bindText(5, string(event.RendererID))
	})
}
