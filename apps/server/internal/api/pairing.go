package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/playback"
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
	var body struct {
		Role security.Role `json:"role"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	if body.Role == "" {
		body.Role = security.RoleController
	}
	code, err := service.config.Security.GeneratePairingCodeForRole(request.Context(), bearer(request), body.Role)
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
	var credential security.Credential
	var err error
	if service.rendererRoutes != nil {
		credential, err = service.rendererRoutes.pairCredential(request.Context(), rendererPairRequest{
			Code: body.Code, Registration: security.Registration{Name: body.Name}, Requester: requester(request),
		})
	} else {
		credential, err = service.config.Security.Pair(request.Context(), body.Code, security.Registration{Name: body.Name}, requester(request))
		if err == nil && credential.Device.Role == security.RoleRenderer {
			err = errors.Join(
				security.ErrRendererStoreUnavailable,
				service.config.Security.AbortRendererPair(request.Context(), credential.Device.ID),
			)
		}
	}
	if err != nil {
		writeError(writer, err)
		return
	}
	if credential.Device.Role == security.RoleRenderer && service.rendererRoutes != nil {
		if err := service.rendererRoutes.beforePairResponse(); err != nil {
			writeError(writer, err)
			return
		}
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
	id := security.DeviceID(request.PathValue("deviceID"))
	device, err := service.config.Security.Device(request.Context(), bearer(request), id)
	if err != nil {
		writeError(writer, err)
		return
	}
	if err := service.config.Security.Revoke(request.Context(), bearer(request), id); err != nil {
		writeError(writer, err)
		return
	}
	if device.Role == security.RoleRenderer {
		if err := service.config.Queue.RevokeRenderer(request.Context(), playback.RendererID(device.ID)); err != nil {
			writeError(writer, err)
			return
		}
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
