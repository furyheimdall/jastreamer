package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jakestreamer/jstreamer-server/internal/playback"
	"github.com/jakestreamer/jstreamer-server/internal/security"
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
	case errors.Is(err, security.ErrInvalidRegistration):
		return apiError{http.StatusBadRequest, "INVALID_REQUEST", "device name is invalid"}
	case errors.Is(err, playback.ErrRevisionConflict):
		return apiError{http.StatusConflict, "STALE_REVISION", "queue revision is stale"}
	case errors.Is(err, playback.ErrIdempotencyConflict):
		return apiError{http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key was reused for a different request"}
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
