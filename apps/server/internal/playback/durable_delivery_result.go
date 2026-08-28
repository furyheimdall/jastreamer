package playback

import (
	"context"
	"time"
)

func (store *Store) RecordCommandResult(ctx context.Context, result CommandResult) error {
	if result.CommandID == "" || result.RendererID == "" || result.Sequence <= 0 ||
		result.RecordedAt.IsZero() {
		return ErrInvalidRequest
	}
	if err := validateCommandPayload(result.Result); err != nil {
		return err
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		recordedAt := result.RecordedAt.UTC().Format(time.RFC3339Nano)
		stmt, err := db.prepare(`
			SELECT renderer_id,sequence,outcome,result_json,error_code,error_detail,recorded_at
			FROM renderer_command_results WHERE command_id=?`)
		if err != nil {
			return err
		}
		if err := stmt.bindText(1, result.CommandID); err != nil {
			stmt.close()
			return err
		}
		row, err := stmt.step()
		if err != nil {
			stmt.close()
			return err
		}
		if row {
			matches := stmt.text(0) == string(result.RendererID) &&
				stmt.int64(1) == int64(result.Sequence) && stmt.text(2) == result.Outcome &&
				stmt.text(3) == string(result.Result) && stmt.text(4) == result.ErrorCode &&
				stmt.text(5) == result.ErrorDetail && stmt.text(6) == recordedAt
			stmt.close()
			if matches {
				return nil
			}
			return ErrCommandResultConflict
		}
		stmt.close()
		if err := execBound(db, `
			INSERT INTO renderer_command_results(
				command_id,renderer_id,sequence,outcome,result_json,error_code,error_detail,recorded_at
			) VALUES (?,?,?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
			values := []string{
				result.CommandID, string(result.RendererID), result.Outcome, string(result.Result),
				result.ErrorCode, result.ErrorDetail, recordedAt,
			}
			if err := stmt.bindText(1, values[0]); err != nil {
				return err
			}
			if err := stmt.bindText(2, values[1]); err != nil {
				return err
			}
			if err := stmt.bindInt64(3, int64(result.Sequence)); err != nil {
				return err
			}
			for index := 2; index < len(values); index++ {
				if err := stmt.bindText(index+2, values[index]); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		return execBound(db, `
			UPDATE renderer_outbox SET state='confirmed',receipt_state='terminal',
			result_json=?,last_error_code=?,last_error_detail=?,terminal_at=?
			WHERE command_id=? AND renderer_id=? AND sequence=?`, func(stmt *sqliteStmt) error {
			values := []string{
				string(result.Result), result.ErrorCode, result.ErrorDetail,
				result.RecordedAt.UTC().Format(time.RFC3339Nano), result.CommandID,
				string(result.RendererID),
			}
			for index, value := range values {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return stmt.bindInt64(7, int64(result.Sequence))
		})
	})
}
