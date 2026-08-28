package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type closeRecorder struct{ closed bool }

func (recorder *closeRecorder) Close() error {
	recorder.closed = true
	return nil
}

func pairRole(t *testing.T, value fixture, role security.Role, name string) security.Credential {
	t.Helper()
	generated := request(t, value.handler, http.MethodPost, "/api/v1/pairing-codes", value.admin.Token, `{"role":"`+string(role)+`"}`, nil)
	if generated.Code != http.StatusCreated {
		t.Fatalf("generate %s code = %d %s", role, generated.Code, generated.Body.String())
	}
	var code security.PairingCode
	if err := json.Unmarshal(generated.Body.Bytes(), &code); err != nil {
		t.Fatalf("decode pairing code: %v", err)
	}
	paired := request(t, value.handler, http.MethodPost, "/api/v1/pairings", "", `{"code":"`+code.Value+`","name":"`+name+`"}`, nil)
	if paired.Code != http.StatusCreated {
		t.Fatalf("pair %s = %d %s", role, paired.Code, paired.Body.String())
	}
	var credential security.Credential
	if err := json.Unmarshal(paired.Body.Bytes(), &credential); err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	return credential
}

func TestPairing_role_is_bound_at_creation_and_consumption_cannot_escalate(t *testing.T) {
	// Given
	value := newFixture(t)
	generated := request(t, value.handler, http.MethodPost, "/api/v1/pairing-codes", value.admin.Token, `{"role":"renderer"}`, nil)
	if generated.Code != http.StatusCreated {
		t.Fatalf("generate = %d %s", generated.Code, generated.Body.String())
	}
	var code security.PairingCode
	if err := json.Unmarshal(generated.Body.Bytes(), &code); err != nil {
		t.Fatalf("decode code: %v", err)
	}

	// When
	escalation := request(t, value.handler, http.MethodPost, "/api/v1/pairings", "", `{"code":"`+code.Value+`","name":"Renderer","role":"admin"}`, nil)
	paired := request(t, value.handler, http.MethodPost, "/api/v1/pairings", "", `{"code":"`+code.Value+`","name":"Renderer"}`, nil)

	// Then
	if escalation.Code != http.StatusBadRequest || responseCode(t, escalation) != "INVALID_REQUEST" {
		t.Fatalf("escalation = %d %s", escalation.Code, escalation.Body.String())
	}
	if paired.Code != http.StatusCreated || !strings.Contains(paired.Body.String(), `"role":"renderer"`) {
		t.Fatalf("paired = %d %s", paired.Code, paired.Body.String())
	}
}

func TestRoleIsolation_renderer_cannot_use_control_or_admin_and_controller_cannot_use_renderer_route(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	renderer := pairRole(t, value, security.RoleRenderer, "Renderer")
	routes := api.NewRendererZoneAPI(value.manager, value.store)

	// When
	rendererControl := request(t, value.handler, http.MethodGet, "/api/v1/discovery", renderer.Token, "", nil)
	rendererAdmin := request(t, value.handler, http.MethodGet, "/api/v1/devices", renderer.Token, "", nil)
	controllerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/renderers/"+string(renderer.Device.ID)+"/session", nil)
	controllerRequest.Header.Set("Authorization", "Bearer "+controller.Token)
	controllerSession := httptest.NewRecorder()
	controllerRequest.SetPathValue("rendererID", string(renderer.Device.ID))
	routes.AuthorizeRendererSession(controllerSession, controllerRequest)
	controllerMedia := httptest.NewRecorder()
	routes.AuthorizeRendererMedia(controllerMedia, controllerRequest)
	rendererRequest := httptest.NewRequest(http.MethodGet, "/api/v1/renderers/"+string(renderer.Device.ID)+"/session", nil)
	rendererRequest.Header.Set("Authorization", "Bearer "+renderer.Token)
	rendererRequest.SetPathValue("rendererID", string(renderer.Device.ID))
	rendererSession := httptest.NewRecorder()
	routes.AuthorizeRendererSession(rendererSession, rendererRequest)

	// Then
	for name, response := range map[string]*httptest.ResponseRecorder{
		"renderer control": rendererControl, "renderer admin": rendererAdmin,
		"controller session": controllerSession, "controller media": controllerMedia,
	} {
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body.String())
		}
	}
	if rendererSession.Code != http.StatusUpgradeRequired || responseCode(t, rendererSession) != "WSS_REQUIRED" {
		t.Fatalf("renderer session authorization = %d %s", rendererSession.Code, rendererSession.Body.String())
	}
}

func TestLastAdminRevocation_returns_typed_conflict_without_mutation(t *testing.T) {
	// Given
	value := newFixture(t)

	// When
	response := request(t, value.handler, http.MethodDelete, "/api/v1/devices/"+string(value.admin.Device.ID), value.admin.Token, "", nil)

	// Then
	if response.Code != http.StatusConflict || responseCode(t, response) != "LAST_ADMIN" {
		t.Fatalf("last-admin response = %d %s", response.Code, response.Body.String())
	}
	if _, err := value.manager.Authenticate(context.Background(), value.admin.Token); err != nil {
		t.Fatalf("last admin was mutated: %v", err)
	}
}

func TestRendererZoneAPI_assigns_revisioned_inventory_and_revoke_suspends_resources(t *testing.T) {
	// Given
	value := newFixture(t)
	renderer := pairRole(t, value, security.RoleRenderer, "Renderer")
	routes := api.NewRendererZoneAPI(value.manager, value.store)
	resource := &closeRecorder{}
	routes.TrackRendererResource(playback.RendererID(renderer.Device.ID), resource)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/zones", strings.NewReader(`{"zone_id":"living","name":"Living room"}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Authorization", "Bearer "+value.admin.Token)
	created := httptest.NewRecorder()
	routes.CreateZone(created, create)
	assign := httptest.NewRequest(http.MethodPut, "/api/v1/zones/living/renderer", strings.NewReader(`{"renderer_id":"`+string(renderer.Device.ID)+`"}`))
	assign.Header.Set("Content-Type", "application/json")
	assign.Header.Set("Authorization", "Bearer "+value.admin.Token)
	assign.Header.Set("Idempotency-Key", "assign-living")
	assign.Header.Set("If-Match", "0")
	assign.SetPathValue("zoneID", "living")
	assigned := httptest.NewRecorder()
	routes.AssignRenderer(assigned, assign)

	// When
	revoke := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/"+string(renderer.Device.ID), nil)
	revoke.Header.Set("Authorization", "Bearer "+value.admin.Token)
	revoke.SetPathValue("deviceID", string(renderer.Device.ID))
	revoked := httptest.NewRecorder()
	routes.RevokeDevice(revoked, revoke)
	snapshot, err := value.store.Zones(context.Background())

	// Then
	if created.Code != http.StatusCreated || assigned.Code != http.StatusOK || assigned.Header().Get("ETag") != `"1"` {
		t.Fatalf("create/assign = %d/%d ETag=%q %s", created.Code, assigned.Code, assigned.Header().Get("ETag"), assigned.Body.String())
	}
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	if err != nil {
		t.Fatalf("zones: %v", err)
	}
	if snapshot.Zones[0].RendererID != "" || snapshot.Zones[0].Transport != playback.TransportSuspended {
		t.Fatalf("zone after revoke = %+v", snapshot.Zones[0])
	}
	if !resource.closed {
		t.Fatal("renderer resource remained open")
	}
	if _, err := value.manager.Authenticate(context.Background(), renderer.Token); !errors.Is(err, security.ErrTokenRevoked) {
		t.Fatalf("renderer token after revoke = %v", err)
	}
}
