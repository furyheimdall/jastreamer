package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

type configFixture struct {
	handler http.Handler
	store   *settings.Store
	manager *security.Manager
	admin   security.Credential
	control security.Credential
}

func newConfigFixture(t *testing.T, lockedFields ...string) configFixture {
	t.Helper()
	directory := t.TempDir()
	media := filepath.Join(directory, "media")
	if err := os.MkdirAll(filepath.Join(media, "music"), 0o750); err != nil {
		t.Fatalf("create media: %v", err)
	}
	store, err := settings.Open(settings.Config{
		Path:     filepath.Join(directory, "config", "settings.json"),
		Defaults: settings.Values{DisplayName: "Jake Streamer", PairingTTLSeconds: 300},
		Locks: settings.Locks{
			ListenAddress: ":8443", CertificateFingerprint: strings.Repeat("a", 64),
			CertificateSANs: []string{"localhost"}, DataDirectory: directory,
			AllowedCatalogBases: []string{media}, Environment: "test", EnvironmentLockedFields: lockedFields,
		},
	})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: filepath.Join(directory, "security", "state.json")})
	if err != nil {
		t.Fatalf("security: %v", err)
	}
	admin, err := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("pairing code: %v", err)
	}
	control, err := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Control"}, "fixture")
	if err != nil {
		t.Fatalf("pair controller: %v", err)
	}
	return configFixture{handler: api.NewConfigHandler(store, manager), store: store, manager: manager, admin: admin, control: control}
}

func configRequest(t *testing.T, fixture configFixture, method, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/v1/config", strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	return response
}

func TestConfigHandler_GET_is_admin_only_redacted_and_emits_strong_decimal_ETag(t *testing.T) {
	fixture := newConfigFixture(t)

	unauthorized := configRequest(t, fixture, http.MethodGet, "", "", nil)
	forbidden := configRequest(t, fixture, http.MethodGet, fixture.control.Token, "", nil)
	response := configRequest(t, fixture, http.MethodGet, fixture.admin.Token, "", nil)

	if unauthorized.Code != http.StatusUnauthorized || forbidden.Code != http.StatusForbidden || response.Code != http.StatusOK {
		t.Fatalf("statuses = %d/%d/%d", unauthorized.Code, forbidden.Code, response.Code)
	}
	if response.Header().Get("ETag") != `"0"` {
		t.Fatalf("ETag = %q", response.Header().Get("ETag"))
	}
	body := response.Body.String()
	for _, forbiddenValue := range []string{"setup_secret", "tls_private_key", fixture.admin.Token} {
		if strings.Contains(body, forbiddenValue) {
			t.Fatalf("response exposes %q: %s", forbiddenValue, body)
		}
	}
	if !strings.Contains(body, `"listen_address":":8443"`) || !strings.Contains(body, `"data_directory":`) {
		t.Fatalf("response omits runtime locks: %s", body)
	}
}

func TestConfigHandler_PATCH_requires_non_empty_idempotency_key_with_zero_mutation(t *testing.T) {
	// Given
	fixture := newConfigFixture(t)
	body := `{"display_name":"Updated"}`

	// When
	missing := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, map[string]string{"If-Match": `"0"`})
	blank := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, map[string]string{"If-Match": `"0"`, "Idempotency-Key": "   "})

	// Then
	for _, response := range []*httptest.ResponseRecorder{missing, blank} {
		var failure struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Code != http.StatusPreconditionRequired || failure.Code != "IDEMPOTENCY_KEY_REQUIRED" {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}
	if fixture.store.Snapshot().Revision != 0 {
		t.Fatalf("missing key mutated settings: %+v", fixture.store.Snapshot())
	}
}

func TestConfigHandler_PATCH_requires_exact_matching_ETag_with_zero_mutation(t *testing.T) {
	fixture := newConfigFixture(t)
	body := `{"display_name":"Updated"}`

	missing := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, map[string]string{"Idempotency-Key": "missing-revision"})
	malformed := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, map[string]string{"If-Match": "0", "Idempotency-Key": "malformed-revision"})
	stale := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, map[string]string{"If-Match": `"1"`, "Idempotency-Key": "stale-revision"})

	if missing.Code != http.StatusPreconditionRequired || malformed.Code != http.StatusBadRequest || stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("statuses = %d/%d/%d", missing.Code, malformed.Code, stale.Code)
	}
	if fixture.store.Snapshot().Revision != 0 || fixture.store.Snapshot().Settings.DisplayName != "Jake Streamer" {
		t.Fatalf("rejected requests mutated settings: %+v", fixture.store.Snapshot())
	}
}

