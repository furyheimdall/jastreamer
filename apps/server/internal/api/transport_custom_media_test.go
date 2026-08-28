package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func Test_Transport_custom_renderer_uses_trusted_HTTPS_origin_not_host_headers(t *testing.T) {
	// Given
	value := newTransportMediaFixture(t, playback.RendererKindCustom)
	server := newTrustedTransportTLSServer(t, value, trustedIPv4TransportServer(api.Config{
		K17HTTPEnabled: true, K17MediaBaseURL: "http://127.0.0.1:41001", K17MediaListenerAddress: "127.0.0.1:41001",
	}))

	// When
	startTransportWithHost(t, server, value.controller, "attacker.invalid:9443", "forwarded-attacker.invalid:9554")
	commands, err := value.fixture.store.PendingOutbox(context.Background(), "transport")
	if err != nil || len(commands) != 1 {
		t.Fatalf("pending command = %+v (%v)", commands, err)
	}
	command, err := value.fixture.store.DurableCommand(context.Background(), commands[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Media json.RawMessage `json:"media"`
	}
	if err := json.Unmarshal(command.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var issued struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(payload.Media, &issued); err != nil {
		t.Fatal(err)
	}

	// Then
	wantPrefix := server.URL + "/api/v1/renderers/" + string(value.rendererID) + "/media/"
	if !strings.HasPrefix(issued.URL, wantPrefix) {
		t.Fatalf("custom media URL = %q, want renderer origin %q", issued.URL, wantPrefix)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, issued.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+value.rendererToken)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "transport-media" {
		t.Fatalf("custom media pull = %d %q", response.StatusCode, body)
	}
}
