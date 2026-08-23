package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/jakestreamer/jstreamer-server/internal/security"
)

func bearer(request *http.Request) string {
	value := request.Header.Get("Authorization")
	if token, found := strings.CutPrefix(value, "Bearer "); found {
		return strings.TrimSpace(token)
	}
	for protocol := range strings.SplitSeq(request.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if token, found := strings.CutPrefix(strings.TrimSpace(protocol), "jstreamer.bearer."); found {
			return token
		}
	}
	return ""
}

func (service *server) authenticate(writer http.ResponseWriter, request *http.Request) (security.Device, bool) {
	device, err := service.config.Security.Authenticate(request.Context(), bearer(request))
	if err != nil {
		writeError(writer, err)
		return security.Device{}, false
	}
	return device, true
}

func (service *server) requireAdmin(writer http.ResponseWriter, request *http.Request) bool {
	device, ok := service.authenticate(writer, request)
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
