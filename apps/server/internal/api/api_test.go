package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakestreamer/jstreamer-server/internal/api"
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
	"github.com/jakestreamer/jstreamer-server/internal/security"
)

type apiClock struct{ now time.Time }

func (clock *apiClock) Now() time.Time { return clock.now }

type fixture struct {
	handler http.Handler
	manager *security.Manager
	admin   security.Credential
	store   *playback.Store
	catalog *catalog.Snapshot
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	clock := &apiClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: filepath.Join(t.TempDir(), "security.json"), Clock: clock})
	if err != nil {
		t.Fatalf("security: %v", err)
	}
	admin, err := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	directory := t.TempDir()
	store, err := playback.Open(context.Background(), playback.Config{
		Path: filepath.Join(directory, "playback.sqlite"), MigrationPath: "../../migrations/002_playback.sql",
		ExpansionPath: "../../migrations/003_todo12.sql", BackupDirectory: filepath.Join(directory, "backups"),
		SupportedSchema: playback.CurrentSchemaVersion,
		JournalMode:     playback.JournalRollback,
	})
	if err != nil {
		t.Fatalf("playback: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	snapshot := catalog.EmptySnapshot()
	snapshot.Revision = 7
	snapshot.Tracks["track-a"] = catalog.Track{TrackID: "track-a", Available: true, AnalysisStatus: catalog.AnalysisComplete}
	handler := api.New(api.Config{
		Security: manager, Queue: store, Catalog: snapshot, CertificateFingerprint: strings.Repeat("a", 64),
		AllowedOrigins: []string{"https://control.fixture.invalid"},
		LoadCatalog:    func(context.Context) (catalog.Snapshot, error) { return snapshot, nil },
		Scan: func(_ context.Context, previous catalog.Snapshot) (catalog.Snapshot, error) {
			previous.Revision++
			snapshot = previous
			return previous, nil
		},
	})
	return fixture{handler: handler, manager: manager, admin: admin, store: store, catalog: &snapshot}
}

func request(t *testing.T, handler http.Handler, method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	handler.ServeHTTP(recorder, req)
	return recorder
}

func responseCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return body.Code
}

func pairController(t *testing.T, value fixture) security.Credential {
	t.Helper()
	code, err := value.manager.GeneratePairingCode(context.Background(), value.admin.Token)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	credential, err := value.manager.Pair(context.Background(), code.Value, security.Registration{Name: "Controller"}, "test")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return credential
}

func TestHealth_and_identity_are_public_but_discovery_requires_supported_authenticated_protocol(t *testing.T) {
	// Given
	value := newFixture(t)

	// When
	health := request(t, value.handler, http.MethodGet, "/healthz", "", "", nil)
	identity := request(t, value.handler, http.MethodGet, "/api/v1/identity", "", "", nil)
	unauthorized := request(t, value.handler, http.MethodGet, "/api/v1/discovery", "", "", nil)
	unsupported := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", map[string]string{"X-Jake-Protocol-Major": "9"})

	// Then
	if health.Code != http.StatusOK || health.Body.String() != "{\"status\":\"ready\"}\n" {
		t.Fatalf("health = %d %s", health.Code, health.Body.String())
	}
	if identity.Code != http.StatusOK || !strings.Contains(identity.Body.String(), strings.Repeat("a", 64)) ||
		!strings.Contains(identity.Body.String(), `"pairing_url":"/pair/"`) ||
		identity.Header().Get("Cache-Control") != "no-store" || identity.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("identity = %d %s headers=%v", identity.Code, identity.Body.String(), identity.Header())
	}
	if !strings.Contains(identity.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", identity.Header().Get("Content-Security-Policy"))
	}
	if unauthorized.Code != http.StatusUnauthorized || responseCode(t, unauthorized) != "UNAUTHORIZED" {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	if unsupported.Code != http.StatusUpgradeRequired || responseCode(t, unsupported) != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("unsupported = %d %s", unsupported.Code, unsupported.Body.String())
	}
}

func TestPairing_endpoints_enforce_admin_role_single_use_and_revocation(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)

	// When
	forbidden := request(t, value.handler, http.MethodPost, "/api/v1/pairing-codes", controller.Token, "{}", nil)
	generated := request(t, value.handler, http.MethodPost, "/api/v1/pairing-codes", value.admin.Token, "{}", nil)
	var code struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(generated.Body.Bytes(), &code); err != nil {
		t.Fatalf("decode code: %v", err)
	}
	paired := request(t, value.handler, http.MethodPost, "/api/v1/pairings", "", `{"code":"`+code.Code+`","name":"Tablet"}`, nil)
	replayed := request(t, value.handler, http.MethodPost, "/api/v1/pairings", "", `{"code":"`+code.Code+`","name":"Replay"}`, nil)
	revoked := request(t, value.handler, http.MethodDelete, "/api/v1/devices/"+string(controller.Device.ID), value.admin.Token, "", nil)
	afterRevoke := request(t, value.handler, http.MethodGet, "/api/v1/discovery", controller.Token, "", nil)

	// Then
	if forbidden.Code != http.StatusForbidden || responseCode(t, forbidden) != "ADMIN_REQUIRED" {
		t.Fatalf("forbidden = %d %s", forbidden.Code, forbidden.Body.String())
	}
	if generated.Code != http.StatusCreated || paired.Code != http.StatusCreated {
		t.Fatalf("generate/pair = %d/%d", generated.Code, paired.Code)
	}
	if replayed.Code != http.StatusConflict || responseCode(t, replayed) != "PAIRING_CODE_USED" {
		t.Fatalf("replay = %d %s", replayed.Code, replayed.Body.String())
	}
	if revoked.Code != http.StatusNoContent || afterRevoke.Code != http.StatusUnauthorized || responseCode(t, afterRevoke) != "TOKEN_REVOKED" {
		t.Fatalf("revoke = %d, after = %d %s", revoked.Code, afterRevoke.Code, afterRevoke.Body.String())
	}
}

