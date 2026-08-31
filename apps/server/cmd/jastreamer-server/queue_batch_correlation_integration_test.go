package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestTask19LiveServerCorrelatesEqualQueueRevisionsByZone(t *testing.T) {
	server := startLiveServer(t, t.TempDir())
	bootstrap, body := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/bootstrap", body: `{"setup_secret":"integration-setup","name":"Admin"}`})
	var admin struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &admin); err != nil || bootstrap.StatusCode != http.StatusCreated || admin.Token == "" {
		t.Fatalf("bootstrap = %d %s (%v)", bootstrap.StatusCode, body, err)
	}
	for _, zoneID := range []string{"batch-a", "batch-b"} {
		created, payload := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/zones", token: admin.Token, body: `{"zone_id":"` + zoneID + `","name":"` + zoneID + `"}`})
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("create %s = %d %s", zoneID, created.StatusCode, payload)
		}
	}
	events := openTask19EventSocket(t, server, admin.Token)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entryIDs := make(map[string][]string)
	for revision := 1; revision <= 2; revision++ {
		for _, zoneID := range []string{"batch-a", "batch-b"} {
			value := strconv.Itoa(revision)
			response, payload := server.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/zones/" + zoneID + "/queue", token: admin.Token, body: `{"command":"append","track_ids":["track-` + zoneID + `-` + value + `"]}`, headers: map[string]string{"If-Match": `"` + strconv.Itoa(revision-1) + `"`, "Idempotency-Key": "append-" + zoneID + "-" + value, "X-Request-ID": "correlation-" + zoneID + "-" + value}})
			var result struct {
				Revision int      `json:"revision"`
				EntryIDs []string `json:"entry_ids"`
			}
			if err := json.Unmarshal(payload, &result); err != nil || response.StatusCode != http.StatusCreated || result.Revision != revision || response.Header.Get("ETag") != `"`+value+`"` || len(result.EntryIDs) != 1 {
				t.Fatalf("append %s/%d = %d ETag=%q %s (%v)", zoneID, revision, response.StatusCode, response.Header.Get("ETag"), payload, err)
			}
			event := events.readEvent(t, ctx)
			if event.Type != "invalidation" || event.Resource != "queue" || event.ZoneID != zoneID || event.Revision != uint64(revision) {
				t.Fatalf("event %s/%d = %+v", zoneID, revision, event)
			}
			entryIDs[zoneID] = append(entryIDs[zoneID], result.EntryIDs[0])
		}
	}
	if entryIDs["batch-a"][0] == entryIDs["batch-b"][0] || entryIDs["batch-a"][0] == entryIDs["batch-a"][1] {
		t.Fatalf("entry IDs are not unique: %+v", entryIDs)
	}
	t.Logf("queue_batches zones=2 batches=4 entries=4 batch_a=%v batch_b=%v final_revisions=2/2 etags=\"2\"/\"2\" events=queue:batch-a:1,queue:batch-b:1,queue:batch-a:2,queue:batch-b:2", entryIDs["batch-a"], entryIDs["batch-b"])
}
