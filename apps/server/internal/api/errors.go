package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type apiError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(writer http.ResponseWriter, err error) {
	value := mapError(err)
	writeJSON(writer, value.Status, value)
}

func mapError(err error) apiError {
	switch {
	case errors.Is(err, security.ErrBootstrapComplete):
		return apiError{http.StatusConflict, "BOOTSTRAP_COMPLETE", "first administrator already exists"}
	case errors.Is(err, security.ErrBootstrapSecret):
		return apiError{http.StatusUnauthorized, "BOOTSTRAP_SECRET_INVALID", "setup secret is invalid"}
	case errors.Is(err, security.ErrTokenRevoked):
		return apiError{http.StatusUnauthorized, "TOKEN_REVOKED", "device token has been revoked"}
	case errors.Is(err, security.ErrUnauthorized):
		return apiError{http.StatusUnauthorized, "UNAUTHORIZED", "device authentication is required"}
	case errors.Is(err, security.ErrForbidden):
		return apiError{http.StatusForbidden, "ADMIN_REQUIRED", "administrator role is required"}
	case errors.Is(err, security.ErrPairingCodeInvalid):
		return apiError{http.StatusBadRequest, "PAIRING_CODE_INVALID", "pairing code is invalid"}
	case errors.Is(err, security.ErrPairingCodeExpired):
		return apiError{http.StatusGone, "PAIRING_CODE_EXPIRED", "pairing code has expired"}
	case errors.Is(err, security.ErrPairingCodeUsed):
		return apiError{http.StatusConflict, "PAIRING_CODE_USED", "pairing code was already consumed"}
	case errors.Is(err, security.ErrRateLimited):
		return apiError{http.StatusTooManyRequests, "PAIRING_RATE_LIMITED", "too many invalid pairing attempts"}
	case errors.Is(err, security.ErrDeviceNotFound):
		return apiError{http.StatusNotFound, "NOT_FOUND", "device was not found"}
	case errors.Is(err, security.ErrInvalidRegistration), errors.Is(err, security.ErrInvalidRole):
		return apiError{http.StatusBadRequest, "INVALID_REQUEST", "device registration is invalid"}
	case errors.Is(err, security.ErrLastAdmin):
		return apiError{http.StatusConflict, "LAST_ADMIN", "the last active administrator cannot be revoked"}
	case errors.Is(err, security.ErrRendererOperationPending), errors.Is(err, security.ErrRendererStoreUnavailable):
		return apiError{http.StatusServiceUnavailable, "RENDERER_OPERATION_PENDING", "renderer operation is pending recovery"}
	case errors.Is(err, playback.ErrZoneActive):
		return apiError{http.StatusConflict, "ZONE_ACTIVE", "an active zone cannot be reassigned"}
	case errors.Is(err, playback.ErrRendererAssigned):
		return apiError{http.StatusConflict, "RENDERER_ASSIGNED", "renderer is already assigned to another zone"}
	case errors.Is(err, playback.ErrRendererNotFound), errors.Is(err, playback.ErrZoneNotFound):
		return apiError{http.StatusNotFound, "NOT_FOUND", "renderer or zone was not found"}
	case errors.Is(err, playback.ErrInvalidRenderer), errors.Is(err, playback.ErrInvalidZone):
		return apiError{http.StatusBadRequest, "INVALID_REQUEST", "renderer or zone is invalid"}
	case errors.Is(err, playback.ErrRevisionConflict):
		return apiError{http.StatusConflict, "STALE_REVISION", "queue revision is stale"}
	case errors.Is(err, playback.ErrIdempotencyConflict):
		return apiError{http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key was reused for a different request"}
	case errors.Is(err, playback.ErrQueueEntryNotFound):
		return apiError{http.StatusNotFound, "QUEUE_ENTRY_NOT_FOUND", "queue entry was not found"}
	case errors.Is(err, playback.ErrQueueEntryActive):
		return apiError{http.StatusConflict, "QUEUE_ENTRY_ACTIVE", "active queue entry cannot be changed"}
	case errors.Is(err, playback.ErrQueueHeadState), errors.Is(err, playback.ErrInvalidTransition):
		return apiError{http.StatusConflict, "INVALID_STATE", "operation is not valid in the current state"}
	case errors.Is(err, playback.ErrRendererOffline), errors.Is(err, playback.ErrRendererRequired):
		return apiError{http.StatusConflict, "RENDERER_OFFLINE", "an assigned online renderer is required"}
	case errors.Is(err, playback.ErrUnsupportedCapability):
		return apiError{http.StatusConflict, "UNSUPPORTED_CAPABILITY", "renderer does not support this command"}
	case errors.Is(err, playback.ErrQueueEmpty):
		return apiError{http.StatusConflict, "QUEUE_EMPTY", "queue is empty"}
	case errors.Is(err, playback.ErrPlaybackHistoryEmpty):
		return apiError{http.StatusConflict, "PLAYBACK_HISTORY_EMPTY", "no prior playback history is available"}
	case errors.Is(err, playback.ErrQueueBlocked):
		return apiError{http.StatusConflict, "BLOCKED_EXPLICIT_HEAD", "explicit queue head is unavailable"}
	case errors.Is(err, playback.ErrInvalidRequest), errors.Is(err, playback.ErrQueueLimit):
		return apiError{http.StatusBadRequest, "INVALID_REQUEST", "queue mutation is invalid"}
	default:
		return apiError{http.StatusInternalServerError, "INTERNAL", "internal server error"}
	}
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(body); err != nil {
		return
	}
}

func invalid(writer http.ResponseWriter, code, message string, status int) {
	writeJSON(writer, status, apiError{Status: status, Code: code, Message: message})
}
