package playback

import (
	"context"
	"encoding/json"
)

type TransportMedia struct {
	URL      string
	MimeType string
}

func (store *Store) AttachTransportMedia(ctx context.Context, commandID string, media TransportMedia) error {
	if commandID == "" || media.URL == "" || media.MimeType == "" {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT payload_json,renderer_id,
			COALESCE(NULLIF(transport_kind,''),command_type) FROM renderer_outbox WHERE command_id=?`)
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
			if err != nil {
				return err
			}
			return ErrInvalidObservation
		}
		var payload storedRendererCommandPayload
		err = json.Unmarshal([]byte(stmt.text(0)), &payload)
		bound := stmt.text(1) != ""
		kind := stmt.text(2)
		stmt.close()
		if err != nil || bound || kind != "play" {
			return ErrCommandDeliveryConflict
		}
		payload.Media, err = json.Marshal(struct {
			URL      string            `json:"url"`
			Headers  map[string]string `json:"headers"`
			MimeType string            `json:"mimeType"`
		}{URL: media.URL, Headers: map[string]string{}, MimeType: media.MimeType})
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return execBound(db, "UPDATE renderer_outbox SET payload_json=?,media_ready=1 WHERE command_id=? AND renderer_id=''", func(update *sqliteStmt) error {
			if err := update.bindText(1, string(encoded)); err != nil {
				return err
			}
			return update.bindText(2, commandID)
		})
	})
}

func (store *Store) MarkTransportMediaReady(ctx context.Context, commandID string) error {
	if commandID == "" {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		return execBound(db, "UPDATE renderer_outbox SET media_ready=1 WHERE command_id=? AND renderer_id=''", func(stmt *sqliteStmt) error {
			return stmt.bindText(1, commandID)
		})
	})
}
