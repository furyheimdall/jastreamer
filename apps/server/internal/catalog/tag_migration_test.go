package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenStoreAddsCurationTagsToExistingCatalog(t *testing.T) {
	ctx := context.Background()
	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	assertNoError(t, err)
	start := strings.Index(string(schema), "CREATE TABLE catalog_track_tags")
	end := strings.Index(string(schema), "CREATE TABLE catalog_analysis")
	if start < 0 || end < start {
		t.Fatal("curation tag migration block not found")
	}
	legacySchema := string(schema[:start]) + string(schema[end:])
	config := StoreConfig{
		Path: filepath.Join(t.TempDir(), "catalog.sqlite"), Root: t.TempDir(),
		Schema: legacySchema, Now: time.Now,
	}
	store, err := OpenStore(ctx, config)
	assertNoError(t, err)
	assertNoError(t, store.Close())
	store, err = OpenStore(ctx, config)
	assertNoError(t, err)
	defer func() { assertNoError(t, store.Close()) }()
	var table string
	assertNoError(t, store.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='catalog_track_tags'`).Scan(&table))
	if table != "catalog_track_tags" {
		t.Fatalf("table = %q", table)
	}
}
