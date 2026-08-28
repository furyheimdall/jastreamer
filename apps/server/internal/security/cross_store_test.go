package security_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestRendererPairing_does_not_authorize_until_inventory_is_ready_and_token_is_presented(t *testing.T) {
	// Given
	ctx := context.Background()
	clock := &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	admin, err := manager.Bootstrap(ctx, "installer-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCodeForRole(ctx, admin.Token, security.RoleRenderer)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	credential, err := manager.Pair(ctx, code.Value, security.Registration{Name: "Renderer"}, "peer")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// When
	_, beforeErr := manager.Authenticate(ctx, credential.Token)
	readyErr := manager.MarkRendererInventoryReady(ctx, credential.Device.ID)
	device, afterErr := manager.Authenticate(ctx, credential.Token)

	// Then
	if !errors.Is(beforeErr, security.ErrCredentialPending) {
		t.Fatalf("authentication before inventory = %v", beforeErr)
	}
	if readyErr != nil || afterErr != nil || device.ID != credential.Device.ID {
		t.Fatalf("ready/authenticate = %v/%v device=%+v", readyErr, afterErr, device)
	}
}

func TestRendererPairing_restart_exposes_token_free_pending_operation_for_compensation(t *testing.T) {
	// Given
	ctx := context.Background()
	clock := &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	statePath := filepath.Join(t.TempDir(), "security.json")
	config := security.Config{SetupSecret: "setup", StatePath: statePath, Clock: clock}
	manager, err := security.NewManager(config)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	admin, err := manager.Bootstrap(ctx, "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCodeForRole(ctx, admin.Token, security.RoleRenderer)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	credential, err := manager.Pair(ctx, code.Value, security.Registration{Name: "Renderer"}, "peer")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// When
	restarted, err := security.NewManager(config)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	operations, err := restarted.PendingRendererOperations(ctx)

	// Then
	if err != nil || len(operations) != 1 || operations[0].Device.ID != credential.Device.ID || operations[0].Kind != security.RendererOperationPair {
		t.Fatalf("operations = %+v, err=%v", operations, err)
	}
}
