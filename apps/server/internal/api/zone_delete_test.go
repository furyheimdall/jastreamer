package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestZoneDeleteAPI_requires_admin_and_exact_preconditions(t *testing.T) {
	value := newFixture(t)
	createZone(t, value, "owned")
	renderer := pairRole(t, value, security.RoleRenderer, "Renderer")
	controller := pairController(t, value)
	tests := []struct {
		name, token string
		headers     map[string]string
		status      int
		code        string
	}{
		{name: "anonymous", headers: deleteHeaders("0", "delete"), status: http.StatusUnauthorized, code: "UNAUTHORIZED"},
		{name: "renderer credential", token: renderer.Token, headers: deleteHeaders("0", "delete"), status: http.StatusForbidden, code: "ADMIN_REQUIRED"},
		{name: "controller credential", token: controller.Token, headers: deleteHeaders("0", "delete"), status: http.StatusForbidden, code: "ADMIN_REQUIRED"},
		{name: "missing If-Match", token: value.admin.Token, headers: map[string]string{"Idempotency-Key": "delete"}, status: http.StatusPreconditionRequired, code: "REVISION_REQUIRED"},
		{name: "missing idempotency key", token: value.admin.Token, headers: map[string]string{"If-Match": `"0"`}, status: http.StatusPreconditionRequired, code: "IDEMPOTENCY_KEY_REQUIRED"},
		{name: "stale revision", token: value.admin.Token, headers: deleteHeaders("1", "delete"), status: http.StatusConflict, code: "STALE_REVISION"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, value.handler, http.MethodDelete, "/api/v1/zones/owned", test.token, "", test.headers)
			if response.Code != test.status || responseCode(t, response) != test.code {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			zones := request(t, value.handler, http.MethodGet, "/api/v1/zones", value.admin.Token, "", nil)
			if !strings.Contains(zones.Body.String(), `"zone_id":"owned"`) {
				t.Fatalf("zone mutated after rejection: %s", zones.Body.String())
			}
		})
	}
}

