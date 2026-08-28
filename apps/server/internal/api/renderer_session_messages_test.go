package api_test

import (
	"encoding/json"
	"testing"
	"time"
)

func (socket *rendererSocket) sendCommandAck(t *testing.T, command rendererFrame, status string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		ProtocolMajor int             `json:"protocolMajor"`
		Type          string          `json:"type"`
		CommandID     string          `json:"commandId"`
		Sequence      int64           `json:"sequence"`
		Status        string          `json:"status"`
		Error         json.RawMessage `json:"error"`
	}{
		ProtocolMajor: 3, Type: "command.ack", CommandID: command.CommandID,
		Sequence: command.Sequence, Status: status, Error: json.RawMessage("null"),
	})
	if err != nil {
		t.Fatalf("encode command acknowledgement: %v", err)
	}
	socket.sendJSON(t, payload)
}

type rendererResultInput struct {
	command       rendererFrame
	resultID      string
	status        string
	observedState string
}

func (socket *rendererSocket) sendResult(t *testing.T, input rendererResultInput) rendererPendingResult {
	t.Helper()
	position := int64(0)
	result := rendererPendingResult{
		ProtocolMajor: 3, Type: "command.result", CommandID: input.command.CommandID,
		ResultID: input.resultID, Status: input.status,
		ObservedState: &input.observedState, PositionMS: &position,
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode command result: %v", err)
	}
	socket.sendJSON(t, payload)
	return result
}

type rendererEventInput struct {
	epoch   string
	eventID string
	playID  string
	kind    string
}

func (socket *rendererSocket) sendPlaybackEvent(t *testing.T, input rendererEventInput) {
	t.Helper()
	payload, err := json.Marshal(struct {
		ProtocolMajor int    `json:"protocolMajor"`
		Type          string `json:"type"`
		EventID       string `json:"eventId"`
		SessionEpoch  string `json:"sessionEpoch"`
		PlayID        string `json:"playId"`
		Kind          string `json:"kind"`
		ObservedAt    string `json:"observedAt"`
	}{
		ProtocolMajor: 3, Type: "playback.event", EventID: input.eventID,
		SessionEpoch: input.epoch, PlayID: input.playID, Kind: input.kind,
		ObservedAt: time.Date(2026, 8, 25, 12, 3, 0, 0, time.UTC).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("encode playback event: %v", err)
	}
	socket.sendJSON(t, payload)
}
