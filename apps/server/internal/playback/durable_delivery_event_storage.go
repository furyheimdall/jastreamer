package playback

import (
	"encoding/json"
	"time"
)

type rendererEventIdentity struct {
	PlayID     PlayID            `json:"playId"`
	Kind       PlaybackEventKind `json:"kind"`
	PositionMS *int64            `json:"positionMs"`
	ObservedAt string            `json:"observedAt"`
}

func matchingRendererEvent(db *sqliteDB, event RendererPlaybackEvent) (bool, string, error) {
	stmt, err := db.prepare(`SELECT play_id,kind,position_ms,observed_at,decision_id
		FROM renderer_playback_events WHERE renderer_id=? AND event_id=?`)
	if err != nil {
		return false, "", err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(event.RendererID)); err != nil {
		return false, "", err
	}
	if err := stmt.bindText(2, event.EventID); err != nil {
		return false, "", err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return false, "", err
	}
	positionMatches := (event.PositionMS == nil && stmt.isNull(2)) ||
		(event.PositionMS != nil && !stmt.isNull(2) && stmt.int64(2) == *event.PositionMS)
	matches := stmt.text(0) == string(event.PlayID) && stmt.text(1) == string(event.Kind) &&
		positionMatches && stmt.text(3) == event.ObservedAt.UTC().Format(time.RFC3339Nano)
	if !matches {
		return false, "", ErrPlaybackEventConflict
	}
	return true, stmt.text(4), nil
}

func insertRendererEvent(db *sqliteDB, event RendererPlaybackEvent) error {
	identity := rendererEventIdentity{
		PlayID: event.PlayID, Kind: event.Kind, PositionMS: event.PositionMS,
		ObservedAt: event.ObservedAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	return execBound(db, `INSERT INTO renderer_playback_events(
		event_id,renderer_id,session_epoch,play_id,kind,position_ms,payload_json,observed_at,recorded_at
	) VALUES (?,?,?,?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		values := []string{
			event.EventID, string(event.RendererID), string(event.Epoch), string(event.PlayID),
			string(event.Kind), string(payload), identity.ObservedAt, identity.ObservedAt,
		}
		for index, value := range values[:5] {
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		if event.PositionMS == nil {
			if err := stmt.bind(6, nil); err != nil {
				return err
			}
		} else if err := stmt.bindInt64(6, *event.PositionMS); err != nil {
			return err
		}
		for index := 5; index < len(values); index++ {
			if err := stmt.bindText(index+2, values[index]); err != nil {
				return err
			}
		}
		return nil
	})
}

type rendererPlayDeliveryState struct {
	received bool
	terminal bool
}

func playCommandWasReceived(db *sqliteDB, rendererID RendererID, playID PlayID) (bool, error) {
	state, err := rendererPlayCommandState(db, rendererID, playID)
	return state.received, err
}

func playCommandIsTerminal(db *sqliteDB, rendererID RendererID, playID PlayID) (bool, error) {
	state, err := rendererPlayCommandState(db, rendererID, playID)
	return state.terminal, err
}

func rendererPlayCommandState(
	db *sqliteDB,
	rendererID RendererID,
	playID PlayID,
) (rendererPlayDeliveryState, error) {
	stmt, err := db.prepare(`SELECT ack_status,receipt_state,superseded_at FROM renderer_outbox
		WHERE renderer_id=? AND play_id=? AND command_type='play'`)
	if err != nil {
		return rendererPlayDeliveryState{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return rendererPlayDeliveryState{}, err
	}
	if err := stmt.bindText(2, string(playID)); err != nil {
		return rendererPlayDeliveryState{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return rendererPlayDeliveryState{}, err
	}
	if !row {
		return rendererPlayDeliveryState{}, ErrInvalidObservation
	}
	ack := CommandAckStatus(stmt.text(0))
	return rendererPlayDeliveryState{
		received: ack == CommandAckReceived || ack == CommandAckDuplicate,
		terminal: stmt.text(1) == string(CommandReceiptTerminal) && stmt.isNull(2),
	}, nil
}
