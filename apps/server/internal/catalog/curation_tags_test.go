package catalog

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestStorePersistsCurationTags(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeRealFixture(t, "real.flac", filepath.Join(root, "tagged.flac"))
	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	assertNoError(t, err)
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	scan, err := scanner.Scan(ctx, EmptySnapshot())
	assertNoError(t, err)
	track := onlyTrack(t, scan.Snapshot)
	track.Metadata.Genres = []string{"Ambient", "Dream Pop"}
	track.Metadata.Styles = []string{"Ethereal"}
	track.Metadata.Moods = []string{"Calm"}
	track.Metadata.LocalTags = []string{"Night"}
	scan.Snapshot.Tracks[track.TrackID] = track
	config := StoreConfig{
		Path: filepath.Join(t.TempDir(), "catalog.sqlite"), Root: root, Schema: string(schema), Now: time.Now,
	}
	store, err := OpenStore(ctx, config)
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, scan))
	assertNoError(t, store.Close())
	reopened, err := OpenStore(ctx, config)
	assertNoError(t, err)
	defer func() { assertNoError(t, reopened.Close()) }()
	loaded, err := reopened.Load(ctx)
	assertNoError(t, err)
	got := loaded.Tracks[track.TrackID].Metadata
	if !slices.Equal(got.Genres, track.Metadata.Genres) ||
		!slices.Equal(got.Styles, track.Metadata.Styles) ||
		!slices.Equal(got.Moods, track.Metadata.Moods) ||
		!slices.Equal(got.LocalTags, track.Metadata.LocalTags) {
		t.Fatalf("curation tags = %+v", got)
	}
}
