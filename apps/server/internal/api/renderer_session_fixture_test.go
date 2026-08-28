package api_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

const rendererSessionProtocol = "jastreamer.renderer.v3"

type rendererSessionScenario struct {
	fixture  fixture
	server   *httptest.Server
	renderer security.Credential
	zoneID   playback.ZoneID
	first    playback.Decision
}

type rendererSocket struct {
	connection net.Conn
	reader     *bufio.Reader
}

type rendererFrame struct {
	ProtocolMajor int     `json:"protocolMajor"`
	Type          string  `json:"type"`
	RendererID    string  `json:"rendererId,omitempty"`
	SessionEpoch  string  `json:"sessionEpoch,omitempty"`
	CommandID     string  `json:"commandId,omitempty"`
	ResultID      string  `json:"resultId,omitempty"`
	EventID       string  `json:"eventId,omitempty"`
	Sequence      int64   `json:"sequence,omitempty"`
	NextSequence  int64   `json:"nextSequence,omitempty"`
	ZoneID        string  `json:"zoneId,omitempty"`
	PlayID        *string `json:"playId,omitempty"`
	Kind          string  `json:"kind,omitempty"`
	Status        string  `json:"status,omitempty"`
	ObservedState *string `json:"observedState,omitempty"`
	PositionMS    *int64  `json:"positionMs,omitempty"`
	ObservedAt    string  `json:"observedAt,omitempty"`
	Deadline      string  `json:"deadline,omitempty"`
	Code          string  `json:"code,omitempty"`
}

type rendererHello struct {
	ProtocolMajor      int                     `json:"protocolMajor"`
	Type               string                  `json:"type"`
	RendererID         string                  `json:"rendererId"`
	SupportedMajors    []int                   `json:"supportedMajors"`
	Capabilities       rendererCapabilities    `json:"capabilities"`
	LastServerSequence int64                   `json:"lastServerSequence"`
	PendingResults     []rendererPendingResult `json:"pendingResults"`
}

type rendererCapabilities struct {
	Commands        []string `json:"commands"`
	MediaTypes      []string `json:"mediaTypes"`
	SupportsRange   bool     `json:"supportsRange"`
	MaxChannels     int      `json:"maxChannels"`
	MaxSampleRateHz int      `json:"maxSampleRateHz"`
}

type rendererPendingResult struct {
	ProtocolMajor int     `json:"protocolMajor"`
	Type          string  `json:"type"`
	CommandID     string  `json:"commandId"`
	ResultID      string  `json:"resultId"`
	Status        string  `json:"status"`
	ObservedState *string `json:"observedState"`
	PositionMS    *int64  `json:"positionMs"`
}

func newRendererSessionScenario(t *testing.T, tracks int) rendererSessionScenario {
	t.Helper()
	value := newFixture(t)
	renderer := pairRole(t, value, security.RoleRenderer, "Session renderer")
	zoneID := playback.ZoneID("renderer-session-zone")
	if _, err := value.store.CreateZone(t.Context(), playback.ZoneDefinition{ID: zoneID, DisplayName: "Session zone"}); err != nil {
		t.Fatalf("create session zone: %v", err)
	}
	if _, err := value.store.AssignRenderer(t.Context(), playback.AssignmentRequest{
		ZoneID: zoneID, RendererID: playback.RendererID(renderer.Device.ID),
	}); err != nil {
		t.Fatalf("assign session renderer: %v", err)
	}
	queued := make([]playback.QueueTrack, tracks)
	for index := range queued {
		queued[index] = playback.QueueTrack{ID: playback.TrackID(fmt.Sprintf("session-track-%d", index+1)), Available: true}
	}
	var first playback.Decision
	if tracks > 0 {
		if _, err := value.store.Enqueue(t.Context(), playback.EnqueueRequest{
			ZoneID: zoneID, IdempotencyKey: "renderer-session-queue", Tracks: queued,
		}); err != nil {
			t.Fatalf("enqueue session tracks: %v", err)
		}
		var err error
		first, err = value.store.ReserveNext(t.Context(), zoneID, playback.Boundary{ID: "renderer-session-start"})
		if err != nil {
			t.Fatalf("reserve session track: %v", err)
		}
	}
	server := httptest.NewTLSServer(value.handler)
	t.Cleanup(server.Close)
	return rendererSessionScenario{fixture: value, server: server, renderer: renderer, zoneID: zoneID, first: first}
}

func (scenario rendererSessionScenario) openSocket(
	t *testing.T,
	cursor int64,
	pending []rendererPendingResult,
) (*rendererSocket, rendererFrame) {
	t.Helper()
	parsed, err := url.Parse(scenario.server.URL)
	if err != nil {
		t.Fatalf("parse fixture Server URL: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(scenario.server.Certificate())
	connection, err := tls.Dial("tcp", parsed.Host, &tls.Config{
		RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("dial renderer session: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	request := fmt.Sprintf("GET /api/v1/renderers/%s/session HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Protocol: %s\r\n\r\n", scenario.renderer.Device.ID, parsed.Host, scenario.renderer.Token, rendererSessionProtocol)
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatalf("write renderer upgrade: %v", err)
	}
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil || status != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Fatalf("renderer upgrade status = %q (%v)", status, err)
	}
	selected := ""
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read renderer upgrade header: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-protocol:") {
			selected = strings.TrimSpace(strings.TrimPrefix(line, "Sec-WebSocket-Protocol:"))
		}
		if strings.Contains(line, scenario.renderer.Token) {
			t.Fatalf("renderer credential leaked in upgrade response: %q", line)
		}
	}
	if selected != rendererSessionProtocol {
		t.Fatalf("selected renderer subprotocol = %q", selected)
	}
	socket := &rendererSocket{connection: connection, reader: reader}
	hello, err := json.Marshal(rendererHello{
		ProtocolMajor: 3, Type: "hello", RendererID: string(scenario.renderer.Device.ID),
		SupportedMajors: []int{3, 2}, LastServerSequence: cursor, PendingResults: pending,
		Capabilities: rendererCapabilities{
			Commands:   []string{"play", "pause", "resume", "stop", "seek"},
			MediaTypes: []string{"audio/flac"}, SupportsRange: true,
			MaxChannels: 2, MaxSampleRateHz: 192000,
		},
	})
	if err != nil {
		t.Fatalf("encode renderer hello: %v", err)
	}
	socket.sendJSON(t, hello)
	welcome := socket.readApplicationFrame(t)
	if welcome.Type != "welcome" || welcome.ProtocolMajor != 3 || welcome.SessionEpoch == "" {
		t.Fatalf("renderer welcome = %+v", welcome)
	}
	return socket, welcome
}
