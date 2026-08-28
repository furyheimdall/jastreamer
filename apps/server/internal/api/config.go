package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

type configHandler struct {
	store     *settings.Store
	security  *security.Manager
	onChanged func(uint64)
	reconcile func(context.Context, []settings.CatalogRoot) error
}

func NewConfigHandler(store *settings.Store, manager *security.Manager) http.Handler {
	return newConfigHandler(store, manager, nil)
}

func newConfigHandler(store *settings.Store, manager *security.Manager, onChanged func(uint64)) *configHandler {
	return &configHandler{store: store, security: manager, onChanged: onChanged}
}

func (handler *configHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	device, err := handler.security.Authenticate(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return
	}
	if device.Role != security.RoleAdmin {
		writeError(writer, security.ErrForbidden)
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.get(writer)
	case http.MethodPatch:
		handler.patch(writer, request)
	default:
		writer.Header().Set("Allow", "GET, PATCH")
		invalid(writer, "METHOD_NOT_ALLOWED", "method is not allowed", http.StatusMethodNotAllowed)
	}
}

func (handler *configHandler) get(writer http.ResponseWriter) {
	writeConfigSnapshot(writer, handler.store.Snapshot())
}

func (handler *configHandler) patch(writer http.ResponseWriter, request *http.Request) {
	expected, ok := exactSettingsRevision(writer, request)
	if !ok {
		return
	}
	key := request.Header.Get("Idempotency-Key")
	if strings.TrimSpace(key) == "" {
		invalid(writer, "IDEMPOTENCY_KEY_REQUIRED", "non-empty Idempotency-Key header is required", http.StatusPreconditionRequired)
		return
	}
	if len(key) > 128 || strings.TrimSpace(key) != key {
		invalid(writer, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must contain at most 128 characters without surrounding whitespace", http.StatusBadRequest)
		return
	}
	update, ok := decodeSettingsUpdate(writer, request)
	if !ok {
		return
	}
	result, err := handler.store.PatchResult(request.Context(), settings.Mutation{
		ExpectedRevision: expected, IdempotencyKey: key, Update: update,
	})
	if err != nil {
		writeConfigError(writer, err)
		return
	}
	if result.Replayed {
		writeConfigSnapshot(writer, result.Snapshot)
		return
	}
	if update.CatalogRoots != nil && handler.reconcile != nil {
		authoritative := handler.store.Snapshot().Settings.CatalogRoots
		if err := handler.reconcile(request.Context(), authoritative); err != nil {
			writeConfigError(writer, err)
			return
		}
	}
	if handler.onChanged != nil {
		handler.onChanged(result.Snapshot.Revision)
	}
	writeConfigSnapshot(writer, result.Snapshot)
}

func exactSettingsRevision(writer http.ResponseWriter, request *http.Request) (uint64, bool) {
	raw := request.Header.Get("If-Match")
	if raw == "" {
		invalid(writer, "REVISION_REQUIRED", "If-Match header is required", http.StatusPreconditionRequired)
		return 0, false
	}
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		invalid(writer, "INVALID_REVISION", "If-Match must be a strong quoted decimal revision", http.StatusBadRequest)
		return 0, false
	}
	revision, err := strconv.ParseUint(raw[1:len(raw)-1], 10, 64)
	if err != nil || strconv.Quote(strconv.FormatUint(revision, 10)) != raw {
		invalid(writer, "INVALID_REVISION", "If-Match must be a strong quoted decimal revision", http.StatusBadRequest)
		return 0, false
	}
	return revision, true
}

func decodeSettingsUpdate(writer http.ResponseWriter, request *http.Request) (settings.Update, bool) {
	if mediaType := strings.ToLower(request.Header.Get("Content-Type")); !strings.HasPrefix(mediaType, "application/json") {
		invalid(writer, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return settings.Update{}, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		invalid(writer, "INVALID_REQUEST", "request body must be a JSON object", http.StatusBadRequest)
		return settings.Update{}, false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		invalid(writer, "INVALID_REQUEST", "request body must contain exactly one JSON object", http.StatusBadRequest)
		return settings.Update{}, false
	}
	update := settings.Update{}
	for name, raw := range fields {
		if string(raw) == "null" {
			invalidField(writer, name, "must not be null")
			return settings.Update{}, false
		}
		if !decodeConfigField(writer, name, raw, &update) {
			return settings.Update{}, false
		}
	}
	if len(fields) == 0 {
		invalid(writer, "INVALID_REQUEST", "settings patch must contain at least one field", http.StatusBadRequest)
		return settings.Update{}, false
	}
	return update, true
}

func decodeConfigField(writer http.ResponseWriter, name string, raw json.RawMessage, update *settings.Update) bool {
	var target any
	switch name {
	case "display_name":
		target = &update.DisplayName
	case "catalog_roots":
		target = &update.CatalogRoots
	case "control_origins":
		target = &update.ControlOrigins
	case "pairing_ttl_seconds":
		target = &update.PairingTTLSeconds
	case "upnp_interfaces":
		target = &update.UPnPInterfaces
	case "k17_http":
		target = &update.K17HTTP
	case "ffmpeg_path":
		target = &update.FFmpegPath
	case "listen_address", "certificate_fingerprint", "certificate_sans", "data_directory", "allowed_catalog_bases", "environment", "environment_locked_fields":
		invalid(writer, "CONFIG_FIELD_LOCKED", "configuration field is read-only", http.StatusConflict)
		return false
	case "setup_secret", "token", "tokens", "tls_private_key", "private_key":
		invalid(writer, "CONFIG_FIELD_FORBIDDEN", "security-sensitive configuration field is forbidden", http.StatusBadRequest)
		return false
	default:
		invalidField(writer, name, "is unknown")
		return false
	}
	if err := json.Unmarshal(raw, target); err != nil {
		invalidField(writer, name, "has the wrong JSON type")
		return false
	}
	return true
}

func invalidField(writer http.ResponseWriter, field, rule string) {
	writeJSON(writer, http.StatusBadRequest, map[string]any{
		"code": "CONFIG_VALIDATION_FAILED", "message": "configuration field is invalid", "field": field, "rule": rule,
	})
}

func writeConfigError(writer http.ResponseWriter, err error) {
	var validation *settings.ValidationError
	var locked *settings.LockedFieldError
	switch {
	case errors.Is(err, settings.ErrRevisionMismatch):
		invalid(writer, "STALE_CONFIG_REVISION", "configuration revision is stale", http.StatusPreconditionFailed)
	case errors.Is(err, settings.ErrIdempotencyConflict):
		invalid(writer, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was reused for a different patch", http.StatusConflict)
	case errors.As(err, &locked):
		writeJSON(writer, http.StatusConflict, map[string]any{"code": "CONFIG_FIELD_LOCKED", "message": "configuration field is locked", "field": locked.Field})
	case errors.As(err, &validation):
		writeJSON(writer, http.StatusBadRequest, map[string]any{"code": "CONFIG_VALIDATION_FAILED", "message": "configuration field is invalid", "field": validation.Field, "rule": validation.Rule})
	default:
		writeError(writer, err)
	}
}

func writeConfigSnapshot(writer http.ResponseWriter, snapshot settings.Snapshot) {
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatUint(snapshot.Revision, 10)))
	writeJSON(writer, http.StatusOK, snapshot)
}
