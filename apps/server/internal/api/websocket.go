package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (service *server) events(writer http.ResponseWriter, request *http.Request) {
	if origin := request.Header.Get("Origin"); origin != "" && !service.originAllowed(request, origin) {
		invalid(writer, "ORIGIN_FORBIDDEN", "WebSocket origin is not allowed", http.StatusForbidden)
		return
	}
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	if !headerContains(request.Header, "Connection", "upgrade") || !headerContains(request.Header, "Upgrade", "websocket") {
		invalid(writer, "WEBSOCKET_UPGRADE_REQUIRED", "WebSocket upgrade is required", http.StatusUpgradeRequired)
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
	defer connection.Close()
	events, unsubscribe := service.eventHub.subscribe()
	defer unsubscribe()
	digest := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(digest[:])
	protocolHeader := ""
	if protocol := websocketBearerProtocol(request); protocol != "" {
		protocolHeader = "Sec-WebSocket-Protocol: " + protocol + "\r\n"
	}
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", accept, protocolHeader); err != nil {
		return
	}
	initial, err := json.Marshal(map[string]any{"type": "state", "resource": "initial", "contract_revision": contractRevision, "catalog_revision": service.catalogSnapshot(request.Context()).Revision})
	if err != nil || writeTextFrame(buffered, initial) != nil || buffered.Flush() != nil {
		return
	}
	disconnected := make(chan struct{})
	go func() {
		var one [1]byte
		_, _ = connection.Read(one[:])
		close(disconnected)
	}()
	for {
		select {
		case payload := <-events:
			if writeTextFrame(buffered, payload) != nil || buffered.Flush() != nil {
				return
			}
		case <-disconnected:
			return
		case <-request.Context().Done():
			return
		}
	}
}

func (service *server) originAllowed(request *http.Request, origin string) bool {
	if origin == "https://"+request.Host || origin == "http://"+request.Host {
		return true
	}
	return slices.Contains(service.config.AllowedOrigins, origin)
}

func websocketKey(request *http.Request) (string, bool) {
	if request.Header.Get("Sec-WebSocket-Version") != "13" {
		return "", false
	}
	key := request.Header.Get("Sec-WebSocket-Key")
	decoded, err := base64.StdEncoding.DecodeString(key)
	return key, err == nil && len(decoded) == 16
}

func websocketBearerProtocol(request *http.Request) string {
	for protocol := range strings.SplitSeq(request.Header.Get("Sec-WebSocket-Protocol"), ",") {
		value := strings.TrimSpace(protocol)
		if strings.HasPrefix(value, "jastreamer.bearer.") {
			return value
		}
	}
	return ""
}

func headerContains(header http.Header, name, token string) bool {
	for part := range strings.SplitSeq(header.Get(name), ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func writeTextFrame(writer *bufio.ReadWriter, payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("state event exceeds initial frame limit")
	}
	if err := writer.WriteByte(0x81); err != nil {
		return err
	}
	if err := writer.WriteByte(byte(len(payload))); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}
