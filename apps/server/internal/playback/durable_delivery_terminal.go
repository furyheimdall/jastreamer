package playback

import (
	"context"
	"encoding/json"
	"time"
)

type rendererResultCommand struct {
	sequence   CommandSequence
	zoneID     ZoneID
	playID     PlayID
	kind       string
	superseded bool
}

func (store *Store) RecordRendererTerminalResult(ctx context.Context, result RendererTerminalResult) error {
	if result.RendererID == "" || result.Epoch == "" || result.CommandID == "" ||
		result.ResultID == "" || result.RecordedAt.IsZero() || result.Status == "" || len(result.Status) > 256 {
		return ErrInvalidRequest
	}
	if len(result.Payload) == 0 {
		result.Payload = json.RawMessage("{}")
	}
	if result.PositionMS != nil && *result.PositionMS < 0 {
		return ErrInvalidRequest
	}
	if err := validateCommandPayload(result.Payload); err != nil {
		return err
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, result.RendererID, result.Epoch); err != nil {
			return err
		}
		command, err := rendererResultCommandIdentity(db, result)
		if err != nil {
			return err
		}
		duplicate, err := persistRendererResult(db, command, result)
		if err != nil {
			return err
		}
		if !duplicate {
			if err := terminalizeRendererCommand(db, result); err != nil {
				return err
			}
		}
		if result.Historical {
			return nil
		}
		if result.ObservedState != "" {
			if err := updateRendererObservation(db, RendererPlaybackEvent{
				RendererID: result.RendererID, Epoch: result.Epoch, PlayID: command.playID,
				Kind: PlaybackEventKind(result.ObservedState), PositionMS: result.PositionMS,
				ObservedAt: result.RecordedAt,
			}); err != nil {
				return err
			}
		}
		if command.kind == "play" && !command.superseded &&
			result.Status == "succeeded" && result.ObservedState == "playing" {
			return confirmStart(db, command.zoneID, command.playID)
		}
		if command.kind == "play" && !command.superseded && result.Status != "succeeded" {
			return suspendAssignedRenderer(db, result.RendererID)
		}
		if command.kind == "skip" && !command.superseded && !duplicate && result.Status == "succeeded" {
			commit := acknowledgedSkip{
				zoneID: command.zoneID, playID: command.playID, boundaryID: BoundaryID("skip:" + result.ResultID),
			}
			_, err := store.commitAcknowledgedSkip(db, commit)
			return err
		}
		return nil
	})
}

func (store *Store) AcknowledgeRendererResult(
	ctx context.Context,
	ack RendererResultAcknowledgement,
) error {
	if ack.RendererID == "" || ack.Epoch == "" || ack.ResultID == "" || ack.RecordedAt.IsZero() {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, ack.RendererID, ack.Epoch); err != nil {
			return err
		}
		stmt, err := db.prepare(`SELECT command_id FROM renderer_command_results
			WHERE renderer_id=? AND result_id=?`)
		if err != nil {
			return err
		}
		if err := stmt.bindText(1, string(ack.RendererID)); err != nil {
			stmt.close()
			return err
		}
		if err := stmt.bindText(2, ack.ResultID); err != nil {
			stmt.close()
			return err
		}
		row, err := stmt.step()
		if err != nil || !row {
			stmt.close()
			if err != nil {
				return err
			}
			return ErrInvalidObservation
		}
		commandID := stmt.text(0)
		stmt.close()
		acknowledgedAt := ack.RecordedAt.UTC().Format(time.RFC3339Nano)
		if err := execBound(db, `UPDATE renderer_command_results SET acknowledged_at=COALESCE(acknowledged_at,?)
			WHERE command_id=?`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, acknowledgedAt); err != nil {
				return err
			}
			return stmt.bindText(2, commandID)
		}); err != nil {
			return err
		}
		return execBound(db, `UPDATE renderer_outbox SET result_ack_at=COALESCE(result_ack_at,?)
			WHERE command_id=?`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, acknowledgedAt); err != nil {
				return err
			}
			return stmt.bindText(2, commandID)
		})
	})
}

func rendererResultCommandIdentity(db *sqliteDB, result RendererTerminalResult) (rendererResultCommand, error) {
	stmt, err := db.prepare(`SELECT sequence,zone_id,play_id,
		COALESCE(NULLIF(transport_kind,''),command_type),ack_status,superseded_at
		FROM renderer_outbox WHERE command_id=? AND renderer_id=?`)
	if err != nil {
		return rendererResultCommand{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, result.CommandID); err != nil {
		return rendererResultCommand{}, err
	}
	if err := stmt.bindText(2, string(result.RendererID)); err != nil {
		return rendererResultCommand{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return rendererResultCommand{}, err
	}
	if !row {
		return rendererResultCommand{}, ErrInvalidObservation
	}
	status := CommandAckStatus(stmt.text(4))
	if status != CommandAckReceived && status != CommandAckDuplicate {
		return rendererResultCommand{}, ErrInvalidObservation
	}
	return rendererResultCommand{
		sequence: CommandSequence(stmt.int64(0)), zoneID: ZoneID(stmt.text(1)),
		playID: PlayID(stmt.text(2)), kind: stmt.text(3), superseded: !stmt.isNull(5),
	}, nil
}

func persistRendererResult(db *sqliteDB, command rendererResultCommand, result RendererTerminalResult) (bool, error) {
	stmt, err := db.prepare(`SELECT result_id,
		CASE WHEN wire_status='' THEN outcome ELSE wire_status END,
		result_json,error_code,error_detail,observed_state,position_ms
		FROM renderer_command_results WHERE command_id=?`)
	if err != nil {
		return false, err
	}
	if err := stmt.bindText(1, result.CommandID); err != nil {
		stmt.close()
		return false, err
	}
	row, err := stmt.step()
	if err != nil {
		stmt.close()
		return false, err
	}
	if row {
		positionMatches := (result.PositionMS == nil && stmt.isNull(6)) ||
			(result.PositionMS != nil && !stmt.isNull(6) && stmt.int64(6) == *result.PositionMS)
		matches := stmt.text(0) == result.ResultID && stmt.text(1) == result.Status &&
			stmt.text(2) == string(result.Payload) && stmt.text(3) == result.ErrorCode &&
			stmt.text(4) == result.ErrorDetail && stmt.text(5) == result.ObservedState && positionMatches
		stmt.close()
		if matches {
			return true, nil
		}
		return false, ErrCommandResultConflict
	}
	stmt.close()
	used, err := rendererResultIDUsed(db, result.RendererID, result.ResultID)
	if err != nil {
		return false, err
	}
	if used {
		return false, ErrCommandResultConflict
	}
	return false, insertRendererResult(db, command, result)
}
