package playback

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func (store *Store) DurableCommand(ctx context.Context, commandID string) (DurableCommand, error) {
	var command DurableCommand
	err := store.read(ctx, func(db *sqliteDB) error {
		loaded, err := loadDurableCommand(db, commandID)
		command = loaded
		return err
	})
	return command, err
}

func loadDurableCommand(db *sqliteDB, commandID string) (DurableCommand, error) {
	stmt, err := db.prepare(`
		SELECT command_id,zone_id,play_id,renderer_id,sequence,
			COALESCE(NULLIF(transport_kind,''),command_type),payload_json,
			receipt_state,attempts,last_error_code,last_error_detail,result_json,
			created_revision,created_at,last_attempt_at,received_at,terminal_at,
			session_id,deadline,ack_status,max_attempts,superseded_at,result_ack_at,next_attempt_at,
			COALESCE((SELECT track_id FROM playback_plays WHERE playback_plays.play_id=renderer_outbox.play_id),'')
		FROM renderer_outbox WHERE command_id=?`)
	if err != nil {
		return DurableCommand{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, commandID); err != nil {
		return DurableCommand{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return DurableCommand{}, err
	}
	if !row {
		return DurableCommand{}, ErrInvalidObservation
	}
	command := DurableCommand{
		ID: stmt.text(0), ZoneID: ZoneID(stmt.text(1)), PlayID: PlayID(stmt.text(2)),
		RendererID: RendererID(stmt.text(3)), Sequence: CommandSequence(stmt.int64(4)),
		Type: stmt.text(5), Payload: json.RawMessage(stmt.text(6)),
		ReceiptState: CommandReceiptState(stmt.text(7)), Attempts: int(stmt.int64(8)),
		LastErrorCode: stmt.text(9), LastErrorDetail: stmt.text(10),
		Result: json.RawMessage(stmt.text(11)), CreatedRevision: Revision(stmt.int64(12)),
		SessionID: SessionID(stmt.text(17)), AckStatus: CommandAckStatus(stmt.text(19)),
		MaxAttempts: int(stmt.int64(20)), TrackID: TrackID(stmt.text(24)),
	}
	if command.CreatedAt, err = parseStoredTime(stmt.text(13)); err != nil {
		return DurableCommand{}, err
	}
	if command.LastAttemptAt, err = parseStoredTime(stmt.text(14)); err != nil {
		return DurableCommand{}, err
	}
	if command.ReceivedAt, err = parseStoredTime(stmt.text(15)); err != nil {
		return DurableCommand{}, err
	}
	if command.TerminalAt, err = parseStoredTime(stmt.text(16)); err != nil {
		return DurableCommand{}, err
	}
	if command.Deadline, err = parseStoredTime(stmt.text(18)); err != nil {
		return DurableCommand{}, err
	}
	if command.SupersededAt, err = parseStoredTime(stmt.text(21)); err != nil {
		return DurableCommand{}, err
	}
	if command.ResultAckAt, err = parseStoredTime(stmt.text(22)); err != nil {
		return DurableCommand{}, err
	}
	command.NextAttemptAt, err = parseStoredTime(stmt.text(23))
	return command, err
}

func (store *Store) RendererCommandWakeAt(
	ctx context.Context,
	rendererID RendererID,
	epoch SessionEpoch,
) (time.Time, error) {
	var wakeAt time.Time
	err := store.read(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, rendererID, epoch); err != nil {
			return err
		}
		stmt, err := db.prepare(`SELECT next_attempt_at,deadline FROM renderer_outbox
			WHERE renderer_id=? AND superseded_at IS NULL AND receipt_state IN ('pending','received')
			ORDER BY sequence LIMIT 1`)
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(rendererID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil {
			return err
		}
		if !row {
			return ErrNoRendererCommand
		}
		nextAttempt, err := parseStoredTime(stmt.text(0))
		if err != nil {
			return err
		}
		deadline, err := parseStoredTime(stmt.text(1))
		if err != nil {
			return err
		}
		wakeAt = nextAttempt
		if deadline.Before(wakeAt) {
			wakeAt = deadline
		}
		return nil
	})
	return wakeAt, err
}

func parseStoredTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.Join(ErrCorruptDatabase, err)
	}
	return parsed, nil
}
