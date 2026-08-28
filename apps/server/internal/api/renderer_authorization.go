package api

import (
	"net/http"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func (handler *RendererZoneAPI) authenticateRole(
	writer http.ResponseWriter,
	request *http.Request,
	role security.Role,
) (security.Device, bool) {
	device, err := handler.security.Authenticate(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return security.Device{}, false
	}
	if device.Role == role {
		return device, true
	}
	writeError(writer, security.ErrForbidden)
	return security.Device{}, false
}

func (handler *RendererZoneAPI) authenticateControl(writer http.ResponseWriter, request *http.Request) bool {
	device, err := handler.security.Authenticate(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return false
	}
	if device.Role == security.RoleAdmin || device.Role == security.RoleController {
		return true
	}
	writeError(writer, security.ErrForbidden)
	return false
}

func (handler *RendererZoneAPI) AuthorizeRendererSession(writer http.ResponseWriter, request *http.Request) {
	handler.serveRendererSession(writer, request)
}

func (handler *RendererZoneAPI) AuthorizeRendererMedia(writer http.ResponseWriter, request *http.Request) {
	handler.authorizeRendererIdentity(writer, request)
}

func (handler *RendererZoneAPI) authorizeRendererIdentity(writer http.ResponseWriter, request *http.Request) {
	if err := handler.ensureRendererRecovery(request.Context()); err != nil {
		writeError(writer, err)
		return
	}
	device, ok := handler.authenticateRole(writer, request, security.RoleRenderer)
	if !ok {
		return
	}
	if string(device.ID) != request.PathValue("rendererID") {
		writeError(writer, security.ErrForbidden)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
