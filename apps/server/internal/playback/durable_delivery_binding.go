package playback

import (
	"encoding/json"
	"time"
)

type storedRendererCommandPayload struct {
	ZoneID     ZoneID          `json:"zoneId"`
	SessionID  SessionID       `json:"sessionId"`
	PlayID     PlayID          `json:"playId"`
	TrackID    TrackID         `json:"trackId"`
	Kind       string          `json:"kind"`
	PositionMS *int64          `json:"positionMs,omitempty"`
	Media      json.RawMessage `json:"media,omitempty"`
}

func bindRendererCommand(db *sqliteDB, commandID string, request RendererCommandRequest) error {
	stmt, err := db.prepare(`SELECT o.zone_id,o.play_id,COALESCE(NULLIF(o.transport_kind,''),o.command_type),
		COALESCE(p.session_id,''),COALESCE(p.track_id,'') FROM renderer_outbox o
		LEFT JOIN playback_plays p ON p.play_id=o.play_id WHERE o.command_id=?`)
	if err != nil {
		return err
	}
	if err := stmt.bindText(1, commandID); err != nil {
		stmt.close()
		return err
	}
	row, err := stmt.step()
	if err != nil || !row {
		stmt.close()
		return err
	}
	identity := storedRendererCommandPayload{
		ZoneID: ZoneID(stmt.text(0)), PlayID: PlayID(stmt.text(1)), Kind: stmt.text(2),
		SessionID: SessionID(stmt.text(3)), TrackID: TrackID(stmt.text(4)),
	}
	stmt.close()
	var persisted storedRendererCommandPayload
	payloadStmt, err := db.prepare("SELECT payload_json FROM renderer_outbox WHERE command_id=?")
	if err != nil {
		return err
	}
	if err := payloadStmt.bindText(1, commandID); err != nil {
		payloadStmt.close()
		return err
	}
	payloadRow, err := payloadStmt.step()
	if err == nil && payloadRow {
		err = json.Unmarshal([]byte(payloadStmt.text(0)), &persisted)
	}
	payloadStmt.close()
	if err != nil {
		return err
	}
	identity.PositionMS = persisted.PositionMS
	identity.Media = persisted.Media
	payload, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if err := validateCommandPayload(payload); err != nil {
		return err
	}
	_, next, _, err := rendererSessionCounters(db, request.RendererID)
	if err != nil {
		return err
	}
	createdAt := request.AttemptedAt.UTC().Format(time.RFC3339Nano)
	deadline := request.Deadline.UTC().Format(time.RFC3339Nano)
	if err := execBound(db, `UPDATE renderer_outbox SET renderer_id=?,sequence=?,session_id=?,
		payload_json=?,created_at=?,deadline=?,max_attempts=?
		WHERE command_id=? AND renderer_id=''`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(request.RendererID)); err != nil {
			return err
		}
		if err := stmt.bindInt64(2, int64(next)); err != nil {
			return err
		}
		values := []string{string(identity.SessionID), string(payload), createdAt, deadline}
		for index, value := range values {
			if err := stmt.bindText(index+3, value); err != nil {
				return err
			}
		}
		if err := stmt.bindInt64(7, MaxRendererCommandAttempts); err != nil {
			return err
		}
		return stmt.bindText(8, commandID)
	}); err != nil {
		return err
	}
	return execBound(db, `UPDATE renderer_session_state SET next_sequence=? WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(next+1)); err != nil {
			return err
		}
		return stmt.bindText(2, string(request.RendererID))
	})
}
