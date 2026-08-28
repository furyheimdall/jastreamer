package api_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func configureTransportZone(t *testing.T, value fixture, capabilities []string) {
	t.Helper()
	if _, err := value.store.CreateZone(context.Background(), playback.ZoneDefinition{ID: "transport", DisplayName: "Transport"}); err != nil {
		t.Fatalf("create transport zone: %v", err)
	}
	if _, err := value.store.UpsertCustomRenderer(context.Background(), playback.CustomRenderer{
		ID: "transport-renderer", DisplayName: "Renderer", State: playback.RendererConnected,
		ProtocolMajor: 3, Capabilities: capabilities, LastSeenAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	if _, err := value.store.AssignRenderer(context.Background(), playback.AssignmentRequest{ZoneID: "transport", RendererID: "transport-renderer"}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
}

func Test_Transport_start_replay_returns_exact_pending_response_without_duplicate_command(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	configureTransportZone(t, value, []string{"command:play", "command:pause", "command:resume", "command:stop", "command:seek"})
	enqueued := request(t, value.handler, http.MethodPost, "/api/v1/zones/transport/queue", controller.Token,
		`{"command":"append","track_ids":["track-a"]}`,
		map[string]string{"If-Match": "0", "Idempotency-Key": "transport-seed"})
	if enqueued.Code != http.StatusCreated {
		t.Fatalf("enqueue = %d %s", enqueued.Code, enqueued.Body.String())
	}
	headers := map[string]string{"If-Match": "1", "Idempotency-Key": "start"}

	// When
	first := request(t, value.handler, http.MethodPost, "/api/v1/zones/transport/transport", controller.Token, `{"command":"start"}`, headers)
	replay := request(t, value.handler, http.MethodPost, "/api/v1/zones/transport/transport", controller.Token, `{"command":"start"}`, headers)

	// Then
	if first.Code != http.StatusAccepted || replay.Code != first.Code || replay.Body.String() != first.Body.String() || replay.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Fatalf("first/replay = %d %q %s / %d %q %s", first.Code, first.Header().Get("ETag"), first.Body.String(), replay.Code, replay.Header().Get("ETag"), replay.Body.String())
	}
	commands, err := value.store.PendingOutbox(context.Background(), "transport")
	if err != nil || len(commands) != 1 {
		t.Fatalf("pending commands = %+v (%v)", commands, err)
	}
}

func Test_Transport_unsupported_seek_is_typed_and_does_not_change_revision(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	configureTransportZone(t, value, []string{"command:play"})
	if _, err := value.store.Enqueue(context.Background(), playback.EnqueueRequest{
		ZoneID: "transport", IdempotencyKey: "seed", Tracks: []playback.QueueTrack{{ID: "track-a", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	started, err := value.store.MutateTransport(context.Background(), playback.TransportMutationRequest{
		ZoneID: "transport", IdempotencyKey: "start", ExpectedRevision: 1, Command: playback.TransportStart,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := value.store.ConfirmStart(context.Background(), "transport", started.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	before, err := value.store.Snapshot(context.Background(), "transport")
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}

	// When
	response := request(t, value.handler, http.MethodPost, "/api/v1/zones/transport/transport", controller.Token,
		`{"command":"seek","position_ms":500}`,
		map[string]string{"If-Match": revisionStringForTest(before.Revision), "Idempotency-Key": "unsupported-seek"})

	// Then
	if response.Code != http.StatusConflict || responseCode(t, response) != "UNSUPPORTED_CAPABILITY" {
		t.Fatalf("seek = %d %s", response.Code, response.Body.String())
	}
	after, snapshotErr := value.store.Snapshot(context.Background(), "transport")
	if snapshotErr != nil || after.Revision != before.Revision {
		t.Fatalf("unsupported seek mutated state: before=%+v after=%+v (%v)", before, after, snapshotErr)
	}
}

func revisionStringForTest(revision playback.Revision) string {
	return strconv.FormatInt(int64(revision), 10)
}
