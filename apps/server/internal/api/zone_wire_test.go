package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func Test_ZonesResponse_matches_canonical_v3_ServerControl_fixture(t *testing.T) {
	// Given: actual persisted Server zones, assignment, and Renderer state.
	value := newFixture(t)
	controller := pairRole(t, value, security.RoleController, "Controller")
	observedAt := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	if _, err := value.store.UpsertCustomRenderer(t.Context(), playback.CustomRenderer{
		ID: "renderer-1", DisplayName: "Fixture Renderer", State: playback.RendererConnected,
		ProtocolMajor: 3, Capabilities: []string{"media:audio/flac", "command:play", "command:pause"},
		LastSeenAt: observedAt,
	}); err != nil {
		t.Fatalf("upsert Renderer: %v", err)
	}
	for _, zone := range []playback.ZoneDefinition{
		{ID: "living", DisplayName: "Living room"},
		{ID: "unassigned", DisplayName: "Unassigned"},
	} {
		if _, err := value.store.CreateZone(t.Context(), zone); err != nil {
			t.Fatalf("create zone: %v", err)
		}
	}
	if _, err := value.store.AssignRenderer(t.Context(), playback.AssignmentRequest{
		ZoneID: "living", RendererID: "renderer-1", ExpectedRevision: 0,
	}); err != nil {
		t.Fatalf("assign Renderer: %v", err)
	}
	routes := api.NewRendererZoneAPI(value.manager, value.store)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	request.Header.Set("Authorization", "Bearer "+controller.Token)
	response := httptest.NewRecorder()

	// When: the real Server handler serializes its response.
	routes.ListZones(response, request)

	// Then: its machine values equal the one shared v3 fixture exactly.
	fixture, err := os.ReadFile("../../../../contracts/control-api/v3/fixtures/zones-snapshot.json")
	if err != nil {
		t.Fatalf("read canonical fixture: %v", err)
	}
	var actual, expected any
	if err := json.NewDecoder(strings.NewReader(response.Body.String())).Decode(&actual); err != nil {
		t.Fatalf("decode Server response: %v", err)
	}
	if err := json.Unmarshal(fixture, &expected); err != nil {
		t.Fatalf("decode canonical fixture: %v", err)
	}
	if response.Code != http.StatusOK || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("zones response = %d %s, want %s", response.Code, response.Body.String(), fixture)
	}
	var inventory struct {
		Zones []struct {
			ZoneID     string  `json:"zone_id"`
			RendererID *string `json:"renderer_id"`
		} `json:"zones"`
		Renderers []struct {
			RendererID string `json:"renderer_id"`
		} `json:"renderers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatalf("decode inventory identities: %v", err)
	}
	rendererIDs := make(map[string]struct{}, len(inventory.Renderers))
	for _, renderer := range inventory.Renderers {
		if _, duplicate := rendererIDs[renderer.RendererID]; duplicate {
			t.Fatalf("duplicate renderer_id %q", renderer.RendererID)
		}
		rendererIDs[renderer.RendererID] = struct{}{}
	}
	zoneIDs := make(map[string]struct{}, len(inventory.Zones))
	for _, zone := range inventory.Zones {
		if _, duplicate := zoneIDs[zone.ZoneID]; duplicate {
			t.Fatalf("duplicate zone_id %q", zone.ZoneID)
		}
		zoneIDs[zone.ZoneID] = struct{}{}
		if zone.RendererID != nil {
			if _, found := rendererIDs[*zone.RendererID]; !found {
				t.Fatalf("dangling renderer assignment %q", *zone.RendererID)
			}
		}
	}
}

func Test_ZonesResponse_emits_empty_capability_array_for_unavailable_renderer(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairRole(t, value, security.RoleController, "Controller")
	pairRole(t, value, security.RoleRenderer, "Unavailable Renderer")
	routes := api.NewRendererZoneAPI(value.manager, value.store)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	request.Header.Set("Authorization", "Bearer "+controller.Token)
	response := httptest.NewRecorder()

	// When
	routes.ListZones(response, request)

	// Then
	var inventory struct {
		Renderers []struct {
			Capabilities json.RawMessage `json:"capabilities"`
		} `json:"renderers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatalf("decode Renderer inventory: %v", err)
	}
	if response.Code != http.StatusOK || len(inventory.Renderers) != 1 ||
		string(inventory.Renderers[0].Capabilities) != "[]" {
		t.Fatalf("zones response = %d %s", response.Code, response.Body.String())
	}
}
