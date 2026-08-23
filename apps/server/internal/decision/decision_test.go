package decision

import (
	"slices"
	"strconv"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
)

func TestDecideNext_playable_explicit_head_wins_over_similar(t *testing.T) {
	// Given
	snapshot := testSimilarSnapshot("generated")
	snapshot.Explicit = []ExplicitEntry{
		testExplicit("queue-1", "explicit", true),
		testExplicit("queue-2", "later", true),
	}

	// When
	outcome := DecideNext(snapshot, testBoundary("boundary-1"))

	// Then
	play := requirePlay(t, outcome)
	if play.Reason != ReasonPlayExplicit || play.Source != SourceExplicit || play.TrackID != "explicit" || play.QueueEntryID != "queue-1" {
		t.Fatalf("play = %#v, want explicit queue head", play)
	}
}

func TestDecideNext_unavailable_explicit_head_blocks_without_skipping(t *testing.T) {
	// Given
	snapshot := testSimilarSnapshot("generated")
	snapshot.Explicit = []ExplicitEntry{
		testExplicit("queue-1", "missing", false),
		testExplicit("queue-2", "later", true),
	}

	// When
	outcome := DecideNext(snapshot, testBoundary("boundary-1"))

	// Then
	block, ok := outcome.(Block)
	if !ok {
		t.Fatalf("outcome = %#v, want Block", outcome)
	}
	if block.Reason != ReasonBlockExplicit || block.QueueEntryID != "queue-1" || block.TrackID != "missing" {
		t.Fatalf("block = %#v, want blocked queue head", block)
	}
}

func TestDecideNext_stop_mode_returns_exact_reason(t *testing.T) {
	// Given
	snapshot := Snapshot{Policy: PolicyStop}

	// When
	outcome := DecideNext(snapshot, testBoundary("boundary-stop"))

	// Then
	if stop := requireStop(t, outcome); stop.Reason != ReasonStopModeOff {
		t.Fatalf("reason = %s, want %s", stop.Reason, ReasonStopModeOff)
	}
}

func TestDecideNext_album_uses_finite_album_selector_without_cross_fallback(t *testing.T) {
	// Given
	snapshot := testAlbumSnapshot(2)
	snapshot.Similar = testSimilarSnapshot("generated").Similar

	// When
	first := DecideNext(snapshot, testBoundary("boundary-album-1"))
	snapshot.Album.Anchor = snapshot.Catalog.Tracks["album-track-2"].Order
	last := DecideNext(snapshot, testBoundary("boundary-album-2"))

	// Then
	play := requirePlay(t, first)
	if play.Source != SourceAlbum || play.Reason != ReasonPlayAlbum || play.TrackID != "album-track-2" {
		t.Fatalf("album play = %#v", play)
	}
	if stop := requireStop(t, last); stop.Reason != ReasonStopAlbumComplete {
		t.Fatalf("album completion = %#v, want no similar fallback", stop)
	}
}

func TestDecideNext_similar_distinguishes_no_signal_from_seen_exhaustion(t *testing.T) {
	// Given
	noSignal := testSimilarSnapshot()
	exhausted := testSimilarSnapshot("related")
	exhausted.Similar.Seen = map[candidates.RecordingKey]struct{}{
		"recording:recording-related": {},
	}

	// When
	noSignalOutcome := DecideNext(noSignal, testBoundary("no-signal"))
	exhaustedOutcome := DecideNext(exhausted, testBoundary("exhausted"))

	// Then
	if got := requireStop(t, noSignalOutcome).Reason; got != ReasonStopSimilarNoSignal {
		t.Fatalf("no-signal reason = %s", got)
	}
	if got := requireStop(t, exhaustedOutcome).Reason; got != ReasonStopSimilarExhausted {
		t.Fatalf("exhaustion reason = %s", got)
	}
}

func TestDecideNext_album_retry_skips_failed_generated_track(t *testing.T) {
	// Given
	snapshot := testAlbumSnapshot(4)
	first := requirePlay(t, DecideNext(snapshot, testBoundary("album-retry")))
	snapshot.FailedGenerated = []catalog.TrackID{first.TrackID}

	// When
	second := requirePlay(t, DecideNext(snapshot, testBoundary("album-retry")))

	// Then
	if second.TrackID == first.TrackID || second.TrackID != "album-track-3" {
		t.Fatalf("retry = %#v after failed %#v", second, first)
	}
}

func TestDecideNext_generated_decision_does_not_mutate_explicit_order(t *testing.T) {
	for count := range 100 {
		// Given
		snapshot := testSimilarSnapshot("one", "two")
		snapshot.Explicit = make([]ExplicitEntry, count)
		for index := range snapshot.Explicit {
			snapshot.Explicit[index] = testExplicit("queue-"+strconv.Itoa(index), "track-"+strconv.Itoa(index), true)
		}
		before := slices.Clone(snapshot.Explicit)

		// When
		_ = DecideNext(snapshot, testBoundary("property"))

		// Then
		if !slices.Equal(snapshot.Explicit, before) {
			t.Fatalf("explicit queue changed for count %d: before=%v after=%v", count, before, snapshot.Explicit)
		}
	}
}
