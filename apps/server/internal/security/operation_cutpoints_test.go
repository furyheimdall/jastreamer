package security_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestRendererPairing_security_commit_cutpoints_have_explicit_consumption(t *testing.T) {
	for _, cut := range []security.OperationStage{security.OperationBeforePair, security.OperationAfterPair} {
		t.Run(string(cut), func(t *testing.T) {
			// Given
			ctx := context.Background()
			statePath := filepath.Join(t.TempDir(), "security.json")
			activeCut := cut
			config := security.Config{
				SetupSecret: "setup", StatePath: statePath,
				Clock: &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)},
				OperationHook: func(stage security.OperationStage) error {
					if stage == activeCut {
						return errors.New("injected security cut")
					}
					return nil
				},
			}
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

			// When
			_, pairErr := manager.Pair(ctx, code.Value, security.Registration{Name: "Renderer"}, "test")
			activeCut = ""
			restarted, restartErr := security.NewManager(config)
			if restartErr != nil {
				t.Fatalf("restart: %v", restartErr)
			}
			_, retryErr := restarted.Pair(ctx, code.Value, security.Registration{Name: "Retry"}, "test")

			// Then
			if pairErr == nil {
				t.Fatal("injected cut did not fail")
			}
			switch cut {
			case security.OperationBeforePair:
				if retryErr != nil {
					t.Fatalf("pre-commit code was consumed: %v", retryErr)
				}
			case security.OperationAfterPair:
				if !errors.Is(retryErr, security.ErrPairingCodeUsed) {
					t.Fatalf("post-commit code retry = %v", retryErr)
				}
			default:
				t.Fatalf("unexpected cut %q", cut)
			}
		})
	}
}

func TestRendererRevocation_security_commit_cutpoints_are_idempotently_retryable(t *testing.T) {
	for _, cut := range []security.OperationStage{security.OperationBeforeRevoke, security.OperationAfterRevoke} {
		t.Run(string(cut), func(t *testing.T) {
			// Given
			ctx := context.Background()
			statePath := filepath.Join(t.TempDir(), "security.json")
			activeCut := security.OperationStage("")
			config := security.Config{
				SetupSecret: "setup", StatePath: statePath,
				Clock: &testClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)},
				OperationHook: func(stage security.OperationStage) error {
					if stage == activeCut {
						return errors.New("injected security cut")
					}
					return nil
				},
			}
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
			credential, err := manager.Pair(ctx, code.Value, security.Registration{Name: "Renderer"}, "test")
			if err != nil {
				t.Fatalf("pair: %v", err)
			}
			if err := manager.MarkRendererInventoryReady(ctx, credential.Device.ID); err != nil {
				t.Fatalf("ready: %v", err)
			}
			if _, err := manager.Authenticate(ctx, credential.Token); err != nil {
				t.Fatalf("activate: %v", err)
			}
			activeCut = cut

			// When
			firstErr := manager.Revoke(ctx, admin.Token, credential.Device.ID)
			activeCut = ""
			retryErr := manager.Revoke(ctx, admin.Token, credential.Device.ID)
			_, authErr := manager.Authenticate(ctx, credential.Token)

			// Then
			if firstErr == nil || retryErr != nil || !errors.Is(authErr, security.ErrTokenRevoked) {
				t.Fatalf("first=%v retry=%v auth=%v", firstErr, retryErr, authErr)
			}
		})
	}
}
