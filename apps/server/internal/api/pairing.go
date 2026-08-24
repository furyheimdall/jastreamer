package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func (service *server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (service *server) identity(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"common_name": "jastreamer Server", "sha256_fingerprint": service.config.CertificateFingerprint,
		"pairing_url": "/pair/",
	})
}

func (service *server) bootstrap(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		SetupSecret string `json:"setup_secret"`
		Name        string `json:"name"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	credential, err := service.config.Security.Bootstrap(request.Context(), body.SetupSecret, security.Registration{Name: body.Name})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, credential)
}

func (service *server) pairingCode(writer http.ResponseWriter, request *http.Request) {
	if !service.requireAdmin(writer, request) {
		return
	}
	code, err := service.config.Security.GeneratePairingCode(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, code)
}

func (service *server) pair(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	credential, err := service.config.Security.Pair(request.Context(), body.Code, security.Registration{Name: body.Name}, requester(request))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, credential)
}

func (service *server) devices(writer http.ResponseWriter, request *http.Request) {
	if !service.requireAdmin(writer, request) {
		return
	}
	devices, err := service.config.Security.Devices(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"devices": devices})
}

func (service *server) revoke(writer http.ResponseWriter, request *http.Request) {
	if !service.requireAdmin(writer, request) {
		return
	}
	err := service.config.Security.Revoke(request.Context(), bearer(request), security.DeviceID(request.PathValue("deviceID")))
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	if mediaType := request.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(mediaType), "application/json") {
		invalid(writer, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		invalid(writer, "INVALID_REQUEST", "request body is invalid", http.StatusBadRequest)
		return false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		invalid(writer, "INVALID_REQUEST", "request body must contain exactly one JSON value", http.StatusBadRequest)
		return false
	}
	return true
}
