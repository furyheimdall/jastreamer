package security_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestManager_restarts_twice_after_bootstrap_without_setup_secret_and_preserves_pairing_key(t *testing.T) {
	// Given
	statePath := filepath.Join(t.TempDir(), "security", "state.json")
	manager, err := security.NewManager(security.Config{SetupSecret: "one-time-secret", StatePath: statePath})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	admin, err := manager.Bootstrap(context.Background(), "one-time-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("generate pairing code: %v", err)
	}

	// When
	firstRestart, err := security.NewManager(security.Config{StatePath: statePath})
	if err != nil {
		t.Fatalf("first setup-free restart: %v", err)
	}
	secondRestart, err := security.NewManager(security.Config{StatePath: statePath})
	if err != nil {
		t.Fatalf("second setup-free restart: %v", err)
	}
	credential, err := secondRestart.Pair(context.Background(), code.Value, security.Registration{Name: "Control"}, "fixture")

	// Then
	if err != nil || credential.Device.Role != security.RoleController {
		t.Fatalf("pair after restart = %+v, %v", credential, err)
	}
	if _, err := firstRestart.Authenticate(context.Background(), admin.Token); err != nil {
		t.Fatalf("admin token after restart: %v", err)
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(state), "one-time-secret") || strings.Contains(string(state), code.Value) {
		t.Fatalf("security state contains bootstrap or pairing credential")
	}
}

func TestManager_requires_setup_secret_before_bootstrap(t *testing.T) {
	_, err := security.NewManager(security.Config{StatePath: filepath.Join(t.TempDir(), "security.json")})
	if err == nil {
		t.Fatal("unbootstrapped manager accepted missing setup secret")
	}
}
