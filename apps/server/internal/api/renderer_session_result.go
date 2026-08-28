package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func (session *rendererSocketSession) acceptPendingResult(ctx context.Context, frame rendererResultFrame) error {
	command, err := session.handler.store.DurableCommand(ctx, frame.CommandID)
	if err != nil {
		return err
	}
	if command.RendererID != session.rendererID || command.Sequence <= 0 {
		return playback.ErrInvalidObservation
	}
	if err := session.handler.store.RecordRendererCommandAcknowledgement(ctx, playback.RendererCommandAcknowledgement{
		RendererID: session.rendererID, Epoch: session.epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: playback.CommandAckDuplicate, RecordedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	return session.acceptResult(ctx, frame, true)
}

func (session *rendererSocketSession) acceptResult(
	ctx context.Context,
	frame rendererResultFrame,
	historical bool,
) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	observedState := ""
	if frame.ObservedState != nil {
		observedState = *frame.ObservedState
	}
	errorCode, errorDetail := rendererResultError(frame.Error)
	result := playback.RendererTerminalResult{
		RendererID: session.rendererID, Epoch: session.epoch, CommandID: frame.CommandID,
		ResultID: frame.ResultID, Status: frame.Status, ObservedState: observedState,
		PositionMS: frame.PositionMS, Payload: payload, ErrorCode: errorCode,
		ErrorDetail: errorDetail, Historical: historical, RecordedAt: time.Now().UTC(),
	}
	if err := session.handler.store.RecordRendererTerminalResult(ctx, result); err != nil {
		return err
	}
	if err := session.handler.store.AcknowledgeRendererResult(ctx, playback.RendererResultAcknowledgement{
		RendererID: session.rendererID, Epoch: session.epoch,
		ResultID: frame.ResultID, RecordedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := session.writeJSON(rendererResultAckFrame{
		ProtocolMajor: rendererProtocolMajor, Type: "result.ack", ResultID: frame.ResultID,
	}); err != nil {
		return err
	}
	if session.inFlight == frame.CommandID {
		session.inFlight = ""
	}
	return nil
}

func rendererResultError(raw json.RawMessage) (string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var frame rendererErrorFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return "INVALID_RESULT_ERROR", "renderer returned an invalid error object"
	}
	return frame.Code, frame.Message
}
