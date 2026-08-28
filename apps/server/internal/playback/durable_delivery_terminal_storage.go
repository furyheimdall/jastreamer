package playback

import "time"

func insertRendererResult(db *sqliteDB, command rendererResultCommand, result RendererTerminalResult) error {
	return execBound(db, `INSERT INTO renderer_command_results(
		command_id,renderer_id,sequence,outcome,wire_status,result_json,error_code,error_detail,
		recorded_at,result_id,observed_state,position_ms
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, result.CommandID); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(result.RendererID)); err != nil {
			return err
		}
		if err := stmt.bindInt64(3, int64(command.sequence)); err != nil {
			return err
		}
		values := []string{
			storageResultOutcome(result.Status), result.Status, string(result.Payload),
			result.ErrorCode, result.ErrorDetail, result.RecordedAt.UTC().Format(time.RFC3339Nano),
			result.ResultID, result.ObservedState,
		}
		for index, value := range values {
			if err := stmt.bindText(index+4, value); err != nil {
				return err
			}
		}
		if result.PositionMS == nil {
			return stmt.bind(12, nil)
		}
		return stmt.bindInt64(12, *result.PositionMS)
	})
}

func terminalizeRendererCommand(db *sqliteDB, result RendererTerminalResult) error {
	return execBound(db, `UPDATE renderer_outbox SET state='confirmed',receipt_state='terminal',
		result_json=?,last_error_code=?,last_error_detail=?,terminal_at=?
		WHERE command_id=? AND renderer_id=?`, func(stmt *sqliteStmt) error {
		values := []string{
			string(result.Payload), result.ErrorCode, result.ErrorDetail,
			result.RecordedAt.UTC().Format(time.RFC3339Nano), result.CommandID, string(result.RendererID),
		}
		for index, value := range values {
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func rendererResultIDUsed(db *sqliteDB, rendererID RendererID, resultID string) (bool, error) {
	stmt, err := db.prepare(`SELECT count(*) FROM renderer_command_results WHERE renderer_id=? AND result_id=?`)
	if err != nil {
		return false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return false, err
	}
	if err := stmt.bindText(2, resultID); err != nil {
		return false, err
	}
	row, err := stmt.step()
	return row && stmt.int64(0) > 0, err
}

func storageResultOutcome(status string) string {
	switch status {
	case "succeeded", "failed", "unsupported", "cancelled":
		return status
	default:
		return "failed"
	}
}
