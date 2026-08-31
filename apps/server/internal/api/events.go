package api

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const eventBufferSize uint64 = 8

type eventType string

const (
	eventTypeSnapshot       eventType = "snapshot"
	eventTypeInvalidation   eventType = "invalidation"
	eventTypeResyncRequired eventType = "resync_required"
)

type resourceRevision struct {
	Resource string `json:"resource"`
	ZoneID   string `json:"zone_id,omitempty"`
	Revision uint64 `json:"revision"`
}

type eventEnvelope struct {
	Type      eventType          `json:"type"`
	Epoch     uint64             `json:"server_epoch"`
	Sequence  uint64             `json:"sequence"`
	Resource  string             `json:"resource,omitempty"`
	ZoneID    string             `json:"zone_id,omitempty"`
	Revision  uint64             `json:"revision,omitempty"`
	Resources []resourceRevision `json:"resources,omitempty"`
}

func validateEventSequence(previous uint64, event eventEnvelope) error {
	if event.Sequence != previous+1 {
		return fmt.Errorf("event sequence gap: previous=%d received=%d", previous, event.Sequence)
	}
	return nil
}

func (service *server) publishState(resource string, revision any) {
	value, err := strconv.ParseUint(fmt.Sprint(revision), 10, 64)
	if err != nil {
		return
	}
	service.eventHub.publishInvalidation(resource, value)
}

func (service *server) publishZoneState(resource, zoneID string, revision any) {
	value, err := strconv.ParseUint(fmt.Sprint(revision), 10, 64)
	if err != nil {
		return
	}
	service.eventHub.publishScopedInvalidation(resource, zoneID, value)
}

func (service *server) events(writer http.ResponseWriter, request *http.Request) {
	if origin := request.Header.Get("Origin"); origin != "" && !service.originAllowed(request, origin) {
		invalid(writer, "ORIGIN_FORBIDDEN", "WebSocket origin is not allowed", http.StatusForbidden)
		return
	}
	device, authenticated := service.authenticateEventRequest(writer, request)
	if !authenticated {
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
	catalogRevision := service.catalogSnapshot(request.Context()).Revision
	service.eventHub.mu.Lock()
	if _, exists := service.eventHub.revisions["catalog"]; !exists {
		service.eventHub.revisions["catalog"] = catalogRevision
	}
	service.eventHub.mu.Unlock()
	subscription := service.eventHub.subscribe(device.ID)
	defer subscription.unsubscribe()
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	readerDone := make(chan struct{})
	defer func() {
		cancel()
		_ = connection.Close()
		<-readerDone
	}()
	digest := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(digest[:])
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		close(readerDone)
		return
	}
	active, writeErr := subscription.subscriber.writeIfActive(func() error {
		return service.writeEvent(buffered.Writer, subscription.snapshot)
	})
	if writeErr != nil {
		close(readerDone)
		return
	}
	if !active {
		_ = writeClose(buffered.Writer, eventRevocationCloseCode, eventRevocationCloseReason)
		close(readerDone)
		return
	}
	_ = connection.SetReadDeadline(time.Now().Add(websocketPongWait))
	stream := eventStream{
		service: service, writer: buffered.Writer, setReadDeadline: connection.SetReadDeadline,
		subscription: subscription, controls: make(chan eventControl, 4), readErrors: make(chan error, 1),
	}
	go stream.readClientFrames(ctx, buffered.Reader, readerDone)
	stream.run(ctx)
}
