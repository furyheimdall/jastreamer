package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func Test_Queue_append_uses_authoritative_catalog_availability_and_exact_replay(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	value.catalog.Tracks["missing"] = catalog.Track{TrackID: "missing", Available: false}
	headers := map[string]string{"If-Match": "0", "Idempotency-Key": "authoritative-append"}
	body := `{"command":"append","track_ids":["missing"]}`

	// When
	first := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token, body, headers)
	replay := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token, body, headers)

	// Then
	if first.Code != http.StatusCreated || replay.Code != first.Code || replay.Body.String() != first.Body.String() || replay.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Fatalf("first/replay differ: %d %q %s / %d %q %s", first.Code, first.Header().Get("ETag"), first.Body.String(), replay.Code, replay.Header().Get("ETag"), replay.Body.String())
	}
	decision, err := value.store.ReserveNext(context.Background(), "main", playback.Boundary{ID: "blocked"})
	if err != nil {
		t.Fatalf("reserve unavailable head: %v", err)
	}
	if decision.Kind != playback.DecisionBlock {
		t.Fatalf("client availability influenced queue: %+v", decision)
	}
}

func Test_Queue_mutations_require_exact_revision_and_do_not_mutate_on_stale(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	created := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token,
		`{"command":"append","track_ids":["track-a"]}`,
		map[string]string{"If-Match": "0", "Idempotency-Key": "seed"})
	var createdBody struct {
		EntryIDs []string `json:"entry_ids"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || len(createdBody.EntryIDs) != 1 {
		t.Fatalf("decode created queue: %s (%v)", created.Body.String(), err)
	}
	entryID := createdBody.EntryIDs[0]

	// When
	stale := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token,
		`{"command":"remove","entry_id":"`+entryID+`"}`,
		map[string]string{"If-Match": "0", "Idempotency-Key": "stale-remove"})
	state := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/queue", controller.Token, "", nil)

	// Then
	if stale.Code != http.StatusConflict || responseCode(t, stale) != "STALE_REVISION" || !strings.Contains(state.Body.String(), entryID) {
		t.Fatalf("stale mutation = %d %s, state=%s", stale.Code, stale.Body.String(), state.Body.String())
	}
}
