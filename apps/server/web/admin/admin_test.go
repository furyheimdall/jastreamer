package admin_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/web/admin"
)

func TestAssets_define_authenticated_management_surface(t *testing.T) {
	// Given
	index, err := fs.ReadFile(admin.Assets, "index.html")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	javascript, err := fs.ReadFile(admin.Assets, "app.js")
	if err != nil {
		t.Fatalf("read app: %v", err)
	}
	apiModule, err := fs.ReadFile(admin.Assets, "api.js")
	if err != nil {
		t.Fatalf("read API module: %v", err)
	}

	// When
	markup := string(index)
	behavior := string(javascript) + string(apiModule)

	// Then
	for _, id := range []string{"login-form", "settings-form", "catalog-roots", "scan-jobs", "renderer-inventory", "renderer-message", "device-list", "conflict-panel", "restart-banner"} {
		if !strings.Contains(markup, `id="`+id+`"`) {
			t.Fatalf("missing management region %q", id)
		}
	}
	for _, route := range []string{"/api/v1/config", "/api/v1/catalog/roots", "/api/v1/catalog/scans", "/api/v1/zones", "/api/v1/devices"} {
		if !strings.Contains(behavior, route) {
			t.Fatalf("missing API route %q", route)
		}
	}
	if !strings.Contains(behavior, "sessionStorage") || strings.Contains(behavior, "localStorage") {
		t.Fatal("administrator credential storage is not session-only")
	}
}

func TestAssets_expose_navigation_and_error_focus_state(t *testing.T) {
	// Given
	index, err := fs.ReadFile(admin.Assets, "index.html")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	stylesheet, err := fs.ReadFile(admin.Assets, "style.css")
	if err != nil {
		t.Fatalf("read stylesheet: %v", err)
	}

	// When
	markup := string(index)
	styles := string(stylesheet)

	// Then
	if strings.Count(markup, `aria-current="location"`) != 1 {
		t.Fatal("initial navigation must expose exactly one current location")
	}
	if !strings.Contains(markup, `id="login-error" class="message error" aria-live="assertive" tabindex="-1"`) {
		t.Fatal("authentication error target is not programmatically focusable")
	}
	if !strings.Contains(styles, `.section-nav a[aria-current="location"]`) {
		t.Fatal("current navigation location has no token-driven style")
	}
}

func TestAssets_keep_locked_fields_and_conflict_reapply_machine_addressable(t *testing.T) {
	// Given
	index, err := fs.ReadFile(admin.Assets, "index.html")
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	javascript, err := fs.ReadFile(admin.Assets, "app.js")
	if err != nil {
		t.Fatalf("read app: %v", err)
	}

	// When
	markup := string(index)
	behavior := string(javascript)

	// Then
	for _, field := range []string{"listen-address", "certificate-fingerprint", "data-directory"} {
		if !strings.Contains(markup, `id="`+field+`"`) || !strings.Contains(markup, "readonly") {
			t.Fatalf("locked field %q is not visibly read-only", field)
		}
	}
	for _, token := range []string{"STALE_CONFIG_REVISION", "pendingIntent", "environment_locked_fields", "restart_required"} {
		if !strings.Contains(behavior, token) {
			t.Fatalf("missing state behavior %q", token)
		}
	}
}
