package playback

import (
	"context"
	"encoding/json"
	"time"
)

func (store *Store) RecordRendererCommandAcknowledgement(
	ctx context.Context,
	ack RendererCommandAcknowledgement,
) error {
	if ack.RendererID == "" || ack.Epoch == "" || ack.CommandID == "" || ack.Sequence <= 0 ||
		ack.RecordedAt.IsZero() || !validCommandAckStatus(ack.Status) {
		return ErrInvalidRequest
	}
	if len(ack.Error) == 0 {
		ack.Error = json.RawMessage("{}")
	}
	if err := validateCommandPayload(ack.Error); err != nil {
		return err
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, ack.RendererID, ack.Epoch); err != nil {
			return err
		}
		current, currentError, err := rendererCommandAckIdentity(db, ack)
		if err != nil {
			return err
		}
		receivedReplay := (current == CommandAckReceived || current == CommandAckDuplicate) &&
			(ack.Status == CommandAckReceived || ack.Status == CommandAckDuplicate)
		if current != "" && current != ack.Status && !receivedReplay {
			return ErrCommandDeliveryConflict
		}
		if current == CommandAckRejected && currentError != string(ack.Error) {
			return ErrCommandDeliveryConflict
		}
		recordedAt := ack.RecordedAt.UTC().Format(time.RFC3339Nano)
		if ack.Status == CommandAckRejected {
			if err := execBound(db, `UPDATE renderer_outbox SET ack_status='rejected',ack_error_json=?,
				receipt_state='terminal',state='confirmed',received_at=COALESCE(received_at,?),terminal_at=?
				WHERE command_id=?`, func(stmt *sqliteStmt) error {
				for index, value := range []string{string(ack.Error), recordedAt, recordedAt, ack.CommandID} {
					if err := stmt.bindText(index+1, value); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			return suspendAssignedRenderer(db, ack.RendererID)
		}
		return execBound(db, `UPDATE renderer_outbox SET ack_status=?,ack_error_json=?,
			receipt_state=CASE receipt_state WHEN 'terminal' THEN 'terminal' ELSE 'received' END,
			received_at=COALESCE(received_at,?) WHERE command_id=?`, func(stmt *sqliteStmt) error {
			for index, value := range []string{string(ack.Status), string(ack.Error), recordedAt, ack.CommandID} {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func rendererCommandAckIdentity(
	db *sqliteDB,
	ack RendererCommandAcknowledgement,
) (CommandAckStatus, string, error) {
	stmt, err := db.prepare(`SELECT ack_status,ack_error_json FROM renderer_outbox
		WHERE command_id=? AND renderer_id=? AND sequence=?`)
	if err != nil {
		return "", "", err
	}
	defer stmt.close()
	if err := stmt.bindText(1, ack.CommandID); err != nil {
		return "", "", err
	}
	if err := stmt.bindText(2, string(ack.RendererID)); err != nil {
		return "", "", err
	}
	if err := stmt.bindInt64(3, int64(ack.Sequence)); err != nil {
		return "", "", err
	}
	row, err := stmt.step()
	if err != nil {
		return "", "", err
	}
	if !row {
		return "", "", ErrInvalidObservation
	}
	return CommandAckStatus(stmt.text(0)), stmt.text(1), nil
}

func validCommandAckStatus(status CommandAckStatus) bool {
	switch status {
	case CommandAckReceived, CommandAckDuplicate, CommandAckRejected:
		return true
	default:
		return false
	}
}
