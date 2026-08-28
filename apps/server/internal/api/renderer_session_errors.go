package api

import (
	"encoding/json"
	"errors"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func (session *rendererSocketSession) writeProtocolFailure(err error) {
	protocol := rendererErrorFor(err)
	payload, marshalErr := json.Marshal(rendererErrorFrame{
		ProtocolMajor: rendererProtocolMajor, Type: "error", Code: protocol.code,
		Message: protocol.message, Retryable: protocol.retryable, Details: json.RawMessage("{}"),
	})
	closing := rendererSessionCloseSignal{code: closePolicyViolation, reason: rendererProtocolCloseReason}
	if marshalErr != nil {
		_ = session.terminate(closing, nil)
		return
	}
	_ = session.terminate(closing, func() error { return session.writeJSONPayload(payload) })
}

func rendererErrorFor(err error) *rendererProtocolError {
	var protocol *rendererProtocolError
	if errors.As(err, &protocol) {
		return protocol
	}
	switch {
	case errors.Is(err, playback.ErrStaleRendererEpoch):
		return protocolError("STALE_SESSION_EPOCH", "renderer session epoch is stale", false)
	case errors.Is(err, playback.ErrCommandSequenceGap):
		return protocolError("COMMAND_SEQUENCE_GAP", "renderer cursor is ahead of Server state", false)
	case errors.Is(err, playback.ErrCommandDeliveryConflict),
		errors.Is(err, playback.ErrCommandResultConflict),
		errors.Is(err, playback.ErrPlaybackEventConflict):
		return protocolError("COMMAND_ID_CONFLICT", "durable message identity conflicts", false)
	case errors.Is(err, playback.ErrCommandExpired):
		return protocolError("COMMAND_EXPIRED", "renderer command deadline expired", false)
	case errors.Is(err, playback.ErrCommandRetryExhausted):
		return protocolError("INTERNAL", "renderer command retry budget exhausted", false)
	case errors.Is(err, security.ErrTokenRevoked):
		return protocolError("TOKEN_REVOKED", "renderer credential is revoked", false)
	case errors.Is(err, playback.ErrInvalidRequest), errors.Is(err, playback.ErrInvalidObservation),
		errors.Is(err, playback.ErrSensitivePayload):
		return protocolError("INVALID_MESSAGE", "renderer message contradicts durable state", false)
	default:
		return protocolError("INTERNAL", "renderer session failed", true)
	}
}
