package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTask19LiveServerZoneDeleteLifecycle(t *testing.T) {
	server := startLiveServer(t, t.TempDir())
	bootstrap, bootstrapBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/bootstrap", body: `{"setup_secret":"integration-setup","name":"Admin"}`})
	var admin struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bootstrapBody, &admin); err != nil || bootstrap.StatusCode != http.StatusCreated || admin.Token == "" {
		t.Fatalf("bootstrap = %d %s (%v)", bootstrap.StatusCode, bootstrapBody, err)
	}
	create := func(id string) {
		response, body := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/zones", token: admin.Token, body: `{"zone_id":"` + id + `","name":"` + id + `"}`})
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create %s = %d %s", id, response.StatusCode, body)
		}
	}
	create("baseline")
	create("task19-owned")
	codeResponse, codeBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/pairing-codes", token: admin.Token, body: `{"role":"renderer"}`})
	var code struct {
		Value string `json:"code"`
	}
	if err := json.Unmarshal(codeBody, &code); err != nil || codeResponse.StatusCode != http.StatusCreated || code.Value == "" {
		t.Fatalf("pairing code = %d %s (%v)", codeResponse.StatusCode, codeBody, err)
	}
	pairedResponse, pairedBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/pairings", body: `{"code":"` + code.Value + `","name":"Task19 renderer"}`})
	var renderer struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(pairedBody, &renderer); err != nil || pairedResponse.StatusCode != http.StatusCreated || renderer.Device.ID == "" {
		t.Fatalf("pair renderer = %d %s (%v)", pairedResponse.StatusCode, pairedBody, err)
	}
	assigned, assignedBody := server.request(t, liveRequest{method: http.MethodPut, path: "/api/v1/zones/task19-owned/renderer", token: admin.Token, body: `{"renderer_id":"` + renderer.Device.ID + `"}`, headers: map[string]string{"If-Match": `"0"`, "Idempotency-Key": "assign-task19"}})
	if assigned.StatusCode != http.StatusOK || assigned.Header.Get("ETag") != `"1"` {
		t.Fatalf("assign = %d ETag=%q %s", assigned.StatusCode, assigned.Header.Get("ETag"), assignedBody)
	}
	queued, queuedBody := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/zones/task19-owned/queue", token: admin.Token, body: `{"command":"append","track_ids":["observed-track"]}`, headers: map[string]string{"If-Match": `"0"`, "Idempotency-Key": "queue-task19"}})
	var queueResult struct {
		Revision int64    `json:"revision"`
		EntryIDs []string `json:"entry_ids"`
	}
	if err := json.Unmarshal(queuedBody, &queueResult); err != nil || queued.StatusCode != http.StatusCreated || queueResult.Revision != 1 || len(queueResult.EntryIDs) != 1 {
		t.Fatalf("queue = %d %s (%v)", queued.StatusCode, queuedBody, err)
	}
	events := openTask19EventSocket(t, server, admin.Token)
	eventContext, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	deleted, deletedBody := server.request(t, liveRequest{method: http.MethodDelete, path: "/api/v1/zones/task19-owned", token: admin.Token, headers: map[string]string{"If-Match": `"1"`, "Idempotency-Key": "delete-task19"}})
	var deletion struct {
		ZoneID   string `json:"zone_id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(deletedBody, &deletion); err != nil || deleted.StatusCode != http.StatusOK || deleted.Header.Get("ETag") != `"2"` || deletion.ZoneID != "task19-owned" || deletion.Revision != 2 {
		t.Fatalf("delete = %d ETag=%q %s (%v)", deleted.StatusCode, deleted.Header.Get("ETag"), deletedBody, err)
	}
	event := events.readEvent(t, eventContext)
	inventory, inventoryBody := server.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: admin.Token})
	var zones struct {
		Zones []struct {
			ID string `json:"zone_id"`
		} `json:"zones"`
	}
	if err := json.Unmarshal(inventoryBody, &zones); err != nil || inventory.StatusCode != http.StatusOK || len(zones.Zones) != 1 || zones.Zones[0].ID != "baseline" || event.Type != "invalidation" || event.Resource != "zones" || event.Revision != 2 {
		t.Fatalf("event/inventory = %+v / %d %s (%v)", event, inventory.StatusCode, inventoryBody, err)
	}
	t.Logf("zone_delete status=%d zone_id=%s renderer_id=%s entry_id=%s assignment_revision=1 queue_revision=%d deletion_revision=%d etag=%s event=%s:%d baseline_zones=%d", deleted.StatusCode, deletion.ZoneID, renderer.Device.ID, queueResult.EntryIDs[0], queueResult.Revision, deletion.Revision, deleted.Header.Get("ETag"), event.Resource, event.Revision, len(zones.Zones))
}
