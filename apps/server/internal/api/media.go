package api

import (
	"net/http"

	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func (service *server) rendererMedia(writer http.ResponseWriter, request *http.Request) {
	device, ok := service.rendererRoutes.authenticateRole(writer, request, security.RoleRenderer)
	if !ok {
		return
	}
	rendererID := playback.RendererID(request.PathValue("rendererID"))
	if playback.RendererID(device.ID) != rendererID {
		writeError(writer, security.ErrForbidden)
		return
	}
	service.config.Media.Handler(media.AudienceCustomRenderer, rendererID).ServeHTTP(writer, request)
}

func MediaOnly(service *media.Service) http.Handler {
	if service == nil {
		return http.NotFoundHandler()
	}
	return media.MediaOnlyHandler(service.K17Handler())
}
