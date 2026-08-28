package main

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type liveCredential struct {
	Token  string `json:"token"`
	Device struct {
		ID string `json:"id"`
	} `json:"device"`
}

type liveEventSnapshot struct {
	Type  string `json:"type"`
	Epoch uint64 `json:"server_epoch"`
}

func (server liveServer) eventSnapshot(t *testing.T, token string) (liveEventSnapshot, string) {
	t.Helper()
	response, payload := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/event-tickets", token: token})
	var ticket struct {
		Value string `json:"ticket"`
	}
	if err := json.Unmarshal(payload, &ticket); err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("event ticket = %d %s (%v)", response.StatusCode, payload, err)
	}
	parsed, err := url.Parse(server.url)
	if err != nil {
		t.Fatalf("parse live URL: %v", err)
	}
	connection, err := tls.Dial("tcp", parsed.Host, &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial event socket: %v", err)
	}
	defer connection.Close()
	if _, err := fmt.Fprintf(connection, "GET /api/v1/events?ticket=%s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", url.QueryEscape(ticket.Value), parsed.Host); err != nil {
		t.Fatalf("write event upgrade: %v", err)
	}
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil || status != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Fatalf("event upgrade = %q (%v)", status, err)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read event headers: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 0x81 {
		t.Fatalf("read event frame header: %x (%v)", header, err)
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		var encoded [2]byte
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			t.Fatalf("read event frame length: %v", err)
		}
		length = uint64(binary.BigEndian.Uint16(encoded[:]))
	}
	frame := make([]byte, int(length))
	if _, err := io.ReadFull(reader, frame); err != nil {
		t.Fatalf("read event frame: %v", err)
	}
	var snapshot liveEventSnapshot
	if err := json.Unmarshal(frame, &snapshot); err != nil {
		t.Fatalf("decode event snapshot: %v", err)
	}
	return snapshot, ticket.Value
}

func TestRun_enforces_role_assignment_ticket_restart_and_revocation_contracts(t *testing.T) {
	// Given
	directory := t.TempDir()
	server := startLiveServer(t, directory)
	_, bootstrapPayload := server.request(t, liveRequest{
		method: http.MethodPost, path: "/api/v1/bootstrap",
		body: `{"setup_secret":"integration-setup","name":"Admin"}`,
	})
	var admin liveCredential
	if err := json.Unmarshal(bootstrapPayload, &admin); err != nil {
		t.Fatalf("decode admin: %v", err)
	}
	_, codePayload := server.request(t, liveRequest{
		method: http.MethodPost, path: "/api/v1/pairing-codes", token: admin.Token, body: `{"role":"renderer"}`,
	})
	var code struct {
		Value string `json:"code"`
	}
	if err := json.Unmarshal(codePayload, &code); err != nil {
		t.Fatalf("decode pairing code: %v", err)
	}
	_, rendererPayload := server.request(t, liveRequest{
		method: http.MethodPost, path: "/api/v1/pairings", body: `{"code":"` + code.Value + `","name":"Renderer"}`,
	})
	var renderer liveCredential
	if err := json.Unmarshal(rendererPayload, &renderer); err != nil {
		t.Fatalf("decode renderer: %v", err)
	}
	created, _ := server.request(t, liveRequest{
		method: http.MethodPost, path: "/api/v1/zones", token: admin.Token, body: `{"zone_id":"living","name":"Living"}`,
	})
	assigned, _ := server.request(t, liveRequest{
		method: http.MethodPut, path: "/api/v1/zones/living/renderer", token: admin.Token,
		body:    `{"renderer_id":"` + renderer.Device.ID + `"}`,
		headers: map[string]string{"If-Match": "0", "Idempotency-Key": "assign-live"},
	})
	forbidden, _ := server.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: renderer.Token})
	firstSnapshot, ticket := server.eventSnapshot(t, admin.Token)
	replay, replayPayload := server.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/events?ticket=" + url.QueryEscape(ticket)})

	// When
	if err := server.stop(); err != nil {
		t.Fatalf("stop before restart: %v", err)
	}
	restarted := startLiveServer(t, directory)
	zonesBeforeRevoke, zonesBeforePayload := restarted.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: admin.Token})
	secondSnapshot, _ := restarted.eventSnapshot(t, admin.Token)
	revoked, _ := restarted.request(t, liveRequest{
		method: http.MethodDelete, path: "/api/v1/devices/" + renderer.Device.ID, token: admin.Token,
	})
	revokedToken, revokedPayload := restarted.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/event-tickets", token: renderer.Token})
	zonesAfterRevoke, zonesAfterPayload := restarted.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: admin.Token})

	// Then
	if created.StatusCode != http.StatusCreated || assigned.StatusCode != http.StatusOK || forbidden.StatusCode != http.StatusForbidden ||
		replay.StatusCode != http.StatusUnauthorized || !strings.Contains(string(replayPayload), "EVENT_TICKET_USED") ||
		zonesBeforeRevoke.StatusCode != http.StatusOK || !strings.Contains(string(zonesBeforePayload), renderer.Device.ID) ||
		revoked.StatusCode != http.StatusNoContent || revokedToken.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(string(revokedPayload), "TOKEN_REVOKED") || zonesAfterRevoke.StatusCode != http.StatusOK ||
		!strings.Contains(string(zonesAfterPayload), `"renderer_id":null`) || !strings.Contains(string(zonesAfterPayload), `"transport":"suspended"`) {
		t.Fatalf("live role lifecycle statuses create=%d assign=%d forbidden=%d replay=%d before=%d revoke=%d token=%d after=%d; replay=%s before=%s token=%s after=%s",
			created.StatusCode, assigned.StatusCode, forbidden.StatusCode, replay.StatusCode, zonesBeforeRevoke.StatusCode,
			revoked.StatusCode, revokedToken.StatusCode, zonesAfterRevoke.StatusCode, replayPayload, zonesBeforePayload, revokedPayload, zonesAfterPayload)
	}
	if firstSnapshot.Type != "snapshot" || secondSnapshot.Type != "snapshot" || firstSnapshot.Epoch == secondSnapshot.Epoch {
		t.Fatalf("event epochs did not change across restart: %v / %v", firstSnapshot, secondSnapshot)
	}
}
