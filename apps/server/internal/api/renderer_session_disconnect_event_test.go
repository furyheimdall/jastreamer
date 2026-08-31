package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestRendererCredentialRevocationAndSessionClosePublishOneAuthoritativeRevision(t *testing.T) {
	scenario := newRendererSessionScenario(t, 0)
	controller := pairController(t, scenario.fixture)
	_, welcome := scenario.openSocket(t, 0, nil)
	events := openEventSocket(t, scenario.fixture.handler, controller.Token)

	revoked := request(t, scenario.fixture.handler, http.MethodDelete, "/api/v1/devices/"+string(scenario.renderer.Device.ID), scenario.fixture.admin.Token, "", nil)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke renderer = %d %s", revoked.Code, revoked.Body.String())
	}
	event := events.readEvent(t)
	renderer, err := scenario.fixture.store.Renderer(t.Context(), playback.RendererID(scenario.renderer.Device.ID))
	if err != nil || renderer.State != playback.RendererRevoked || event["resource"] != "renderers" || event["revision"] != float64(renderer.Revision) {
		t.Fatalf("revocation event/renderer = %#v / %+v / %v", event, renderer, err)
	}
	overlap, err := scenario.fixture.store.CloseRendererSessionResult(t.Context(), playback.RendererSessionClose{
		RendererID: playback.RendererID(scenario.renderer.Device.ID), Epoch: playback.SessionEpoch(welcome.SessionEpoch), DisconnectedAt: time.Now().UTC(),
	})
	after, readErr := scenario.fixture.store.Renderer(t.Context(), playback.RendererID(scenario.renderer.Device.ID))
	if err != nil || readErr != nil || overlap.Changed || after.Revision != renderer.Revision {
		t.Fatalf("revocation overlap = %+v/%v, renderer = %+v/%v", overlap, err, after, readErr)
	}
}

func TestRendererSessionDisconnectPublishesCommittedRendererInvalidation(t *testing.T) {
	for _, test := range []struct {
		name       string
		disconnect func(*testing.T, *rendererSocket)
	}{
		{name: "abrupt socket close", disconnect: func(t *testing.T, socket *rendererSocket) {
			t.Helper()
			if err := socket.connection.Close(); err != nil {
				t.Fatalf("close renderer socket: %v", err)
			}
		}},
		{name: "clean websocket close", disconnect: func(t *testing.T, socket *rendererSocket) {
			t.Helper()
			socket.sendFrame(t, 0x8, []byte{0x03, 0xe8})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := newRendererSessionScenario(t, 0)
			controller := pairController(t, scenario.fixture)
			rendererSocket, welcome := scenario.openSocket(t, 0, nil)
			events := openEventSocket(t, scenario.fixture.handler, controller.Token)

			test.disconnect(t, rendererSocket)
			event := events.readEvent(t)
			if event["type"] != "invalidation" || event["resource"] != "renderers" {
				t.Fatalf("renderer disconnect event = %#v", event)
			}
			inventoryResponse := request(t, scenario.fixture.handler, http.MethodGet, "/api/v1/zones", controller.Token, "", nil)
			var inventory struct {
				Renderers []struct {
					ID       string `json:"renderer_id"`
					Status   string `json:"status"`
					Revision int64  `json:"revision"`
				} `json:"renderers"`
			}
			if err := json.Unmarshal(inventoryResponse.Body.Bytes(), &inventory); err != nil {
				t.Fatalf("decode renderer inventory: %v", err)
			}
			if inventoryResponse.Code != http.StatusOK || len(inventory.Renderers) != 1 || inventory.Renderers[0].Status != string(playback.RendererAvailable) {
				t.Fatalf("renderer inventory after event = %d %+v", inventoryResponse.Code, inventory.Renderers)
			}
			renderer, err := scenario.fixture.store.Renderer(t.Context(), playback.RendererID(scenario.renderer.Device.ID))
			if err != nil {
				t.Fatalf("read committed renderer: %v", err)
			}
			if event["revision"] != float64(renderer.Revision) {
				t.Fatalf("event revision = %v, committed renderer revision = %d", event["revision"], renderer.Revision)
			}
			t.Logf("disconnect_event resource=%s revision=%.0f renderer_state=%s committed_revision=%d", event["resource"], event["revision"], renderer.State, renderer.Revision)

			beforeDuplicate := renderer.Revision
			if err := scenario.fixture.store.CloseRendererSession(t.Context(), playback.RendererSessionClose{
				RendererID: playback.RendererID(scenario.renderer.Device.ID), Epoch: playback.SessionEpoch(welcome.SessionEpoch), DisconnectedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("duplicate close: %v", err)
			}
			afterDuplicate, err := scenario.fixture.store.Renderer(t.Context(), playback.RendererID(scenario.renderer.Device.ID))
			if err != nil || afterDuplicate.Revision != beforeDuplicate {
				t.Fatalf("duplicate close changed renderer: before=%d after=%d err=%v", beforeDuplicate, afterDuplicate.Revision, err)
			}
		})
	}
}
