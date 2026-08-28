package api_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type liveEventSocket struct {
	connection net.Conn
	reader     *bufio.Reader
}

func openEventSocket(t *testing.T, handler http.Handler, token string) *liveEventSocket {
	t.Helper()
	ticketResponse := request(t, handler, http.MethodPost, "/api/v1/event-tickets", token, "", nil)
	var ticket struct {
		Value string `json:"ticket"`
	}
	if err := json.Unmarshal(ticketResponse.Body.Bytes(), &ticket); err != nil || ticketResponse.Code != http.StatusCreated {
		t.Fatalf("issue event ticket: %d %s (%v)", ticketResponse.Code, ticketResponse.Body.String(), err)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	connection, err := tls.Dial("tcp", parsed.Host, &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("dial TLS server: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if _, err := fmt.Fprintf(connection, "GET /api/v1/events?ticket=%s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", ticket.Value, parsed.Host); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil || status != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Fatalf("upgrade status = %q (%v)", status, err)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read upgrade header: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-protocol:") {
			t.Fatalf("upgrade leaked credential in subprotocol: %q", line)
		}
	}
	socket := &liveEventSocket{connection: connection, reader: reader}
	initial := socket.readEvent(t)
	if initial["type"] != "snapshot" || initial["server_epoch"] == nil || initial["sequence"] == nil {
		t.Fatalf("initial event = %#v", initial)
	}
	return socket
}

func (socket *liveEventSocket) readFrame(t *testing.T) (byte, []byte) {
	t.Helper()
	if err := socket.connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set WebSocket deadline: %v", err)
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(socket.reader, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[0]&0x80 == 0 || header[1]&0x80 != 0 || header[1] > 125 {
		t.Fatalf("unexpected frame header: %x", header)
	}
	payload := make([]byte, int(header[1]))
	if _, err := io.ReadFull(socket.reader, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	return header[0] & 0x0f, payload
}

func (socket *liveEventSocket) readEvent(t *testing.T) map[string]any {
	t.Helper()
	opcode, payload := socket.readFrame(t)
	if opcode != 0x1 {
		t.Fatalf("event opcode = %#x, payload = %x", opcode, payload)
	}
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode event %q: %v", payload, err)
	}
	return event
}

func (socket *liveEventSocket) requireClose(t *testing.T, expectedCode uint16, expectedReason string) {
	t.Helper()
	opcode, payload := socket.readFrame(t)
	if opcode != 0x8 || len(payload) < 2 {
		t.Fatalf("close frame = opcode %#x payload %x", opcode, payload)
	}
	if code, reason := binary.BigEndian.Uint16(payload[:2]), string(payload[2:]); code != expectedCode || reason != expectedReason {
		t.Fatalf("close = %d %q, want %d %q", code, reason, expectedCode, expectedReason)
	}
}

func TestEventStream_upgrades_authenticated_TLS_connection_and_emits_invalidation(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	socket := openEventSocket(t, value.handler, controller.Token)

	// When
	mutation := request(t, value.handler, http.MethodPost, "/api/v1/zones/stream/queue", controller.Token,
		`{"tracks":[{"track_id":"stream-track","available":true}]}`,
		map[string]string{"If-Match": "0", "Idempotency-Key": "stream"})
	postMutation := socket.readEvent(t)

	// Then
	if mutation.Code != http.StatusCreated || postMutation["type"] != "invalidation" || postMutation["resource"] != "queue" || postMutation["revision"] != float64(1) {
		t.Fatalf("mutation/event = %d / %#v", mutation.Code, postMutation)
	}
}

func TestEventStream_closes_revoked_device_without_interrupting_another_device(t *testing.T) {
	// Given
	value := newFixture(t)
	revocationAdmin := pairRole(t, value, security.RoleAdmin, "Revocation Admin")
	revokedController := pairController(t, value)
	activeController := pairController(t, value)
	revokedSocket := openEventSocket(t, value.handler, revokedController.Token)
	activeSocket := openEventSocket(t, value.handler, activeController.Token)

	// When
	revocation := request(t, value.handler, http.MethodDelete, "/api/v1/devices/"+string(revokedController.Device.ID), revocationAdmin.Token, "", nil)
	revokedSocket.requireClose(t, 1008, "device revoked")
	mutation := request(t, value.handler, http.MethodPost, "/api/v1/zones/revocation/queue", activeController.Token,
		`{"tracks":[{"track_id":"after-revocation","available":true}]}`,
		map[string]string{"If-Match": "0", "Idempotency-Key": "after-revocation"})
	activeEvent := activeSocket.readEvent(t)

	// Then
	if revocation.Code != http.StatusNoContent || mutation.Code != http.StatusCreated {
		t.Fatalf("revoke/mutation statuses = %d/%d", revocation.Code, mutation.Code)
	}
	if activeEvent["type"] != "invalidation" || activeEvent["resource"] != "queue" || activeEvent["revision"] != float64(1) {
		t.Fatalf("unrelated device event = %#v", activeEvent)
	}
	if err := revokedSocket.connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set closed-socket deadline: %v", err)
	}
	if _, err := revokedSocket.reader.ReadByte(); err == nil {
		t.Fatal("revoked socket received bytes after its close frame")
	}
}

func TestEventStream_shutdown_closes_socket_and_restart_rebinds_revocation(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	firstContext, stopFirst := context.WithCancel(context.Background())
	firstHandler := api.New(api.Config{Security: value.manager, Queue: value.store, Context: firstContext})
	firstSocket := openEventSocket(t, firstHandler, controller.Token)

	// When
	stopFirst()
	firstSocket.requireClose(t, 1001, "server shutting down")
	secondContext, stopSecond := context.WithCancel(context.Background())
	t.Cleanup(stopSecond)
	secondHandler := api.New(api.Config{Security: value.manager, Queue: value.store, Context: secondContext})
	secondSocket := openEventSocket(t, secondHandler, controller.Token)
	revocation := request(t, secondHandler, http.MethodDelete, "/api/v1/devices/"+string(controller.Device.ID), value.admin.Token, "", nil)

	// Then
	if revocation.Code != http.StatusNoContent {
		t.Fatalf("revocation after restart = %d %s", revocation.Code, revocation.Body.String())
	}
	secondSocket.requireClose(t, 1008, "device revoked")
}
