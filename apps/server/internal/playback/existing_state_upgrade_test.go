package playback

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func Test_Migration_preserves_existing_security_and_catalog_state(t *testing.T) {
	// Given
	ctx := context.Background()
	root := t.TempDir()
	securityPath := filepath.Join(root, "security.json")
	securityConfig := security.Config{
		SetupSecret: "setup-only-in-memory", StatePath: securityPath,
		Random:     bytes.NewReader(bytes.Repeat([]byte{7}, 128)),
		PairingTTL: time.Minute, MaxFailures: 3,
	}
	manager, err := security.NewManager(securityConfig)
	if err != nil {
		t.Fatalf("create security manager: %v", err)
	}
	credential, err := manager.Bootstrap(ctx, "setup-only-in-memory", security.Registration{Name: "preserved-admin"})
	if err != nil {
		t.Fatalf("bootstrap security state: %v", err)
	}
	catalogSchema, err := os.ReadFile("../../migrations/001_catalog.sql")
	if err != nil {
		t.Fatalf("read catalog schema: %v", err)
	}
	catalogPath := filepath.Join(root, "catalog.sqlite")
	mediaRoot := t.TempDir()
	catalogConfig := catalog.StoreConfig{
		Path: catalogPath, Root: mediaRoot, Schema: string(catalogSchema),
		Now: func() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) },
	}
	catalogStore, err := catalog.OpenStore(ctx, catalogConfig)
	if err != nil {
		t.Fatalf("open catalog store: %v", err)
	}
	if err := catalogStore.Close(); err != nil {
		t.Fatalf("close catalog store: %v", err)
	}
	db, err := sql.Open("sqlite", catalogPath)
	if err != nil {
		t.Fatalf("open catalog fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog_roots(root_id,canonical_path,created_at)
		VALUES ('root-preserved',?,'2026-08-25T00:00:00Z')`, mediaRoot); err != nil {
		closeErr := db.Close()
		t.Fatalf("seed catalog root: %v; close: %v", err, closeErr)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close catalog fixture: %v", err)
	}
	playbackConfig := testConfig(t)
	seedSchemaThree(t, playbackConfig)

	// When
	playbackStore := openTestStore(t, playbackConfig)
	if err := playbackStore.Close(); err != nil {
		t.Fatalf("close upgraded playback store: %v", err)
	}
	restartedSecurity, err := security.NewManager(securityConfig)
	if err != nil {
		t.Fatalf("restart security manager: %v", err)
	}
	restartedCatalog, err := catalog.OpenStore(ctx, catalogConfig)
	if err != nil {
		t.Fatalf("restart catalog store: %v", err)
	}
	t.Cleanup(func() {
		if err := restartedCatalog.Close(); err != nil {
			t.Errorf("close restarted catalog: %v", err)
		}
	})

	// Then
	device, err := restartedSecurity.Authenticate(ctx, credential.Token)
	if err != nil {
		t.Fatalf("authenticate preserved credential: %v", err)
	}
	if device.Name != "preserved-admin" || device.Role != security.RoleAdmin {
		t.Fatalf("preserved device = %+v", device)
	}
	catalogDB, err := sql.Open("sqlite", catalogPath)
	if err != nil {
		t.Fatalf("inspect restarted catalog: %v", err)
	}
	defer func() {
		if err := catalogDB.Close(); err != nil {
			t.Errorf("close catalog inspection: %v", err)
		}
	}()
	var canonicalPath string
	if err := catalogDB.QueryRowContext(ctx,
		"SELECT canonical_path FROM catalog_roots WHERE root_id='root-preserved'",
	).Scan(&canonicalPath); err != nil {
		t.Fatalf("load preserved catalog root: %v", err)
	}
	if canonicalPath != mediaRoot {
		t.Fatalf("preserved catalog root = %q, want %q", canonicalPath, mediaRoot)
	}
}
