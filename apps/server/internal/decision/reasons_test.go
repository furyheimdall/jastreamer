package decision

import "testing"

func TestDecideNext_album_without_anchor_returns_exact_reason(t *testing.T) {
	// Given
	snapshot := testAlbumSnapshot(2)
	snapshot.Album.AlbumID = ""

	// When
	outcome := DecideNext(snapshot, testBoundary("no-album"))

	// Then
	if stop := requireStop(t, outcome); stop.Reason != ReasonStopNoAlbum {
		t.Fatalf("reason = %s, want %s", stop.Reason, ReasonStopNoAlbum)
	}
}

func TestDecideNext_similar_play_returns_exact_reason(t *testing.T) {
	// Given
	snapshot := testSimilarSnapshot("generated")

	// When
	outcome := DecideNext(snapshot, testBoundary("similar-play"))

	// Then
	if play := requirePlay(t, outcome); play.Reason != ReasonPlaySimilar {
		t.Fatalf("reason = %s, want %s", play.Reason, ReasonPlaySimilar)
	}
}
