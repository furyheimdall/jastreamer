package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
)

func TestNew_serves_separate_admin_assets_without_changing_pairing_mount(t *testing.T) {
	// Given
	value := newFixture(t)
	handler := api.New(api.Config{Security: value.manager, Queue: value.store})

	// When
	adminRedirect := request(t, handler, http.MethodGet, "/admin", "", "", nil)
	adminIndex := request(t, handler, http.MethodGet, "/admin/", "", "", nil)
	adminScript := request(t, handler, http.MethodGet, "/admin/app.js", "", "", nil)
	pairing := request(t, handler, http.MethodGet, "/pair/", "", "", nil)

	// Then
	if adminRedirect.Code != http.StatusTemporaryRedirect || adminRedirect.Header().Get("Location") != "/admin/" {
		t.Fatalf("admin redirect = %d %q", adminRedirect.Code, adminRedirect.Header().Get("Location"))
	}
	if adminIndex.Code != http.StatusOK || !strings.Contains(adminIndex.Body.String(), `id="login-form"`) {
		t.Fatalf("admin index = %d %s", adminIndex.Code, adminIndex.Body.String())
	}
	if adminScript.Code != http.StatusOK || !strings.Contains(adminScript.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("admin script = %d content-type=%q", adminScript.Code, adminScript.Header().Get("Content-Type"))
	}
	if pairing.Code != http.StatusNotFound {
		t.Fatalf("pairing mount unexpectedly changed = %d", pairing.Code)
	}
}
