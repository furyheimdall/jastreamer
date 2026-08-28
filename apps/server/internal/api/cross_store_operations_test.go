package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type crossStoreClock struct{ now time.Time }

func (clock crossStoreClock) Now() time.Time { return clock.now }

type failingCloser struct {
	calls int
}

func (closer *failingCloser) Close() error {
	closer.calls++
	if closer.calls == 1 {
		return errors.New("injected close failure")
	}
	return nil
}

type crossStoreFixture struct {
	securityPath string
	playbackPath string
	backupPath   string
	manager      *security.Manager
	store        *playback.Store
	admin        security.Credential
	handler      *RendererZoneAPI
}

func newCrossStoreFixture(t *testing.T) crossStoreFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	value := crossStoreFixture{
		securityPath: filepath.Join(root, "security.json"),
		playbackPath: filepath.Join(root, "playback.sqlite"),
		backupPath:   filepath.Join(root, "backups"),
	}
	var err error
	value.manager, err = security.NewManager(security.Config{
		SetupSecret: "setup", StatePath: value.securityPath,
		Clock: crossStoreClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("security: %v", err)
	}
	value.admin, err = value.manager.Bootstrap(ctx, "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	value.store = openCrossStorePlayback(t, value.playbackPath, value.backupPath)
	value.handler = newRendererZoneAPI(ctx, value.manager, value.store, nil)
	return value
}

func openCrossStorePlayback(t *testing.T, path, backups string) *playback.Store {
	t.Helper()
	store, err := playback.Open(context.Background(), playback.Config{
		Path: path, MigrationPath: "../../migrations/002_playback.sql",
		ExpansionPath: "../../migrations/003_todo12.sql", BackupDirectory: backups,
		SupportedSchema: playback.CurrentSchemaVersion, JournalMode: playback.JournalRollback,
	})
	if err != nil {
		t.Fatalf("playback: %v", err)
	}
	return store
}

func (value crossStoreFixture) pairRenderer(t *testing.T) security.Credential {
	t.Helper()
	ctx := context.Background()
	code, err := value.manager.GeneratePairingCodeForRole(ctx, value.admin.Token, security.RoleRenderer)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	credential, err := value.handler.pairCredential(ctx, rendererPairRequest{
		Code: code.Value, Registration: security.Registration{Name: "Renderer"}, Requester: "test",
	})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return credential
}

func TestRendererPairing_restart_compensates_each_committed_delivery_cut(t *testing.T) {
	for _, cut := range []rendererOperationStage{rendererAfterInventory, rendererBeforePairResponse} {
		t.Run(string(cut), func(t *testing.T) {
			// Given
			ctx := context.Background()
			value := newCrossStoreFixture(t)
			credential := security.Credential{}
			pairCode := ""
			if cut == rendererAfterInventory {
				value.handler.operationHook = func(stage rendererOperationStage) error {
					if stage == cut {
						return errors.New("injected crash")
					}
					return nil
				}
				code, err := value.manager.GeneratePairingCodeForRole(ctx, value.admin.Token, security.RoleRenderer)
				if err != nil {
					t.Fatalf("generate: %v", err)
				}
				pairCode = code.Value
				_, err = value.handler.pairCredential(ctx, rendererPairRequest{
					Code: pairCode, Registration: security.Registration{Name: "Renderer"}, Requester: "test",
				})
				if !errors.Is(err, security.ErrRendererOperationPending) {
					t.Fatalf("cut error = %v", err)
				}
			} else {
				code, err := value.manager.GeneratePairingCodeForRole(ctx, value.admin.Token, security.RoleRenderer)
				if err != nil {
					t.Fatalf("generate: %v", err)
				}
				pairCode = code.Value
				credential, err = value.handler.pairCredential(ctx, rendererPairRequest{
					Code: pairCode, Registration: security.Registration{Name: "Renderer"}, Requester: "test",
				})
				if err != nil {
					t.Fatalf("pair: %v", err)
				}
				value.handler.operationHook = func(stage rendererOperationStage) error {
					if stage == cut {
						return errors.New("injected response failure")
					}
					return nil
				}
				if err := value.handler.beforePairResponse(); !errors.Is(err, security.ErrRendererOperationPending) {
					t.Fatalf("response cut = %v", err)
				}
			}
			if err := value.store.Close(); err != nil {
				t.Fatalf("close before restart: %v", err)
			}

			// When
			restartedManager, err := security.NewManager(security.Config{StatePath: value.securityPath})
			if err != nil {
				t.Fatalf("restart security: %v", err)
			}
			restartedStore := openCrossStorePlayback(t, value.playbackPath, value.backupPath)
			t.Cleanup(func() { _ = restartedStore.Close() })
			_ = newRendererZoneAPI(ctx, restartedManager, restartedStore, nil)
			operations, err := restartedManager.PendingRendererOperations(ctx)

			// Then
			if err != nil || len(operations) != 0 {
				t.Fatalf("pending after restart = %+v, %v", operations, err)
			}
			if credential.Token != "" {
				if _, err := restartedManager.Authenticate(ctx, credential.Token); !errors.Is(err, security.ErrTokenRevoked) {
					t.Fatalf("undelivered token authorization = %v", err)
				}
			}
			if _, err := restartedManager.Pair(ctx, pairCode, security.Registration{Name: "Retry"}, "test"); !errors.Is(err, security.ErrPairingCodeUsed) {
				t.Fatalf("consumed pairing code retry = %v", err)
			}
			renderers, err := restartedStore.Renderers(ctx)
			if err != nil || len(renderers) != 1 || renderers[0].State != playback.RendererRevoked {
				t.Fatalf("renderer reconciliation = %+v, %v", renderers, err)
			}
		})
	}
}

func TestRendererRevocation_restart_reconciles_each_playback_cut(t *testing.T) {
	for _, cut := range []rendererOperationStage{
		rendererBeforePlaybackRevoke, rendererAfterPlaybackRevoke,
		rendererBeforeResourceClose, rendererAfterResourceClose,
	} {
		t.Run(string(cut), func(t *testing.T) {
			// Given
			ctx := context.Background()
			value := newCrossStoreFixture(t)
			credential := value.pairRenderer(t)
			if _, err := value.manager.Authenticate(ctx, credential.Token); err != nil {
				t.Fatalf("activate: %v", err)
			}
			if _, err := value.store.CreateZone(ctx, playback.ZoneDefinition{ID: "zone", DisplayName: "Zone"}); err != nil {
				t.Fatalf("create zone: %v", err)
			}
			if _, err := value.store.AssignRenderer(ctx, playback.AssignmentRequest{
				ZoneID: "zone", RendererID: playback.RendererID(credential.Device.ID), ExpectedRevision: 0,
			}); err != nil {
				t.Fatalf("assign: %v", err)
			}
			fired := false
			value.handler.operationHook = func(stage rendererOperationStage) error {
				if stage == cut && !fired {
					fired = true
					return errors.New("injected crash")
				}
				return nil
			}
			_, revokeErr := value.handler.revokeCredential(ctx, value.admin.Token, credential.Device.ID)
			if !errors.Is(revokeErr, security.ErrRendererOperationPending) {
				t.Fatalf("cut error = %v", revokeErr)
			}
			if _, err := value.manager.Authenticate(ctx, credential.Token); !errors.Is(err, security.ErrTokenRevoked) {
				t.Fatalf("authorization after cut = %v", err)
			}
			if err := value.store.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			// When
			restartedManager, err := security.NewManager(security.Config{StatePath: value.securityPath})
			if err != nil {
				t.Fatalf("restart security: %v", err)
			}
			restartedStore := openCrossStorePlayback(t, value.playbackPath, value.backupPath)
			t.Cleanup(func() { _ = restartedStore.Close() })
			_ = newRendererZoneAPI(ctx, restartedManager, restartedStore, nil)
			snapshot, err := restartedStore.Zones(ctx)
			if err != nil {
				t.Fatalf("zones: %v", err)
			}

			// Then
			if len(snapshot.Zones) != 1 || snapshot.Zones[0].RendererID != "" || snapshot.Zones[0].Transport != playback.TransportSuspended {
				t.Fatalf("zone after recovery = %+v", snapshot.Zones)
			}
			operations, err := restartedManager.PendingRendererOperations(ctx)
			if err != nil || len(operations) != 0 {
				t.Fatalf("pending after recovery = %+v, %v", operations, err)
			}
		})
	}
}

func TestRendererRevocation_retries_failed_resource_once_without_reauthorizing(t *testing.T) {
	// Given
	ctx := context.Background()
	value := newCrossStoreFixture(t)
	t.Cleanup(func() { _ = value.store.Close() })
	credential := value.pairRenderer(t)
	if _, err := value.manager.Authenticate(ctx, credential.Token); err != nil {
		t.Fatalf("activate delivered credential: %v", err)
	}
	resource := &failingCloser{}
	value.handler.TrackRendererResource(playback.RendererID(credential.Device.ID), resource)

	// When
	_, firstErr := value.handler.revokeCredential(ctx, value.admin.Token, credential.Device.ID)
	_, authErr := value.manager.Authenticate(ctx, credential.Token)
	_, retryErr := value.handler.revokeCredential(ctx, value.admin.Token, credential.Device.ID)

	// Then
	if !errors.Is(firstErr, security.ErrRendererOperationPending) {
		t.Fatalf("first revoke = %v", firstErr)
	}
	if !errors.Is(authErr, security.ErrTokenRevoked) {
		t.Fatalf("authorization after failed cleanup = %v", authErr)
	}
	if retryErr != nil || resource.calls != 2 {
		t.Fatalf("retry = %v, close calls=%d", retryErr, resource.calls)
	}
	operations, err := value.manager.PendingRendererOperations(ctx)
	if err != nil || len(operations) != 0 {
		t.Fatalf("pending after retry = %+v, %v", operations, err)
	}
}
