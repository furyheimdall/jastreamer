package playback_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
	"github.com/jakestreamer/jstreamer-server/internal/curation/ranking"
	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

func todo12Config(t *testing.T) playback.Config {
	t.Helper()
	root := t.TempDir()
	return playback.Config{
		Path:            filepath.Join(root, "playback.sqlite"),
		MigrationPath:   "../../migrations/002_playback.sql",
		ExpansionPath:   "../../migrations/003_todo12.sql",
		BackupDirectory: filepath.Join(root, "backups"),
		SupportedSchema: playback.CurrentSchemaVersion,
		JournalMode:     playback.JournalRollback,
	}
}

func openTodo12Store(t *testing.T, config playback.Config) *playback.Store {
	t.Helper()
	store, err := playback.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("open playback store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close playback store: %v", err)
		}
	})
	return store
}

func startTodo12Session(t *testing.T, store *playback.Store, zone playback.ZoneID) playback.ZoneSnapshot {
	t.Helper()
	return startTodo12SessionWithPolicy(t, todo12SessionFixture{
		store: store, zone: zone, mode: decision.PolicySimilar,
	})
}

type todo12SessionFixture struct {
	store *playback.Store
	zone  playback.ZoneID
	mode  decision.Policy
}

func startTodo12SessionWithPolicy(t *testing.T, fixture todo12SessionFixture) playback.ZoneSnapshot {
	t.Helper()
	ctx := context.Background()
	if _, err := fixture.store.UpdateContinuationPolicy(ctx, playback.PolicyUpdate{
		ZoneID: fixture.zone, ExpectedRevision: 0, Mode: fixture.mode,
		ArtistGap: ranking.DefaultArtistGap, AlbumGap: ranking.DefaultAlbumGap,
	}); err != nil {
		t.Fatalf("set %s policy: %v", fixture.mode, err)
	}
	if _, err := fixture.store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: fixture.zone, IdempotencyKey: "seed", Tracks: []playback.QueueTrack{{ID: "seed", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue seed: %v", err)
	}
	seed, err := fixture.store.ReserveNext(ctx, fixture.zone, playback.Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve seed: %v", err)
	}
	started, err := fixture.store.ConfirmStart(ctx, fixture.zone, seed.PlayID)
	if err != nil {
		t.Fatalf("confirm seed: %v", err)
	}
	return started
}

func todo12SimilarSnapshot(candidateIDs ...string) decision.Snapshot {
	seed := todo12Track("seed")
	tracks := []candidates.Track{seed}
	for _, id := range candidateIDs {
		tracks = append(tracks, todo12Track(id))
	}
	return decision.Snapshot{
		Similar: decision.SimilarSnapshot{
			Index: candidates.NewIndex(1, tracks), Seed: seed, Current: seed,
			PageSize: 2, RankingPolicy: ranking.DefaultPolicy(),
			SessionSeed: "todo12-session", DecisionSequence: 7,
		},
	}
}

func todo12Track(id string) candidates.Track {
	return candidates.Track{
		Catalog: catalog.Track{
			TrackID: catalog.TrackID(id), RecordingID: catalog.RecordingID("recording-" + id),
			AlbumID: catalog.AlbumID("album-" + id), Format: catalog.FormatFLAC,
			Metadata: catalog.Metadata{Artist: "artist-" + id}, Available: true,
		},
		Signals: candidates.Signals{Genres: []string{"ambient"}},
	}
}
