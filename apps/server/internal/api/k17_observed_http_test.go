package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func Test_PlaybackState_exposes_persisted_K17_observed_truth(t *testing.T) {
	// Given: a K17 start intent has been dispatched and polling confirms Server-owned playback.
	value := newTransportMediaFixture(t, playback.RendererKindK17)
	server := httptest.NewTLSServer(value.handler(api.Config{}))
	defer server.Close()
	startTransport(t, server, value.controller)
	observedAt := time.Date(2026, 8, 23, 12, 0, 1, 0, time.UTC)
	if err := value.fixture.store.RecordK17Observation(context.Background(), playback.K17Observation{
		RendererID: "transport-renderer", ZoneID: "transport", Transport: "PLAYING",
		CurrentURI: value.adapter.mediaURL, Owned: true, ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/zones/transport/playback-state", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+value.controller)

	// When
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"observed_transport":"playing"`) {
		t.Fatalf("playback truth = %d %s", response.StatusCode, body)
	}
}
