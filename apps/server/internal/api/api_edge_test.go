package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestEnqueue_rejects_trailing_JSON_with_one_error_and_no_mutation(t *testing.T) {
	value := newFixture(t)
	controller := pairController(t, value)
	body := `{"tracks":[{"track_id":"track-a","available":true}]} {"extra":true}`
	response := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token, body,
		map[string]string{"Idempotency-Key": "trailing", "If-Match": "0"})
	if response.Code != http.StatusBadRequest || strings.Count(response.Body.String(), `"code"`) != 1 || responseCode(t, response) != "INVALID_REQUEST" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	state := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/queue", controller.Token, "", nil)
	if strings.Contains(state.Body.String(), "track-a") {
		t.Fatalf("trailing JSON mutated queue: %s", state.Body.String())
	}
}

func TestPairing_rate_limits_distinct_IPv6_hosts_independently(t *testing.T) {
	value := newFixture(t)
	for range 5 {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/pairings", strings.NewReader(`{"code":"000000","name":"Bad"}`))
		req.RemoteAddr = "[2001:db8::1]:41000"
		req.Header.Set("Content-Type", "application/json")
		value.handler.ServeHTTP(recorder, req)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pairings", strings.NewReader(`{"code":"000000","name":"Other"}`))
	req.RemoteAddr = "[2001:db8::2]:41000"
	req.Header.Set("Content-Type", "application/json")
	value.handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest || responseCode(t, recorder) != "PAIRING_CODE_INVALID" {
		t.Fatalf("second IPv6 requester inherited rate limit: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDecisionExplanation_reads_persisted_Todo12_decision(t *testing.T) {
	value := newFixture(t)
	controller := pairController(t, value)
	_, err := value.store.Enqueue(context.Background(), playback.EnqueueRequest{
		ZoneID: "main", IdempotencyKey: "persisted-decision", ExpectedRevision: 0,
		Tracks: []playback.QueueTrack{{ID: "track-a", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	persisted, err := value.store.ReserveNext(context.Background(), "main", playback.Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	response := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/decision-explanation", controller.Token, "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reason":"PLAY_EXPLICIT"`) ||
		!strings.Contains(response.Body.String(), `"track_id":"track-a"`) || !strings.Contains(response.Body.String(), persisted.ID) ||
		strings.Contains(response.Body.String(), `"explanation"`) {
		t.Fatalf("decision explanation did not expose persisted Todo12 decision cleanly: %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogStatus_observes_background_analysis_without_rescan(t *testing.T) {
	value := newFixture(t)
	controller := pairController(t, value)
	track := value.catalog.Tracks["track-a"]
	track.AnalysisStatus = catalog.AnalysisQueued
	value.catalog.Tracks["track-a"] = track
	queued := request(t, value.handler, http.MethodGet, "/api/v1/catalog/status", controller.Token, "", nil)
	track.AnalysisStatus = catalog.AnalysisComplete
	value.catalog.Tracks["track-a"] = track
	complete := request(t, value.handler, http.MethodGet, "/api/v1/catalog/status", controller.Token, "", nil)
	if !strings.Contains(queued.Body.String(), `"analysis_queued":1`) || !strings.Contains(complete.Body.String(), `"analysis_complete":1`) {
		t.Fatalf("queued/complete = %s / %s", queued.Body.String(), complete.Body.String())
	}
}
