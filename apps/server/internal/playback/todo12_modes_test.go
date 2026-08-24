package playback_test

import (
	"context"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestModeExhaustionNeverCrossFallsBack(t *testing.T) {
	tests := []struct {
		name     string
		mode     decision.Policy
		snapshot decision.Snapshot
		want     decision.Reason
	}{
		{
			name:     "album complete does not use similar candidate",
			mode:     decision.PolicyAlbum,
			snapshot: albumCompleteWithSimilarCandidate(),
			want:     decision.ReasonStopAlbumComplete,
		},
		{
			name:     "similar no signal does not use album successor",
			mode:     decision.PolicySimilar,
			snapshot: similarNoSignalWithAlbumSuccessor(),
			want:     decision.ReasonStopSimilarNoSignal,
		},
		{
			name:     "stop ignores every automatic candidate",
			mode:     decision.PolicyStop,
			snapshot: albumCompleteWithSimilarCandidate(),
			want:     decision.ReasonStopModeOff,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			ctx := context.Background()
			store := openTodo12Store(t, todo12Config(t))
			playing := startTodo12SessionWithPolicy(t, todo12SessionFixture{
				store: store, zone: "zone-mode", mode: test.mode,
			})

			// When
			result, err := store.CommitNext(ctx, playback.NextRequest{
				ZoneID:   "zone-mode",
				Boundary: playback.Boundary{ID: "mode-end", PreviousPlayID: playing.CurrentPlay},
				Snapshot: test.snapshot,
			})
			// Then
			if err != nil {
				t.Fatalf("commit next: %v", err)
			}
			if result.Kind != playback.DecisionStop || result.Reason != string(test.want) {
				t.Fatalf("decision = %+v, want exact stop %s", result, test.want)
			}
			commands, err := store.PendingOutbox(ctx, "zone-mode")
			if err != nil {
				t.Fatalf("pending outbox: %v", err)
			}
			if len(commands) != 0 {
				t.Fatalf("cross-fallback created renderer command: %+v", commands)
			}
		})
	}
}

func albumCompleteWithSimilarCandidate() decision.Snapshot {
	snapshot := todo12SimilarSnapshot("similar")
	catalogSnapshot := catalog.EmptySnapshot()
	last := todo12AlbumTrack("last", "release", 1)
	catalogSnapshot.Tracks[last.TrackID] = last
	snapshot.Catalog = catalogSnapshot
	snapshot.Album = decision.AlbumSnapshot{AlbumID: "release", Anchor: last.Order}
	return snapshot
}

func similarNoSignalWithAlbumSuccessor() decision.Snapshot {
	snapshot := todo12SimilarSnapshot()
	catalogSnapshot := catalog.EmptySnapshot()
	anchor := todo12AlbumTrack("anchor", "release", 1)
	successor := todo12AlbumTrack("successor", "release", 2)
	catalogSnapshot.Tracks[anchor.TrackID] = anchor
	catalogSnapshot.Tracks[successor.TrackID] = successor
	snapshot.Catalog = catalogSnapshot
	snapshot.Album = decision.AlbumSnapshot{AlbumID: "release", Anchor: anchor.Order}
	return snapshot
}

func todo12AlbumTrack(id string, albumID catalog.AlbumID, number int) catalog.Track {
	return catalog.Track{
		TrackID: catalog.TrackID(id), RecordingID: catalog.RecordingID("recording-" + id),
		AlbumID: albumID, Available: true,
		Order: catalog.NewOrderKey(catalog.Metadata{Disc: 1, Track: number}, id, catalog.TrackID(id)),
	}
}
