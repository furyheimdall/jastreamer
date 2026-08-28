package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func decodeRendererHello(payload []byte, rendererID playback.RendererID) (rendererHelloFrame, error) {
	hello, err := decodeRendererJSON[rendererHelloFrame](payload)
	if err != nil {
		return rendererHelloFrame{}, protocolError("INVALID_MESSAGE", "hello is not valid protocol JSON", false)
	}
	if hello.ProtocolMajor != rendererProtocolMajor || hello.Type != "hello" ||
		hello.RendererID != string(rendererID) || !slices.Contains(hello.SupportedMajors, rendererProtocolMajor) ||
		hello.LastServerSequence < 0 || len(hello.PendingResults) > maxRendererPendingResults ||
		!validRendererCapabilities(hello.Capabilities) {
		return rendererHelloFrame{}, protocolError("INVALID_MESSAGE", "hello fields are invalid", false)
	}
	for _, result := range hello.PendingResults {
		if err := validateRendererResult(result); err != nil {
			return rendererHelloFrame{}, err
		}
	}
	return hello, nil
}

func decodeRendererInbound(payload []byte) (rendererInbound, error) {
	var envelope struct {
		ProtocolMajor int    `json:"protocolMajor"`
		Type          string `json:"type"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.ProtocolMajor != rendererProtocolMajor {
		return nil, protocolError("INVALID_MESSAGE", "message envelope is invalid", false)
	}
	switch envelope.Type {
	case "command.ack":
		frame, err := decodeRendererJSON[rendererAckFrame](payload)
		if err != nil || !validRendererAck(frame) {
			return nil, protocolError("INVALID_MESSAGE", "command acknowledgement is invalid", false)
		}
		return rendererAckMessage{frame: frame}, nil
	case "command.result":
		frame, err := decodeRendererJSON[rendererResultFrame](payload)
		if err != nil {
			return nil, protocolError("INVALID_MESSAGE", "command result is invalid", false)
		}
		if err := validateRendererResult(frame); err != nil {
			return nil, err
		}
		return rendererResultMessage{frame: frame}, nil
	case "playback.event":
		frame, err := decodeRendererJSON[rendererPlaybackEventFrame](payload)
		if err != nil || !validRendererPlaybackEvent(frame) {
			return nil, protocolError("INVALID_MESSAGE", "playback event is invalid", false)
		}
		return rendererPlaybackEventMessage{frame: frame}, nil
	default:
		return nil, protocolError("INVALID_MESSAGE", "message type is unsupported", false)
	}
}

func decodeRendererJSON[T any](payload []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("renderer JSON contains trailing data")
	}
	return value, nil
}

func validRendererCapabilities(value rendererCapabilitiesFrame) bool {
	if len(value.Commands) == 0 || len(value.Commands) > maxRendererCapabilities ||
		len(value.MediaTypes) == 0 || len(value.MediaTypes) > maxRendererCapabilities ||
		value.MaxChannels <= 0 || value.MaxSampleRateHz <= 0 {
		return false
	}
	return uniqueRendererValues(value.Commands) && uniqueRendererValues(value.MediaTypes)
}

func uniqueRendererValues(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > 256 {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validRendererAck(frame rendererAckFrame) bool {
	validStatus := frame.Status == string(playback.CommandAckReceived) ||
		frame.Status == string(playback.CommandAckDuplicate) || frame.Status == string(playback.CommandAckRejected)
	return frame.ProtocolMajor == rendererProtocolMajor && frame.Type == "command.ack" &&
		validRendererID(frame.CommandID) && frame.Sequence > 0 && validStatus &&
		(len(frame.Error) == 0 || json.Valid(frame.Error))
}

func validateRendererResult(frame rendererResultFrame) error {
	if frame.ProtocolMajor != rendererProtocolMajor || frame.Type != "command.result" ||
		!validRendererID(frame.CommandID) || !validRendererID(frame.ResultID) || frame.Status == "" ||
		(frame.PositionMS != nil && *frame.PositionMS < 0) || (len(frame.Error) != 0 && !json.Valid(frame.Error)) {
		return protocolError("INVALID_MESSAGE", "command result fields are invalid", false)
	}
	return nil
}

func validRendererPlaybackEvent(frame rendererPlaybackEventFrame) bool {
	if frame.ProtocolMajor != rendererProtocolMajor || frame.Type != "playback.event" ||
		!validRendererID(frame.EventID) || !validRendererID(frame.SessionEpoch) ||
		!validRendererID(frame.PlayID) || frame.Kind == "" || len(frame.Kind) > 256 ||
		(frame.PositionMS != nil && *frame.PositionMS < 0) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, frame.ObservedAt)
	return err == nil
}

func validRendererID(value string) bool { return value != "" && len(value) <= 256 }

func protocolError(code, message string, retryable bool) *rendererProtocolError {
	return &rendererProtocolError{code: code, message: message, retryable: retryable}
}
