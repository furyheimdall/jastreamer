package player

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type playerHistoryRecord struct {
	key        string
	receivedAt time.Time
	rendererID string
	entryID    string
	playID     string
	commandID  string
	stage      string
	code       string
	message    string
	outcome    string
	positionMS *int64
	details    map[string]any
}

func (s *Service) commandHistoryRecord(command commandRecord, st storedState, entryID, message, outcome string, finished time.Time) *playerHistoryRecord {
	if s.history == nil || st.rendererID == "" || strings.HasPrefix(st.rendererID, "browser:") || !recordablePlayerDiagnostic(command.errorInfo) {
		return nil
	}
	stage := command.errorInfo.action
	if stage == "" {
		stage = command.action
	}
	code := command.errorInfo.faultCode
	if code == "" {
		code = command.errorInfo.category
		if command.errorInfo.code != 0 {
			code += ":" + strconv.Itoa(command.errorInfo.code)
		}
	}
	position := st.positionMS
	return &playerHistoryRecord{
		key: "player-command:" + command.id, receivedAt: finished, rendererID: st.rendererID, entryID: entryID,
		playID: st.playID, commandID: command.id, stage: stage, code: code, message: message, outcome: outcome,
		positionMS: &position,
		details: map[string]any{
			"category": command.errorInfo.category, "renderer_action": command.errorInfo.action,
			"renderer_code": command.errorInfo.code, "http_status": command.errorInfo.httpStatus,
			"fault_code": command.errorInfo.faultCode,
		},
	}
}

func recordablePlayerDiagnostic(info diagnosticErrorInfo) bool {
	switch info.category {
	case string(output.ErrorUnsupported), string(output.ErrorTimeout), string(output.ErrorCancelled), string(output.ErrorTransport),
		string(output.ErrorResponse), string(output.ErrorFault), string(output.ErrorMedia), "offline", "recovery_unconfirmed", "unconfirmed", "panic":
		return true
	default:
		return false
	}
}

func (s *Service) recordPlayerHistory(record *playerHistoryRecord) {
	if record == nil || s.history == nil {
		return
	}
	device, found := s.devices.Device(record.rendererID)
	if found && device.Protocol == output.ProtocolBrowser {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	event := errorhistory.Event{
		Key: record.key, ReceivedAt: record.receivedAt.UTC(), Kind: "renderer", RendererID: record.rendererID,
		PlayID: record.playID, CommandID: record.commandID, Stage: record.stage, Code: record.code,
		Message: record.message, Outcome: record.outcome, PositionMS: record.positionMS,
	}
	if found {
		event.RendererName = device.Name
		event.Protocol = device.Protocol
	}
	if record.entryID != "" {
		var trackID string
		if err := s.db.QueryRowContext(ctx, "SELECT track_id FROM player_queue WHERE entry_id=?", record.entryID).Scan(&trackID); err == nil {
			event.TrackID = trackID
			if track, trackErr := s.lib.Track(ctx, trackID); trackErr == nil {
				event.TrackTitle = track.Title
				event.RelativePath = track.Path
				if track.RootID != "" {
					_ = s.db.QueryRowContext(ctx, "SELECT name FROM library_roots WHERE id=?", track.RootID).Scan(&event.RootName)
				}
			}
		}
	}
	details, err := json.Marshal(record.details)
	if err != nil {
		details = []byte(`{}`)
	}
	event.Details = details
	if err := s.history.Record(ctx, event); err != nil {
		log.Printf("diagnostic component=player event=history_record_failed renderer_id=%q command_id=%q error_type=%T", event.RendererID, event.CommandID, err)
	}
}

func observationHistoryKey(rendererID, playID, stage string, warningID int64, outcome string) string {
	return fmt.Sprintf("player-observation:%s:%s:%s:%d:%s", rendererID, playID, stage, warningID, outcome)
}
