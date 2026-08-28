package playback

import (
	"context"
	"errors"
	"time"
)

func (store *Store) BindCommandDelivery(ctx context.Context, delivery CommandDelivery) error {
	if delivery.CommandID == "" || delivery.RendererID == "" || delivery.Sequence <= 0 ||
		delivery.CreatedAt.IsZero() {
		return ErrInvalidRequest
	}
	if err := validateCommandPayload(delivery.Payload); err != nil {
		return err
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		createdAt := delivery.CreatedAt.UTC().Format(time.RFC3339Nano)
		stmt, err := db.prepare(`
			SELECT renderer_id,sequence,payload_json,created_at
			FROM renderer_outbox WHERE command_id=?`)
		if err != nil {
			return err
		}
		if err := stmt.bindText(1, delivery.CommandID); err != nil {
			stmt.close()
			return err
		}
		row, err := stmt.step()
		if err != nil || !row {
			stmt.close()
			return errors.Join(err, ErrInvalidObservation)
		}
		boundRenderer := stmt.text(0)
		if boundRenderer != "" {
			matches := boundRenderer == string(delivery.RendererID) &&
				stmt.int64(1) == int64(delivery.Sequence) &&
				stmt.text(2) == string(delivery.Payload) && stmt.text(3) == createdAt
			stmt.close()
			if matches {
				return nil
			}
			return ErrCommandDeliveryConflict
		}
		stmt.close()
		return execBound(db, `
			UPDATE renderer_outbox
			SET renderer_id=?,sequence=?,payload_json=?,created_at=?
			WHERE command_id=? AND renderer_id=''`, func(stmt *sqliteStmt) error {
			values := []string{string(delivery.RendererID), string(delivery.Payload), createdAt, delivery.CommandID}
			if err := stmt.bindText(1, values[0]); err != nil {
				return err
			}
			if err := stmt.bindInt64(2, int64(delivery.Sequence)); err != nil {
				return err
			}
			for index := 1; index < len(values); index++ {
				if err := stmt.bindText(index+2, values[index]); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func (store *Store) RecordCommandAttempt(ctx context.Context, attempt CommandAttempt) error {
	if attempt.CommandID == "" || attempt.AttemptedAt.IsZero() {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		return execBound(db, `
			UPDATE renderer_outbox SET state='sent',attempts=attempts+1,
			last_error_code=?,last_error_detail=?,last_attempt_at=?
			WHERE command_id=? AND receipt_state<>'terminal'`, func(stmt *sqliteStmt) error {
			values := []string{
				attempt.ErrorCode, attempt.ErrorDetail,
				attempt.AttemptedAt.UTC().Format(time.RFC3339Nano), attempt.CommandID,
			}
			for index, value := range values {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func (store *Store) RecordCommandReceipt(ctx context.Context, receipt CommandReceipt) error {
	if receipt.CommandID == "" || receipt.ReceivedAt.IsZero() {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		return execBound(db, `
			UPDATE renderer_outbox SET receipt_state='received',received_at=?
			WHERE command_id=? AND receipt_state='pending'`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, receipt.ReceivedAt.UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			return stmt.bindText(2, receipt.CommandID)
		})
	})
}
