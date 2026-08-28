package security_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestRevocationObserver_receives_committed_device_once_and_can_unregister(t *testing.T) {
	// Given
	manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: filepath.Join(t.TempDir(), "security.json")})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	admin, err := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	pair := func(name string) security.Credential {
		code, codeErr := manager.GeneratePairingCode(context.Background(), admin.Token)
		if codeErr != nil {
			t.Fatalf("pairing code: %v", codeErr)
		}
		credential, pairErr := manager.Pair(context.Background(), code.Value, security.Registration{Name: name}, "test")
		if pairErr != nil {
			t.Fatalf("pair: %v", pairErr)
		}
		return credential
	}
	first := pair("First")
	second := pair("Second")
	revoked := make(chan security.DeviceID, 1)
	unregister := manager.ObserveRevocations(func(id security.DeviceID) { revoked <- id })

	// When
	firstErr := manager.Revoke(context.Background(), admin.Token, first.Device.ID)
	unregister()
	secondErr := manager.Revoke(context.Background(), admin.Token, second.Device.ID)

	// Then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("revoke errors = %v / %v", firstErr, secondErr)
	}
	select {
	case id := <-revoked:
		if id != first.Device.ID {
			t.Fatalf("observed ID = %q", id)
		}
	default:
		t.Fatal("committed revocation was not observed")
	}
	select {
	case id := <-revoked:
		t.Fatalf("observer received ID after unregister = %q", id)
	default:
	}
}
