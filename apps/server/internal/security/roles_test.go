package security_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestPairingCode_consumption_uses_bound_renderer_role_after_restart(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	statePath := filepath.Join(t.TempDir(), "security.json")
	manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: statePath, Clock: clock})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	admin, err := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCodeForRole(context.Background(), admin.Token, security.RoleRenderer)
	if err != nil {
		t.Fatalf("generate renderer code: %v", err)
	}
	restarted, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: statePath, Clock: clock})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}

	// When
	credential, err := restarted.Pair(context.Background(), code.Value, security.Registration{Name: "Renderer"}, "renderer-peer")
	// Then
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if credential.Device.Role != security.RoleRenderer {
		t.Fatalf("role = %q", credential.Device.Role)
	}
}

func TestPairingCode_rejects_unknown_target_role_without_creating_code(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	admin, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// When
	_, err = manager.GeneratePairingCodeForRole(context.Background(), admin.Token, security.Role("owner"))

	// Then
	if !errors.Is(err, security.ErrInvalidRole) {
		t.Fatalf("error = %v", err)
	}
}

func TestRevoke_rejects_last_active_admin_without_mutation(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	admin, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// When
	err = manager.Revoke(context.Background(), admin.Token, admin.Device.ID)

	// Then
	if !errors.Is(err, security.ErrLastAdmin) {
		t.Fatalf("revoke error = %v", err)
	}
	if _, err := manager.Authenticate(context.Background(), admin.Token); err != nil {
		t.Fatalf("last admin was mutated: %v", err)
	}
}

func TestRevoke_allows_admin_when_another_active_admin_exists(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	first, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "First"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCodeForRole(context.Background(), first.Token, security.RoleAdmin)
	if err != nil {
		t.Fatalf("generate admin code: %v", err)
	}
	second, err := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Second"}, "admin-peer")
	if err != nil {
		t.Fatalf("pair second admin: %v", err)
	}

	// When
	err = manager.Revoke(context.Background(), first.Token, second.Device.ID)
	// Then
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := manager.Authenticate(context.Background(), second.Token); !errors.Is(err, security.ErrTokenRevoked) {
		t.Fatalf("revoked admin authentication = %v", err)
	}
}
