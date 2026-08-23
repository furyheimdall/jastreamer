package album

import (
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

func TestSelectMultiDiscForwardOrder(t *testing.T) {
	snapshot := fixture("album", track("d1-1", "album", 1, 1, true), track("d1-2", "album", 1, 2, true), track("d2-1", "album", 2, 1, true))
	result := Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: snapshot.Tracks["d1-1"].Order})
	if result.Reason != Continue || result.Track.TrackID != "d1-2" {
		t.Fatalf("want d1-2 continuation, got %+v", result)
	}
	result = Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: result.Track.Order})
	if result.Track.TrackID != "d2-1" {
		t.Fatalf("want d2-1 continuation, got %+v", result)
	}
}

func TestSelectUnknownNumbersAndRemovedTracks(t *testing.T) {
	snapshot := fixture("album", track("known", "album", 1, 2, true), track("unknown", "album", 0, 0, true), track("removed", "album", 1, 3, false))
	result := Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: snapshot.Tracks["known"].Order})
	if result.Track.TrackID != "unknown" {
		t.Fatalf("want unknown-number track after numbered track, got %+v", result)
	}
}

func TestSelectReanchorAndNoWrap(t *testing.T) {
	snapshot := fixture("album", track("first", "album", 1, 1, true), track("last", "album", 1, 2, true))
	result := Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: snapshot.Tracks["last"].Order})
	if result.Reason != AlbumComplete {
		t.Fatalf("want album completion, got %+v", result)
	}
	result = Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: snapshot.Tracks["first"].Order})
	if result.Track.TrackID != "last" {
		t.Fatalf("explicit re-anchor must begin a new forward decision, got %+v", result)
	}
}

func TestSelectAlbumCompleteDoesNotFallback(t *testing.T) {
	snapshot := fixture("album", track("only", "album", 1, 1, true), track("similar", "other", 1, 1, true))
	result := Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: snapshot.Tracks["only"].Order})
	if result.Reason != AlbumComplete {
		t.Fatalf("want STOP_ALBUM_COMPLETE, got %+v", result)
	}
}

func TestSelectUsesPersistedAnchorWhenCurrentFileIsMissing(t *testing.T) {
	missing := track("missing", "album", 1, 1, true)
	snapshot := fixture("album", track("next", "album", 1, 2, true))
	result := Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: missing.Order})
	if result.Reason != Continue || result.Track.TrackID != "next" {
		t.Fatalf("persisted anchor result = %+v", result)
	}
}

func TestSelectKeepsFrozenReleaseAndExcludesStartedRecording(t *testing.T) {
	snapshot := fixture(
		"frozen",
		track("anchor", "frozen", 1, 1, true),
		track("started", "frozen", 1, 2, true),
		track("next", "frozen", 1, 3, true),
		track("other-release", "later-explicit", 1, 2, true),
	)
	result := Select(Request{
		Snapshot: snapshot, AlbumID: "frozen", Anchor: snapshot.Tracks["anchor"].Order,
		Started: map[catalog.RecordingID]bool{"started": true},
	})
	if result.Reason != Continue || result.Track.TrackID != "next" {
		t.Fatalf("frozen release result = %+v", result)
	}
}

func TestSelectMissingAlbumReturnsExactReason(t *testing.T) {
	result := Select(Request{Snapshot: fixture("other", track("track", "other", 1, 1, true)), AlbumID: "missing"})
	if result.Reason != NoAlbum {
		t.Fatalf("missing album reason = %s", result.Reason)
	}
}

func TestSelectIsStrictlyForwardAndFinite(t *testing.T) {
	tracks := make([]catalog.Track, 0, 80)
	for disc := range 4 {
		for number := range 20 {
			tracks = append(tracks, track(
				string(rune('a'+disc))+"-"+string(rune('a'+number)),
				"album", disc, number, true,
			))
		}
	}
	snapshot := fixture("album", tracks...)
	anchor := catalog.OrderKey{
		Disc:  catalog.OrderedNumber{Known: true},
		Track: catalog.OrderedNumber{Known: true},
	}
	selected := make(map[catalog.TrackID]bool)
	for steps := 0; ; steps++ {
		if steps > len(snapshot.Tracks) {
			t.Fatal("selector did not terminate")
		}
		result := Select(Request{Snapshot: snapshot, AlbumID: "album", Anchor: anchor})
		if result.Reason == AlbumComplete {
			break
		}
		if result.Reason != Continue {
			t.Fatalf("unexpected reason %s", result.Reason)
		}
		anchorTrack := catalog.Track{TrackID: anchor.TrackID, Order: anchor}
		if catalog.CompareTrackOrder(result.Track, anchorTrack) <= 0 || selected[result.Track.TrackID] {
			t.Fatalf("non-forward or repeated selection: %+v", result.Track)
		}
		selected[result.Track.TrackID] = true
		anchor = result.Track.Order
	}
	if len(selected) != len(snapshot.Tracks) {
		t.Fatalf("selected %d of %d tracks", len(selected), len(snapshot.Tracks))
	}
}

func fixture(albumID string, tracks ...catalog.Track) catalog.Snapshot {
	result := catalog.EmptySnapshot()
	for _, item := range tracks {
		if item.AlbumID == "" {
			item.AlbumID = catalog.AlbumID(albumID)
		}
		result.Tracks[item.TrackID] = item
	}
	return result
}

func track(id, album string, disc, number int, available bool) catalog.Track {
	return catalog.Track{TrackID: catalog.TrackID(id), RecordingID: catalog.RecordingID(id), AlbumID: catalog.AlbumID(album), Available: available, Order: catalog.NewOrderKey(catalog.Metadata{Disc: disc, Track: number}, id, catalog.TrackID(id))}
}
