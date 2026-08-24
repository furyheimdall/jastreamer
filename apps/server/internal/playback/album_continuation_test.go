package playback_test

import (
	"context"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestAlbumContinuationMultiDiscForwardOrder(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	snapshot := albumSnapshot(
		albumTrack("disc-1-track-1", 1, 1),
		albumTrack("disc-1-track-2", 1, 2),
		albumTrack("disc-2-track-1", 2, 1),
	)
	anchor := startAlbumAnchor(t, albumFixture{store: store, snapshot: snapshot, trackID: "disc-1-track-1"})
	request := decision.Snapshot{
		Catalog: snapshot,
		Album: decision.AlbumSnapshot{
			AlbumID: "release", Anchor: snapshot.Tracks["disc-1-track-1"].Order,
			Started: map[catalog.RecordingID]bool{"disc-1-track-1": true},
		},
	}
	first, err := store.CommitNext(ctx, playback.NextRequest{
		ZoneID: "zone-album", Boundary: playback.Boundary{ID: "album-1", PreviousPlayID: anchor.PlayID},
		Snapshot: request,
	})
	if err != nil {
		t.Fatalf("first album decision: %v", err)
	}
	if _, err := store.ConfirmStart(ctx, "zone-album", first.PlayID); err != nil {
		t.Fatalf("confirm first album track: %v", err)
	}

	// When
	second, err := store.CommitNext(ctx, playback.NextRequest{
		ZoneID: "zone-album", Boundary: playback.Boundary{ID: "album-2", PreviousPlayID: first.PlayID},
		Snapshot: request,
	})
	// Then
	if err != nil {
		t.Fatalf("second album decision: %v", err)
	}
	if first.TrackID != "disc-1-track-2" || second.TrackID != "disc-2-track-1" ||
		first.Reason != string(decision.ReasonPlayAlbum) || second.Reason != string(decision.ReasonPlayAlbum) {
		t.Fatalf("album order = %+v then %+v", first, second)
	}
}

func TestAlbumLastTrackStopsWithoutSimilarFallback(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTodo12Store(t, todo12Config(t))
	snapshot := albumSnapshot(albumTrack("last", 1, 1))
	anchor := startAlbumAnchor(t, albumFixture{store: store, snapshot: snapshot, trackID: "last"})
	request := todo12SimilarSnapshot("similar")
	request.Catalog = snapshot
	request.Album = decision.AlbumSnapshot{AlbumID: "release", Anchor: snapshot.Tracks["last"].Order}

	// When
	result, err := store.CommitNext(ctx, playback.NextRequest{
		ZoneID: "zone-album", Boundary: playback.Boundary{ID: "album-last", PreviousPlayID: anchor.PlayID},
		Snapshot: request,
	})
	// Then
	if err != nil {
		t.Fatalf("album completion: %v", err)
	}
	if result.Kind != playback.DecisionStop || result.Reason != string(decision.ReasonStopAlbumComplete) {
		t.Fatalf("decision = %+v, want STOP_ALBUM_COMPLETE", result)
	}
}

func TestAlbumContinuationStatePersistsAcrossRestart(t *testing.T) {
	// Given
	ctx := context.Background()
	config := todo12Config(t)
	store := openTodo12Store(t, config)
	snapshot := albumSnapshot(
		albumTrack("track-1", 1, 1), albumTrack("track-2", 1, 2), albumTrack("track-3", 1, 3),
	)
	anchor := startAlbumAnchor(t, albumFixture{store: store, snapshot: snapshot, trackID: "track-1"})
	request := decision.Snapshot{
		Catalog: snapshot,
		Album:   decision.AlbumSnapshot{AlbumID: "release", Anchor: snapshot.Tracks["track-1"].Order},
	}
	second, err := store.CommitNext(ctx, playback.NextRequest{
		ZoneID: "zone-album", Boundary: playback.Boundary{ID: "album-1", PreviousPlayID: anchor.PlayID},
		Snapshot: request,
	})
	if err != nil {
		t.Fatalf("select second track: %v", err)
	}
	confirmed, err := store.ConfirmStart(ctx, "zone-album", second.PlayID)
	if err != nil {
		t.Fatalf("confirm second track: %v", err)
	}
	duplicate, err := store.ConfirmStart(ctx, "zone-album", second.PlayID)
	if err != nil {
		t.Fatalf("duplicate confirm second track: %v", err)
	}
	if duplicate.Revision != confirmed.Revision || duplicate.CurrentPlay != confirmed.CurrentPlay {
		t.Fatalf("duplicate confirm mutated state: first=%+v duplicate=%+v", confirmed, duplicate)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted := openTodo12Store(t, config)

	// When
	third, err := restarted.CommitNext(ctx, playback.NextRequest{
		ZoneID: "zone-album", Boundary: playback.Boundary{ID: "album-2", PreviousPlayID: second.PlayID},
		Snapshot: request,
	})
	// Then
	if err != nil {
		t.Fatalf("select after restart: %v", err)
	}
	if third.TrackID != "track-3" || third.Reason != string(decision.ReasonPlayAlbum) {
		t.Fatalf("decision after restart = %+v, want track-3", third)
	}
}

type albumFixture struct {
	store    *playback.Store
	snapshot catalog.Snapshot
	trackID  catalog.TrackID
}

func startAlbumAnchor(t *testing.T, fixture albumFixture) playback.Decision {
	t.Helper()
	ctx := context.Background()
	if _, err := fixture.store.UpdateContinuationPolicy(ctx, playback.PolicyUpdate{
		ZoneID: "zone-album", Mode: decision.PolicyAlbum, ArtistGap: 4, AlbumGap: 10,
	}); err != nil {
		t.Fatalf("set album policy: %v", err)
	}
	if _, err := fixture.store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "zone-album", IdempotencyKey: "album-anchor",
		Tracks: []playback.QueueTrack{{ID: playback.TrackID(fixture.trackID), Available: true}},
	}); err != nil {
		t.Fatalf("enqueue album anchor: %v", err)
	}
	anchor, err := fixture.store.CommitNext(ctx, playback.NextRequest{
		ZoneID: "zone-album", Boundary: playback.Boundary{ID: "album-start"},
		Snapshot: decision.Snapshot{Catalog: fixture.snapshot},
	})
	if err != nil {
		t.Fatalf("commit album anchor: %v", err)
	}
	if _, err := fixture.store.ConfirmStart(ctx, "zone-album", anchor.PlayID); err != nil {
		t.Fatalf("confirm album anchor: %v", err)
	}
	return anchor
}

func albumSnapshot(tracks ...catalog.Track) catalog.Snapshot {
	snapshot := catalog.EmptySnapshot()
	for _, track := range tracks {
		snapshot.Tracks[track.TrackID] = track
	}
	return snapshot
}

func albumTrack(id string, disc, number int) catalog.Track {
	return catalog.Track{
		TrackID: catalog.TrackID(id), RecordingID: catalog.RecordingID(id),
		AlbumID: "release", Available: true,
		Order: catalog.NewOrderKey(catalog.Metadata{Disc: disc, Track: number}, id, catalog.TrackID(id)),
	}
}
