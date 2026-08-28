package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORS_allows_configured_Control_origin_preflight(t *testing.T) {
	// Given
	value := newFixture(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/zones/main/queue", nil)
	request.Header.Set("Origin", "https://control.fixture.invalid")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization,if-match,idempotency-key,x-jake-supported-protocol-majors")

	// When
	value.handler.ServeHTTP(recorder, request)

	// Then
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Access-Control-Allow-Origin") != "https://control.fixture.invalid" ||
		recorder.Header().Get("Vary") != "Origin" ||
		recorder.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type, If-Match, Idempotency-Key, X-Jake-Protocol-Major, X-Jake-Supported-Protocol-Majors" {
		t.Fatalf("preflight = %d %#v", recorder.Code, recorder.Header())
	}
}

func TestWSS_rejects_unlisted_origin_but_allows_native_no_origin(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	ticket := issueEventTicket(t, value, controller.Token)

	// When
	blocked := request(t, value.handler, http.MethodGet, "/api/v1/events?ticket="+ticket, "", "",
		map[string]string{"Origin": "https://attacker.invalid"})
	native := request(t, value.handler, http.MethodGet, "/api/v1/events?ticket="+ticket, "", "", nil)

	// Then
	if blocked.Code != http.StatusForbidden || responseCode(t, blocked) != "ORIGIN_FORBIDDEN" {
		t.Fatalf("blocked WSS origin = %d %s", blocked.Code, blocked.Body.String())
	}
	if native.Code != http.StatusUpgradeRequired || responseCode(t, native) != "WEBSOCKET_UPGRADE_REQUIRED" {
		t.Fatalf("native WSS request = %d %s", native.Code, native.Body.String())
	}
}

func TestCORS_rejects_unlisted_origin_before_pairing_code_mutation(t *testing.T) {
	// Given
	value := newFixture(t)
	code, err := value.manager.GeneratePairingCode(context.Background(), value.admin.Token)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	body := `{"code":"` + code.Value + `","name":"Control"}`
	blocked := httptest.NewRecorder()
	blockedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/pairings", strings.NewReader(body))
	blockedRequest.Header.Set("Content-Type", "text/plain")
	blockedRequest.Header.Set("Origin", "https://attacker.invalid")

	// When
	value.handler.ServeHTTP(blocked, blockedRequest)
	allowed := request(t, value.handler, http.MethodPost, "/api/v1/pairings", "", body, nil)

	// Then
	if blocked.Code != http.StatusForbidden || blocked.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("blocked request = %d %#v", blocked.Code, blocked.Header())
	}
	if allowed.Code != http.StatusCreated {
		t.Fatalf("blocked origin consumed pairing code: %d %s", allowed.Code, allowed.Body.String())
	}
}
