package api

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func (handler *RendererZoneAPI) serveRendererSession(writer http.ResponseWriter, request *http.Request) {
	if err := handler.ensureRendererRecovery(request.Context()); err != nil {
		writeError(writer, err)
		return
	}
	device, ok := handler.authenticateRole(writer, request, security.RoleRenderer)
	if !ok {
		return
	}
	rendererID := playback.RendererID(request.PathValue("rendererID"))
	if string(device.ID) != string(rendererID) {
		writeError(writer, security.ErrForbidden)
		return
	}
	if request.URL.RawQuery != "" {
		invalid(writer, "INVALID_REQUEST", "renderer session query parameters are forbidden", http.StatusBadRequest)
		return
	}
	if request.TLS == nil {
		invalid(writer, "WSS_REQUIRED", "renderer sessions require TLS", http.StatusUpgradeRequired)
		return
	}
	if !headerContains(request.Header, "Connection", "upgrade") ||
		!headerContains(request.Header, "Upgrade", "websocket") {
		invalid(writer, "WEBSOCKET_UPGRADE_REQUIRED", "WebSocket upgrade is required", http.StatusUpgradeRequired)
		return
	}
	if !rendererProtocolOffered(request.Header.Get("Sec-WebSocket-Protocol")) {
		writer.Header().Set("Sec-WebSocket-Protocol", rendererSessionSubprotocol)
		invalid(writer, "UNSUPPORTED_PROTOCOL_MAJOR", "renderer protocol v3 is required", http.StatusUpgradeRequired)
		return
	}
	key, valid := websocketKey(request)
	if !valid {
		invalid(writer, "INVALID_WEBSOCKET_HANDSHAKE", "WebSocket version 13 and a 16-byte key are required", http.StatusBadRequest)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		invalid(writer, "INTERNAL", "WebSocket transport is unavailable", http.StatusInternalServerError)
		return
	}
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	digest := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(digest[:])
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\nX-Jake-Supported-Protocol-Majors: 3,2\r\nX-Jake-Selected-Protocol-Major: 3\r\n\r\n", accept, rendererSessionSubprotocol); err != nil {
		_ = connection.Close()
		return
	}
	if err := buffered.Flush(); err != nil {
		_ = connection.Close()
		return
	}
	session := rendererSocketSession{
		handler: handler, rendererID: rendererID, connection: connection,
		reader: buffered.Reader, writer: buffered.Writer,
	}
	session.serve(request.Context())
}

func rendererProtocolOffered(header string) bool {
	protocols := strings.Split(header, ",")
	return len(protocols) == 1 && strings.TrimSpace(protocols[0]) == rendererSessionSubprotocol
}
