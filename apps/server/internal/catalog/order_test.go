package catalog

import (
	"slices"
	"testing"
)

func TestOrderMultiDiscDuplicateAndUnknown_when_numbers_overlap(t *testing.T) {
	// Given
	tracks := []Track{
		{TrackID: "z", Order: NewOrderKey(Metadata{Disc: 0, Track: 0}, "disc x/track 2.flac", "z")},
		{TrackID: "b", Order: NewOrderKey(Metadata{Disc: 1, Track: 10}, "disc 1/track 10.flac", "b")},
		{TrackID: "c", Order: NewOrderKey(Metadata{Disc: 1, Track: 2}, "disc 1/track 2 copy.flac", "c")},
		{TrackID: "a", Order: NewOrderKey(Metadata{Disc: 1, Track: 2}, "disc 1/track 2.flac", "a")},
		{TrackID: "d", Order: NewOrderKey(Metadata{Disc: 2, Track: 1}, "disc 2/track 1.flac", "d")},
	}
	// When
	slices.SortFunc(tracks, CompareTrackOrder)
	// Then
	want := []TrackID{"c", "a", "b", "d", "z"}
	for index, id := range want {
		if tracks[index].TrackID != id {
			t.Fatalf("order[%d] = %q, want %q; all=%+v", index, tracks[index].TrackID, id, tracks)
		}
	}
}

func TestDuplicateRecordingKeepsDistinctTracks_when_files_share_embedded_recording(t *testing.T) {
	// Given
	metadata := Metadata{Title: "Same", RecordingID: "mb-recording-1"}
	// When
	first := identities("one.flac", "content-one", "audio-one", metadata)
	second := identities("two.flac", "content-two", "audio-two", metadata)
	// Then
	if first.RecordingID != second.RecordingID || first.TrackID == second.TrackID {
		t.Fatalf("first=%+v second=%+v, want shared recording and distinct tracks", first, second)
	}
}

func TestCanonicalOrderUsesTrackID_when_paths_are_unicode_equivalent(t *testing.T) {
	// Given
	tracks := []Track{
		{TrackID: "z", Order: NewOrderKey(Metadata{}, "e\u0301.flac", "z")},
		{TrackID: "a", Order: NewOrderKey(Metadata{}, "\u00e9.flac", "a")},
	}

	// When
	slices.SortFunc(tracks, CompareTrackOrder)

	// Then
	if tracks[0].TrackID != "a" {
		t.Fatalf("first track = %q, want final TrackID tie-break", tracks[0].TrackID)
	}
}
