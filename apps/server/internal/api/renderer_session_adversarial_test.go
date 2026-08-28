package api_test

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func Test_RendererSession_new_connection_fences_old_epoch(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 1)
	first, firstWelcome := scenario.openSocket(t, 0, nil)
	firstCommand := first.readApplicationFrame(t)

	// When
	second, secondWelcome := scenario.openSocket(t, 0, nil)
	secondCommand := second.readApplicationFrame(t)
	opcode, payload := first.readFrame(t)

	// Then
	if firstWelcome.SessionEpoch == secondWelcome.SessionEpoch {
		t.Fatalf("connection epoch was reused: %q", firstWelcome.SessionEpoch)
	}
	if secondCommand.CommandID != firstCommand.CommandID || secondCommand.Sequence != firstCommand.Sequence {
		t.Fatalf("fenced command identity changed: first=%+v second=%+v", firstCommand, secondCommand)
	}
	if opcode != 0x8 || len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != 1008 ||
		string(payload[2:]) != "stale session epoch" {
		t.Fatalf("old connection close = %#x %x", opcode, payload)
	}
}

func Test_RendererSession_revocation_closes_socket_and_rejects_reconnect(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 0)
	socket, _ := scenario.openSocket(t, 0, nil)

	// When
	revoked := request(t, scenario.fixture.handler, http.MethodDelete,
		"/api/v1/devices/"+string(scenario.renderer.Device.ID), scenario.fixture.admin.Token, "", nil)
	opcode, payload := socket.readFrame(t)
	request, err := http.NewRequest(http.MethodGet, scenario.server.URL+"/api/v1/renderers/"+
		string(scenario.renderer.Device.ID)+"/session", nil)
	if err != nil {
		t.Fatalf("create reconnect request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+scenario.renderer.Token)
	reconnect, err := scenario.server.Client().Do(request)
	if err != nil {
		t.Fatalf("reconnect revoked renderer: %v", err)
	}
	reconnectBody, readErr := io.ReadAll(reconnect.Body)
	closeErr := reconnect.Body.Close()

	// Then
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke renderer = %d %s", revoked.Code, revoked.Body.String())
	}
	if opcode != 0x8 || len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != 1008 ||
		string(payload[2:]) != "device revoked" {
		t.Fatalf("revocation close = %#x %x", opcode, payload)
	}
	if readErr != nil || closeErr != nil || reconnect.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(string(reconnectBody), `"code":"TOKEN_REVOKED"`) {
		t.Fatalf("revoked reconnect = %d %q read=%v close=%v", reconnect.StatusCode, reconnectBody, readErr, closeErr)
	}
}

func Test_RendererSession_accepts_bearer_only_in_header_and_selects_static_subprotocol(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 0)
	parsed, err := url.Parse(scenario.server.URL)
	if err != nil {
		t.Fatalf("parse Server URL: %v", err)
	}
	endpoint := scenario.server.URL + "/api/v1/renderers/" + string(scenario.renderer.Device.ID) + "/session"

	// When
	queryRequest, err := http.NewRequest(http.MethodGet, endpoint+"?token="+url.QueryEscape(scenario.renderer.Token), nil)
	if err != nil {
		t.Fatalf("create query-token request: %v", err)
	}
	queryRequest.Header.Set("Authorization", "Bearer "+scenario.renderer.Token)
	queryResponse, err := scenario.server.Client().Do(queryRequest)
	if err != nil {
		t.Fatalf("send query-token request: %v", err)
	}
	queryBody, _ := io.ReadAll(queryResponse.Body)
	_ = queryResponse.Body.Close()
	wrongProtocol, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create wrong-protocol request: %v", err)
	}
	wrongProtocol.Host = parsed.Host
	wrongProtocol.Header.Set("Authorization", "Bearer "+scenario.renderer.Token)
	wrongProtocol.Header.Set("Connection", "Upgrade")
	wrongProtocol.Header.Set("Upgrade", "websocket")
	wrongProtocol.Header.Set("Sec-WebSocket-Version", "13")
	wrongProtocol.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	wrongProtocol.Header.Set("Sec-WebSocket-Protocol", "jastreamer.bearer."+scenario.renderer.Token)
	wrongResponse, err := scenario.server.Client().Do(wrongProtocol)
	if err != nil {
		t.Fatalf("send wrong-protocol request: %v", err)
	}
	wrongBody, _ := io.ReadAll(wrongResponse.Body)
	_ = wrongResponse.Body.Close()

	// Then
	if queryResponse.StatusCode != http.StatusBadRequest || strings.Contains(string(queryBody), scenario.renderer.Token) {
		t.Fatalf("query-token response = %d %q", queryResponse.StatusCode, queryBody)
	}
	if wrongResponse.StatusCode != http.StatusUpgradeRequired ||
		wrongResponse.Header.Get("Sec-WebSocket-Protocol") != rendererSessionProtocol ||
		strings.Contains(string(wrongBody), scenario.renderer.Token) {
		t.Fatalf("subprotocol response = %d headers=%v body=%q", wrongResponse.StatusCode, wrongResponse.Header, wrongBody)
	}
}

func Test_RendererSession_rejects_oversized_message_with_bounded_close(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 0)
	socket, _ := scenario.openSocket(t, 0, nil)

	// When
	header := []byte{0x81, 0xff, 0, 0, 0, 0, 0, 1, 0, 1}
	if _, err := socket.connection.Write(header); err != nil {
		t.Fatalf("write oversized renderer header: %v", err)
	}
	opcode, payload := socket.readFrame(t)

	// Then
	if opcode != 0x8 || len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != 1009 {
		t.Fatalf("oversized renderer close = %#x %x", opcode, payload)
	}
}

func Test_RendererSession_conflicting_result_ID_is_rejected_without_second_side_effect(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 1)
	socket, _ := scenario.openSocket(t, 0, nil)
	command := socket.readApplicationFrame(t)
	socket.sendCommandAck(t, command, "received")
	physicalStarts := 1
	socket.sendResult(t, rendererResultInput{
		command: command, resultID: "result-conflicting", status: "succeeded", observedState: "playing",
	})
	if ack := socket.readApplicationFrame(t); ack.Type != "result.ack" {
		t.Fatalf("first result acknowledgement = %+v", ack)
	}

	// When
	socket.sendResult(t, rendererResultInput{
		command: command, resultID: "result-conflicting", status: "failed", observedState: "failed",
	})
	protocolError := socket.readApplicationFrame(t)
	durable, err := scenario.fixture.store.DurableCommand(t.Context(), command.CommandID)

	// Then
	if protocolError.Type != "error" || protocolError.Code != "COMMAND_ID_CONFLICT" {
		t.Fatalf("conflicting result response = %+v", protocolError)
	}
	if err != nil {
		t.Fatalf("load command after conflict: %v", err)
	}
	var retained rendererPendingResult
	if err := json.Unmarshal(durable.Result, &retained); err != nil || retained.Status != "succeeded" {
		t.Fatalf("retained terminal result = %s (%v)", durable.Result, err)
	}
	if physicalStarts != 1 {
		t.Fatalf("physical side effects = %d", physicalStarts)
	}
}