func TestQueue_requires_identity_idempotency_and_matching_revision_without_stale_mutation(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	body := `{"tracks":[{"track_id":"track-a","available":true}]}`

	// When
	unauthorized := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", "", body, map[string]string{"Idempotency-Key": "one", "If-Match": "0"})
	created := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token, body, map[string]string{"Idempotency-Key": "one", "If-Match": "0"})
	replayed := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token, body, map[string]string{"Idempotency-Key": "one", "If-Match": "0"})
	stale := request(t, value.handler, http.MethodPost, "/api/v1/zones/main/queue", controller.Token, body, map[string]string{"Idempotency-Key": "two", "If-Match": "0"})
	state := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/playback-state", controller.Token, "", nil)

	// Then
	if unauthorized.Code != http.StatusUnauthorized || created.Code != http.StatusCreated || replayed.Code != http.StatusOK {
		t.Fatalf("statuses = %d/%d/%d", unauthorized.Code, created.Code, replayed.Code)
	}
	if stale.Code != http.StatusConflict || responseCode(t, stale) != "STALE_REVISION" {
		t.Fatalf("stale = %d %s", stale.Code, stale.Body.String())
	}
	if strings.Count(state.Body.String(), "track-a") != 1 {
		t.Fatalf("state mutated by replay/stale: %s", state.Body.String())
	}
}

func TestPolicy_patch_uses_IfMatch_and_preserves_state_on_stale_request(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)
	body := `{"mode":"similar","artist_gap":3,"album_gap":8,"session_override":"album"}`

	// When
	updated := request(t, value.handler, http.MethodPatch, "/api/v1/zones/main/continuation-policy", controller.Token, body, map[string]string{"If-Match": "0"})
	stale := request(t, value.handler, http.MethodPatch, "/api/v1/zones/main/continuation-policy", controller.Token, `{"mode":"stop"}`, map[string]string{"If-Match": "0"})
	current := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/continuation-policy", controller.Token, "", nil)

	// Then
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != `"1"` {
		t.Fatalf("updated = %d ETag=%q %s", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	if stale.Code != http.StatusPreconditionFailed || responseCode(t, stale) != "STALE_POLICY_REVISION" {
		t.Fatalf("stale = %d %s", stale.Code, stale.Body.String())
	}
	if !strings.Contains(current.Body.String(), `"mode":"similar"`) || !strings.Contains(current.Body.String(), `"session_override":"album"`) {
		t.Fatalf("policy changed after stale request: %s", current.Body.String())
	}
}

func TestCatalog_preview_and_decision_contracts_expose_revisions_coverage_and_reasons(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)

	// When
	catalogStatus := request(t, value.handler, http.MethodGet, "/api/v1/catalog/status", controller.Token, "", nil)
	preview := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/automatic-preview", controller.Token, "", nil)
	decision := request(t, value.handler, http.MethodGet, "/api/v1/zones/main/decision-explanation", controller.Token, "", nil)

	// Then
	for name, response := range map[string]*httptest.ResponseRecorder{"catalog": catalogStatus, "preview": preview, "decision": decision} {
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body.String())
		}
	}
	if !strings.Contains(catalogStatus.Body.String(), `"catalog_revision":7`) || !strings.Contains(catalogStatus.Body.String(), `"analysis_coverage":100`) {
		t.Fatalf("catalog status = %s", catalogStatus.Body.String())
	}
	if !strings.Contains(preview.Body.String(), `"replaceable":true`) || !strings.Contains(decision.Body.String(), `"reason":"STOP_MODE_OFF"`) {
		t.Fatalf("preview/decision = %s / %s", preview.Body.String(), decision.Body.String())
	}
}

func TestStateStream_requires_websocket_upgrade(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/events", controller.Token, "", nil)

	// Then
	if response.Code != http.StatusUpgradeRequired || responseCode(t, response) != "WEBSOCKET_UPGRADE_REQUIRED" {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogScan_requires_admin_and_updates_catalog_revision(t *testing.T) {
	// Given
	value := newFixture(t)
	controller := pairController(t, value)

	// When
	forbidden := request(t, value.handler, http.MethodPost, "/api/v1/catalog/scans", controller.Token, "{}", nil)
	accepted := request(t, value.handler, http.MethodPost, "/api/v1/catalog/scans", value.admin.Token, "{}", nil)
	status := request(t, value.handler, http.MethodGet, "/api/v1/catalog/status", value.admin.Token, "", nil)

	// Then
	if forbidden.Code != http.StatusForbidden || accepted.Code != http.StatusAccepted {
		t.Fatalf("scan statuses = %d/%d", forbidden.Code, accepted.Code)
	}
	if !strings.Contains(status.Body.String(), `"catalog_revision":8`) {
		t.Fatalf("catalog status = %s", status.Body.String())
	}
}
