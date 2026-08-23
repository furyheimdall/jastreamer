package security_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakestreamer/jstreamer-server/internal/security"
)

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time { return clock.now }

func newManager(t *testing.T, clock *testClock) *security.Manager {
	t.Helper()
	manager, err := security.NewManager(security.Config{
		SetupSecret: "installer-secret", StatePath: filepath.Join(t.TempDir(), "security.json"),
		Clock: clock, PairingTTL: 5 * time.Minute, MaxFailures: 3,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return manager
}

func TestBootstrap_issues_first_admin_once_and_persists_only_token_hash(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	statePath := filepath.Join(t.TempDir(), "security.json")
	manager, err := security.NewManager(security.Config{SetupSecret: "setup-raw", StatePath: statePath, Clock: clock})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	// When
	credential, err := manager.Bootstrap(context.Background(), "setup-raw", security.Registration{Name: "Owner"})

	// Then
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if credential.Device.Role != security.RoleAdmin || credential.Token == "" {
		t.Fatalf("credential = %#v", credential)
	}
	_, err = manager.Bootstrap(context.Background(), "setup-raw", security.Registration{Name: "Second"})
	if !errors.Is(err, security.ErrBootstrapComplete) {
		t.Fatalf("second bootstrap error = %v", err)
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(state), credential.Token) || strings.Contains(string(state), "setup-raw") {
		t.Fatalf("state contains raw credential: %s", state)
	}
}

func TestPairingCode_is_five_minute_single_use_and_controller_only(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	admin, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// When
	paired, err := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Living room"}, "client-a")

	// Then
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if paired.Device.Role != security.RoleController {
		t.Fatalf("paired role = %q", paired.Device.Role)
	}
	_, err = manager.Pair(context.Background(), code.Value, security.Registration{Name: "Replay"}, "client-a")
	if !errors.Is(err, security.ErrPairingCodeUsed) {
		t.Fatalf("replay error = %v", err)
	}
	expiring, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("generate expiring: %v", err)
	}
	clock.now = clock.now.Add(5*time.Minute + time.Nanosecond)
	_, err = manager.Pair(context.Background(), expiring.Value, security.Registration{Name: "Late"}, "client-b")
	if !errors.Is(err, security.ErrPairingCodeExpired) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestPairingCode_persists_keyed_digest_and_survives_restart(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	statePath := filepath.Join(t.TempDir(), "security.json")
	manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: statePath, Clock: clock})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	admin, _ := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	code, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// When
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	plainDigest := sha256.Sum256([]byte(code.Value))
	restarted, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: statePath, Clock: clock})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	credential, err := restarted.Pair(context.Background(), code.Value, security.Registration{Name: "Control"}, "client")

	// Then
	if strings.Contains(string(state), code.Value) || strings.Contains(string(state), hex.EncodeToString(plainDigest[:])) {
		t.Fatalf("state contains enumerable pairing credential: %s", state)
	}
	if err != nil || credential.Device.Role != security.RoleController {
		t.Fatalf("pair after restart = %+v, %v", credential, err)
	}
}

func TestPairingCode_expires_at_exact_five_minute_boundary(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	admin, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	code, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	clock.now = code.ExpiresAt

	// When
	_, err = manager.Pair(context.Background(), code.Value, security.Registration{Name: "Late"}, "client")

	// Then
	if !errors.Is(err, security.ErrPairingCodeExpired) {
		t.Fatalf("pair at expiry error = %v", err)
	}
}

func TestPersistenceFailure_leaves_security_state_unchanged(t *testing.T) {
	newPersistentManager := func(t *testing.T) (*security.Manager, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "security.json")
		manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: path})
		if err != nil {
			t.Fatalf("manager: %v", err)
		}
		return manager, path
	}
	failWrites := func(t *testing.T, path string) {
		t.Helper()
		if err := os.Mkdir(path+".tmp", 0o700); err != nil {
			t.Fatalf("block temporary state: %v", err)
		}
	}
	restoreWrites := func(t *testing.T, path string) {
		t.Helper()
		if err := os.Remove(path + ".tmp"); err != nil {
			t.Fatalf("restore temporary state: %v", err)
		}
	}

	t.Run("bootstrap", func(t *testing.T) {
		manager, path := newPersistentManager(t)
		failWrites(t, path)
		if _, err := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"}); err == nil {
			t.Fatal("bootstrap succeeded despite persistence failure")
		}
		restoreWrites(t, path)
		credential, err := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
		if err != nil || credential.Device.ID != "device-000001" {
			t.Fatalf("retry bootstrap = %+v, %v", credential, err)
		}
	})
	t.Run("code generation", func(t *testing.T) {
		manager, path := newPersistentManager(t)
		admin, _ := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
		failWrites(t, path)
		if _, err := manager.GeneratePairingCode(context.Background(), admin.Token); err == nil {
			t.Fatal("code generation succeeded despite persistence failure")
		}
		restoreWrites(t, path)
		if _, err := manager.GeneratePairingCode(context.Background(), admin.Token); err != nil {
			t.Fatalf("retry generation: %v", err)
		}
	})
	t.Run("pair consume", func(t *testing.T) {
		manager, path := newPersistentManager(t)
		admin, _ := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
		code, _ := manager.GeneratePairingCode(context.Background(), admin.Token)
		failWrites(t, path)
		if _, err := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Control"}, "client"); err == nil {
			t.Fatal("pair succeeded despite persistence failure")
		}
		restoreWrites(t, path)
		credential, err := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Control"}, "client")
		if err != nil || credential.Device.ID != "device-000002" {
			t.Fatalf("retry pair = %+v, %v", credential, err)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		manager, path := newPersistentManager(t)
		admin, _ := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
		code, _ := manager.GeneratePairingCode(context.Background(), admin.Token)
		controller, _ := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Control"}, "client")
		failWrites(t, path)
		if err := manager.Revoke(context.Background(), admin.Token, controller.Device.ID); err == nil {
			t.Fatal("revoke succeeded despite persistence failure")
		}
		if _, err := manager.Authenticate(context.Background(), controller.Token); err != nil {
			t.Fatalf("failed revoke mutated memory: %v", err)
		}
	})
}

func TestAuthorization_revocation_and_wrong_code_limit_do_not_create_devices(t *testing.T) {
	// Given
	clock := &testClock{now: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	manager := newManager(t, clock)
	admin, err := manager.Bootstrap(context.Background(), "installer-secret", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// When
	for range 3 {
		_, err = manager.Pair(context.Background(), "000000", security.Registration{Name: "Attacker"}, "same-client")
	}
	_, limitedErr := manager.Pair(context.Background(), "000000", security.Registration{Name: "Attacker"}, "same-client")

	// Then
	if !errors.Is(limitedErr, security.ErrRateLimited) {
		t.Fatalf("limited error = %v", limitedErr)
	}
	devices, err := manager.Devices(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices after wrong codes = %d", len(devices))
	}
	code, err := manager.GeneratePairingCode(context.Background(), admin.Token)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	controller, err := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Controller"}, "good-client")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := manager.GeneratePairingCode(context.Background(), controller.Token); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("controller generate error = %v", err)
	}
	if err := manager.Revoke(context.Background(), admin.Token, controller.Device.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err = manager.Authenticate(context.Background(), controller.Token)
	if !errors.Is(err, security.ErrTokenRevoked) {
		t.Fatalf("revoked authentication error = %v", err)
	}
}
