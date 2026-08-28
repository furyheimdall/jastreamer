package playback

import (
	"context"
	"time"
)

func (store *Store) AcquireRendererCommand(ctx context.Context, request RendererCommandRequest) (DurableCommand, error) {
	if request.RendererID == "" || request.Epoch == "" || request.AttemptedAt.IsZero() ||
		request.Deadline.IsZero() || !request.Deadline.After(request.AttemptedAt) {
		return DurableCommand{}, ErrInvalidRequest
	}
	var command DurableCommand
	var outcome error
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, request.RendererID, request.Epoch); err != nil {
			return err
		}
		commandID, found, err := activeRendererCommandID(db, request.RendererID)
		if err != nil {
			return err
		}
		if !found {
			commandID, found, err = unboundRendererCommandID(db, request.RendererID)
			if err != nil {
				return err
			}
			if !found {
				outcome = ErrNoRendererCommand
				return nil
			}
			if err := bindRendererCommand(db, commandID, request); err != nil {
				return err
			}
		}
		command, err = loadDurableCommand(db, commandID)
		if err != nil {
			return err
		}
		now := request.AttemptedAt.UTC()
		switch {
		case !command.Deadline.IsZero() && !now.Before(command.Deadline):
			outcome = ErrCommandExpired
			if err := recordRendererDeliveryFailure(db, rendererDeliveryFailure{
				commandID: command.ID, code: "COMMAND_EXPIRED", at: now,
			}); err != nil {
				return err
			}
			return suspendAssignedRenderer(db, request.RendererID)
		case command.Attempts >= command.MaxAttempts:
			outcome = ErrCommandRetryExhausted
			if err := recordRendererDeliveryFailure(db, rendererDeliveryFailure{
				commandID: command.ID, code: "RETRY_EXHAUSTED", at: now,
			}); err != nil {
				return err
			}
			return suspendAssignedRenderer(db, request.RendererID)
		}
		if err := recordRendererDeliveryAttempt(db, command, now); err != nil {
			return err
		}
		command, err = loadDurableCommand(db, command.ID)
		return err
	})
	if err != nil {
		return DurableCommand{}, err
	}
	if outcome != nil {
		return DurableCommand{}, outcome
	}
	return command, nil
}

func activeRendererCommandID(db *sqliteDB, rendererID RendererID) (string, bool, error) {
	stmt, err := db.prepare(`SELECT command_id FROM renderer_outbox
		WHERE renderer_id=? AND receipt_state<>'terminal' AND superseded_at IS NULL
		ORDER BY sequence LIMIT 1`)
	if err != nil {
		return "", false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return "", false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return "", false, err
	}
	return stmt.text(0), true, nil
}

func unboundRendererCommandID(db *sqliteDB, rendererID RendererID) (string, bool, error) {
	stmt, err := db.prepare(`SELECT o.command_id FROM renderer_outbox o
		JOIN renderer_assignments a ON a.zone_id=o.zone_id AND a.unassigned_revision IS NULL
		WHERE a.renderer_id=? AND o.renderer_id='' AND o.state<>'confirmed' AND o.media_ready=1
			AND o.failed_revision IS NULL AND o.superseded_at IS NULL
		ORDER BY o.created_revision,o.command_id LIMIT 1`)
	if err != nil {
		return "", false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return "", false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return "", false, err
	}
	return stmt.text(0), true, nil
}

func recordRendererDeliveryAttempt(db *sqliteDB, command DurableCommand, attemptedAt time.Time) error {
	backoff := time.Duration(1<<min(command.Attempts, 5)) * time.Second
	return execBound(db, `UPDATE renderer_outbox SET state='sent',attempts=attempts+1,
		last_attempt_at=?,next_attempt_at=?,last_error_code='',last_error_detail=''
		WHERE command_id=? AND receipt_state<>'terminal'`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, attemptedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := stmt.bindText(2, attemptedAt.Add(backoff).Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return stmt.bindText(3, command.ID)
	})
}

type rendererDeliveryFailure struct {
	commandID string
	code      string
	at        time.Time
}

func recordRendererDeliveryFailure(db *sqliteDB, failure rendererDeliveryFailure) error {
	return execBound(db, `UPDATE renderer_outbox SET last_error_code=?,last_attempt_at=? WHERE command_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, failure.code); err != nil {
			return err
		}
		if err := stmt.bindText(2, failure.at.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		return stmt.bindText(3, failure.commandID)
	})
}