func TestZoneDeleteAPI_publishes_one_post_commit_event_and_replay_is_eventless(t *testing.T) {
	value := newFixture(t)
	if _, err := value.store.CreateZone(t.Context(), playback.ZoneDefinition{ID: "baseline", DisplayName: "Baseline"}); err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.CreateZone(t.Context(), playback.ZoneDefinition{ID: "owned", DisplayName: "Owned"}); err != nil {
		t.Fatal(err)
	}
	queued, err := value.store.MutateQueue(t.Context(), playback.QueueMutationRequest{ZoneID: "owned", IdempotencyKey: "populate", Command: playback.QueueAppend, Tracks: []playback.QueueTrack{{ID: "track-a", Available: true}}})
	if err != nil || queued.Revision != 1 {
		t.Fatalf("populate = %+v, %v", queued, err)
	}
	controller := pairController(t, value)
	events := openEventSocket(t, value.handler, controller.Token)

	deleted := request(t, value.handler, http.MethodDelete, "/api/v1/zones/owned", value.admin.Token, "", deleteHeaders("1", "delete-owned"))
	if deleted.Code != http.StatusOK || deleted.Header().Get("ETag") != `"2"` {
		t.Fatalf("delete = %d ETag=%q %s", deleted.Code, deleted.Header().Get("ETag"), deleted.Body.String())
	}
	var body struct {
		ZoneID   string `json:"zone_id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(deleted.Body.Bytes(), &body); err != nil || body.ZoneID != "owned" || body.Revision != 2 {
		t.Fatalf("delete body = %+v, %v", body, err)
	}
	event := events.readEvent(t)
	zones := request(t, value.handler, http.MethodGet, "/api/v1/zones", value.admin.Token, "", nil)
	if event["type"] != "invalidation" || event["resource"] != "zones" || event["revision"] != float64(2) || strings.Contains(zones.Body.String(), `"zone_id":"owned"`) || !strings.Contains(zones.Body.String(), `"zone_id":"baseline"`) {
		t.Fatalf("event/state = %#v / %s", event, zones.Body.String())
	}

	replay := request(t, value.handler, http.MethodDelete, "/api/v1/zones/owned", value.admin.Token, "", deleteHeaders("1", "delete-owned"))
	if replay.Code != http.StatusOK || replay.Header().Get("ETag") != `"2"` {
		t.Fatalf("replay = %d %s", replay.Code, replay.Body.String())
	}
	sentinel := request(t, value.handler, http.MethodPost, "/api/v1/catalog/scans", value.admin.Token, "", nil)
	if sentinel.Code != http.StatusAccepted {
		t.Fatalf("sentinel = %d %s", sentinel.Code, sentinel.Body.String())
	}
	next := events.readEvent(t)
	if next["resource"] != "catalog" || next["revision"] != float64(8) {
		t.Fatalf("replay emitted misleading event before sentinel: %#v", next)
	}
	conflict := request(t, value.handler, http.MethodDelete, "/api/v1/zones/owned", value.admin.Token, "", deleteHeaders("2", "delete-owned"))
	unknown := request(t, value.handler, http.MethodDelete, "/api/v1/zones/owned", value.admin.Token, "", deleteHeaders("2", "delete-again"))
	if conflict.Code != http.StatusConflict || responseCode(t, conflict) != "IDEMPOTENCY_CONFLICT" || unknown.Code != http.StatusNotFound {
		t.Fatalf("conflict/unknown = %d %s / %d %s", conflict.Code, conflict.Body.String(), unknown.Code, unknown.Body.String())
	}
}

func TestQueueInvalidations_are_correlated_per_zone_at_equal_revisions(t *testing.T) {
	value := newFixture(t)
	for _, id := range []playback.ZoneID{"zone-a", "zone-b"} {
		if _, err := value.store.CreateZone(t.Context(), playback.ZoneDefinition{ID: id, DisplayName: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	controller := pairController(t, value)
	events := openEventSocket(t, value.handler, controller.Token)
	for _, id := range []string{"zone-a", "zone-b"} {
		response := request(t, value.handler, http.MethodPost, "/api/v1/zones/"+id+"/queue", controller.Token, `{"command":"append","track_ids":["track-a"]}`, deleteHeaders("0", "append-"+id))
		if response.Code != http.StatusCreated {
			t.Fatalf("append %s = %d %s", id, response.Code, response.Body.String())
		}
		event := events.readEvent(t)
		if event["resource"] != "queue" || event["zone_id"] != id || event["revision"] != float64(1) {
			t.Fatalf("queue event %s = %#v", id, event)
		}
	}
}

func TestZoneDeleteAPI_storage_failure_emits_no_event(t *testing.T) {
	value := newFixture(t)
	if _, err := value.store.CreateZone(t.Context(), playback.ZoneDefinition{ID: "owned", DisplayName: "Owned"}); err != nil {
		t.Fatal(err)
	}
	controller := pairController(t, value)
	events := openEventSocket(t, value.handler, controller.Token)
	if err := value.store.Close(); err != nil {
		t.Fatal(err)
	}
	failed := request(t, value.handler, http.MethodDelete, "/api/v1/zones/owned", value.admin.Token, "", deleteHeaders("0", "delete"))
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("delete failure = %d %s", failed.Code, failed.Body.String())
	}
	sentinel := request(t, value.handler, http.MethodPost, "/api/v1/catalog/scans", value.admin.Token, "", nil)
	if sentinel.Code != http.StatusAccepted {
		t.Fatalf("sentinel = %d %s", sentinel.Code, sentinel.Body.String())
	}
	event := events.readEvent(t)
	if event["resource"] != "catalog" || event["revision"] != float64(8) {
		t.Fatalf("failed delete emitted misleading event before sentinel: %#v", event)
	}
}

func createZone(t *testing.T, value fixture, id string) {
	t.Helper()
	response := request(t, value.handler, http.MethodPost, "/api/v1/zones", value.admin.Token, `{"zone_id":"`+id+`","name":"`+id+`"}`, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create %s = %d %s", id, response.Code, response.Body.String())
	}
}

func deleteHeaders(revision, key string) map[string]string {
	return map[string]string{"If-Match": `"` + revision + `"`, "Idempotency-Key": key}
}
