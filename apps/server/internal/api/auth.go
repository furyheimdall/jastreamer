package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func bearer(request *http.Request) string {
	value := request.Header.Get("Authorization")
	if token, found := strings.CutPrefix(value, "Bearer "); found {
		return strings.TrimSpace(token)
	}
	return ""
}

func (service *server) authenticate(writer http.ResponseWriter, request *http.Request) (security.Device, bool) {
	device, ok := service.authenticateAny(writer, request)
	if !ok {
		return security.Device{}, false
	}
	if device.Role != security.RoleAdmin && device.Role != security.RoleController {
		writeError(writer, security.ErrForbidden)
		return security.Device{}, false
	}
	return device, true
}

func (service *server) authenticateAny(writer http.ResponseWriter, request *http.Request) (security.Device, bool) {
	device, err := service.config.Security.Authenticate(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return security.Device{}, false
	}
	return device, true
}

func (service *server) requireAdmin(writer http.ResponseWriter, request *http.Request) bool {
	device, ok := service.authenticateAny(writer, request)
	if !ok {
		return false
	}
	if device.Role != security.RoleAdmin {
		writeError(writer, security.ErrForbidden)
		return false
	}
	return true
}

func requester(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}
