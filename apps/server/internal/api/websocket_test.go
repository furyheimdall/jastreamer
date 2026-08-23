package api_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestStateStream_upgrades_authenticated_TLS_connection_and_emits_state_event(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	server := httptest.NewTLSServer(value.handler)
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
	protocol := "jstreamer.bearer." + controller.Token
	_, err = fmt.Fprintf(connection, "GET /api/v1/events HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Protocol: %s\r\n\r\n", parsed.Host, protocol)
	if err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	reader := bufio.NewReader(connection)

	// When
	response, err := reader.ReadString('\n')

	// Then
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if response != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Fatalf("status line = %q", response)
	}
	foundProtocol := false
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read upgrade header: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
		if line == "Sec-WebSocket-Protocol: "+protocol+"\r\n" {
			foundProtocol = true
		}
	}
	if !foundProtocol {
		t.Fatal("upgrade did not authenticate and echo the bearer subprotocol")
	}
	initial := readServerFrame(t, reader)
	if initial["resource"] != "initial" {
		t.Fatalf("initial event = %#v", initial)
	}

	// When - subscription is established before the mutation.
	mutation := request(t, value.handler, http.MethodPost, "/api/v1/zones/stream/queue", controller.Token,
		`{"tracks":[{"track_id":"stream-track","available":true}]}`,
		map[string]string{"If-Match": "0", "Idempotency-Key": "stream"})
	postMutation := readServerFrame(t, reader)

	// Then
	if mutation.Code != http.StatusCreated || postMutation["resource"] != "queue" {
		t.Fatalf("mutation/event = %d / %#v", mutation.Code, postMutation)
	}
}

func readServerFrame(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if header[0] != 0x81 || header[1] > 125 {
		t.Fatalf("unexpected frame header: %x", header)
	}
	payload := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode event %q: %v", payload, err)
	}
	return event
}
