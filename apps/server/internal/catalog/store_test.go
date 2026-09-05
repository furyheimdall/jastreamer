package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsTombstone_when_catalog_reopens(t *testing.T) {
	// Given
	ctx := context.Background()
	root := t.TempDir()
	mediaPath := filepath.Join(root, "queued.flac")
	writeRealFixture(t, "real.flac", mediaPath)
	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	assertNoError(t, err)
	config := StoreConfig{
		Path:   filepath.Join(t.TempDir(), "catalog.sqlite"),
		Root:   root,
		Schema: string(schema),
		Now: func() time.Time {
			return time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
		},
	}
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(ctx, EmptySnapshot())
	assertNoError(t, err)
	before := onlyTrack(t, first.Snapshot)
	store, err := OpenStore(ctx, config)
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, first))
	assertNoError(t, os.Remove(mediaPath))
	second, err := scanner.Scan(ctx, first.Snapshot)
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, second))
	assertNoError(t, store.Close())

	// When
	reopened, err := OpenStore(ctx, config)
	assertNoError(t, err)
	t.Cleanup(func() { assertNoError(t, reopened.Close()) })
	loaded, err := reopened.Load(ctx)

	// Then
	assertNoError(t, err)
	after, ok := loaded.Tracks[before.TrackID]
	if !ok || after.Available {
		t.Fatalf("reopened track = (%+v, %v), want durable tombstone", after, ok)
	}
	if after.AlbumID != before.AlbumID || after.Order != before.Order {
		t.Fatalf("durable tombstone lost anchor: before=%+v after=%+v", before, after)
	}
	if loaded.Generation != second.Snapshot.Generation || loaded.Revision != second.Snapshot.Revision {
		t.Fatalf("loaded generation/revision = %d/%d, want %d/%d", loaded.Generation, loaded.Revision, second.Snapshot.Generation, second.Snapshot.Revision)
	}
	assertNoError(t, reopened.IntegrityCheck(ctx))
	if reopened.SQLiteVersion() < 3_051_003 {
		t.Fatalf("SQLite version = %d, want 3.51.3+", reopened.SQLiteVersion())
	}
}

func TestOpenStore_reopensImmediatelyAfterClose(t *testing.T) {
	ctx := context.Background()
	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	assertNoError(t, err)
	config := StoreConfig{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), Root: t.TempDir(), Schema: string(schema), Now: time.Now}
	for attempt := 0; attempt < 3; attempt++ {
		store, openErr := OpenStore(ctx, config)
		assertNoError(t, openErr)
		assertNoError(t, store.Close())
	}
}

func TestIsSQLiteBusy_detectsLockedErrors(t *testing.T) {
	if isSQLiteBusy(nil) || isSQLiteBusy(errors.New("other")) {
		t.Fatal("non-busy error accepted")
	}
	if !isSQLiteBusy(fmt.Errorf("configure catalog database: %w", errors.New("database is locked (5) (SQLITE_BUSY)"))) {
		t.Fatal("locked error ignored")
	}
}
