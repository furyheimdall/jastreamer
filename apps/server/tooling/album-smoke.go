//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type smokeResult struct {
	Selected []catalog.TrackID `json:"selected"`
	Stop     decision.Reason   `json:"stop"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) != 3 {
		return errors.New("usage: go run ./tooling/album-smoke.go <base-migration> <todo12-migration>")
	}
	ctx := context.Background()
	directory, err := os.MkdirTemp("", "jastreamer-album-smoke-")
	if err != nil {
		return fmt.Errorf("create album smoke directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	store, err := playback.Open(ctx, playback.Config{
		Path:          filepath.Join(directory, "playback.sqlite"),
		MigrationPath: os.Args[1], ExpansionPath: os.Args[2],
		BackupDirectory: filepath.Join(directory, "backups"),
		SupportedSchema: playback.CurrentSchemaVersion, JournalMode: playback.JournalRollback,
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if _, err := store.UpdateContinuationPolicy(ctx, playback.PolicyUpdate{
		ZoneID: "album-smoke", Mode: decision.PolicyAlbum, ArtistGap: 4, AlbumGap: 10,
	}); err != nil {
		return err
	}
	snapshot := catalog.EmptySnapshot()
	for _, track := range []catalog.Track{
		newTrack("disc-1-track-1", 1, 1),
		newTrack("disc-1-track-2", 1, 2),
		newTrack("disc-2-track-1", 2, 1),
	} {
		snapshot.Tracks[track.TrackID] = track
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "album-smoke", IdempotencyKey: "anchor",
		Tracks: []playback.QueueTrack{{ID: "disc-1-track-1", Available: true}},
	}); err != nil {
		return err
	}
	anchor, err := store.CommitNext(ctx, playback.NextRequest{
		ZoneID: "album-smoke", Boundary: playback.Boundary{ID: "start"},
		Snapshot: decision.Snapshot{Catalog: snapshot},
	})
	if err != nil {
		return err
	}
	if _, err := store.ConfirmStart(ctx, "album-smoke", anchor.PlayID); err != nil {
		return err
	}
	result := smokeResult{}
	previous := anchor.PlayID
	for sequence := 1; ; sequence++ {
		selected, err := store.CommitNext(ctx, playback.NextRequest{
			ZoneID: "album-smoke",
			Boundary: playback.Boundary{
				ID: playback.BoundaryID(fmt.Sprintf("album-%d", sequence)), PreviousPlayID: previous,
			},
			Snapshot: decision.Snapshot{Catalog: snapshot},
		})
		if err != nil {
			return err
		}
		switch selected.Kind {
		case playback.DecisionPlay:
			result.Selected = append(result.Selected, catalog.TrackID(selected.TrackID))
			if _, err := store.ConfirmStart(ctx, "album-smoke", selected.PlayID); err != nil {
				return err
			}
			previous = selected.PlayID
		case playback.DecisionStop:
			result.Stop = decision.Reason(selected.Reason)
		default:
			return fmt.Errorf("unexpected outcome %s", selected.Kind)
		}
		if result.Stop != "" {
			break
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode album smoke result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

func newTrack(id string, disc, number int) catalog.Track {
	return catalog.Track{
		TrackID: catalog.TrackID(id), RecordingID: catalog.RecordingID(id),
		AlbumID: "release", Available: true,
		Order: catalog.NewOrderKey(
			catalog.Metadata{Disc: disc, Track: number}, id, catalog.TrackID(id),
		),
	}
}