func TestConfigHandler_PATCH_replays_idempotently_and_stages_restart(t *testing.T) {
	fixture := newConfigFixture(t)
	headers := map[string]string{"If-Match": `"0"`, "Idempotency-Key": "ttl-update"}
	body := `{"pairing_ttl_seconds":600}`

	first := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, headers)
	replay := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, headers)

	if first.Code != http.StatusOK || replay.Code != http.StatusOK || first.Header().Get("ETag") != `"1"` || replay.Header().Get("ETag") != `"1"` {
		t.Fatalf("responses = %d/%q and %d/%q", first.Code, first.Header().Get("ETag"), replay.Code, replay.Header().Get("ETag"))
	}
	var payload struct {
		Revision        uint64   `json:"revision"`
		RestartRequired bool     `json:"restart_required"`
		RestartFields   []string `json:"restart_fields"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Revision != 1 || !payload.RestartRequired || len(payload.RestartFields) != 1 || payload.RestartFields[0] != "pairing_ttl_seconds" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestConfigHandler_PATCH_replay_returns_exact_original_response_and_conflict_has_zero_mutation(t *testing.T) {
	// Given
	fixture := newConfigFixture(t)
	firstHeaders := map[string]string{"If-Match": `"0"`, "Idempotency-Key": "original"}
	firstBody := `{"pairing_ttl_seconds":600}`
	first := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, firstBody, firstHeaders)
	second := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, `{"display_name":"Current"}`, map[string]string{
		"If-Match": `"1"`, "Idempotency-Key": "second",
	})
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("setup responses = %d/%d", first.Code, second.Code)
	}
	current := fixture.store.Snapshot()
	restarted, err := settings.Open(settings.Config{
		Path: filepath.Join(current.Locks.DataDirectory, "config", "settings.json"), Defaults: current.Settings, Locks: current.Locks,
	})
	if err != nil {
		t.Fatalf("restart settings: %v", err)
	}
	fixture.store = restarted
	fixture.handler = api.NewConfigHandler(restarted, fixture.manager)

	// When
	replay := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, firstBody, firstHeaders)
	conflict := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, `{"display_name":"Conflict"}`, firstHeaders)

	// Then
	if replay.Code != first.Code || replay.Header().Get("ETag") != first.Header().Get("ETag") || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay differs: first=%d %q %s replay=%d %q %s", first.Code, first.Header().Get("ETag"), first.Body.String(), replay.Code, replay.Header().Get("ETag"), replay.Body.String())
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Code != http.StatusConflict || failure.Code != "IDEMPOTENCY_CONFLICT" || fixture.store.Snapshot().Revision != 2 || fixture.store.Snapshot().Settings.DisplayName != "Current" {
		t.Fatalf("conflict = %d %s snapshot=%+v", conflict.Code, conflict.Body.String(), fixture.store.Snapshot())
	}
}

func TestConfigHandler_PATCH_rejects_locked_and_secret_fields(t *testing.T) {
	fixture := newConfigFixture(t, "control_origins")
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "environment locked", body: `{"control_origins":["https://control.example"]}`, status: http.StatusConflict, code: "CONFIG_FIELD_LOCKED"},
		{name: "setup secret", body: `{"setup_secret":"must-not-accept"}`, status: http.StatusBadRequest, code: "CONFIG_FIELD_FORBIDDEN"},
		{name: "listen address", body: `{"listen_address":"127.0.0.1:9999"}`, status: http.StatusConflict, code: "CONFIG_FIELD_LOCKED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, test.body, map[string]string{"If-Match": `"0"`, "Idempotency-Key": "reject-" + test.name})
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Code != test.status || body.Code != test.code {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if fixture.store.Snapshot().Revision != 0 {
				t.Fatalf("rejected field mutated revision: %d", fixture.store.Snapshot().Revision)
			}
		})
	}
}
