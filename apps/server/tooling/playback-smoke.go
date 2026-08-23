//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
	"github.com/jakestreamer/jstreamer-server/internal/curation/ranking"
	"github.com/jakestreamer/jstreamer-server/internal/decision"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
)

type smokeResult struct {
	Revision        playback.Revision     `json:"revision"`
	Transport       playback.Transport    `json:"transport"`
	QueueStates     []playback.QueueState `json:"queue_states"`
	PendingCommands int                   `json:"pending_commands"`
	StaleRejected   bool                  `json:"stale_revision_rejected"`
	SessionEnded    bool                  `json:"session_ended"`
	SQLiteVersion   int                   `json:"sqlite_version"`
	AutomaticReason string                `json:"automatic_reason"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) != 3 {
		return errors.New("usage: go run ./tooling/playback-smoke.go <base-migration> <todo12-migration>")
	}
	directory, err := os.MkdirTemp("", "jstreamer-playback-smoke-")
	if err != nil {
		return fmt.Errorf("create smoke directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	config := playback.Config{
		Path: filepath.Join(directory, "playback.sqlite"), MigrationPath: os.Args[1], ExpansionPath: os.Args[2],
		BackupDirectory: filepath.Join(directory, "backups"),
		SupportedSchema: playback.CurrentSchemaVersion, JournalMode: playback.JournalRollback,
	}
	ctx := context.Background()
	store, err := playback.Open(ctx, config)
	if err != nil {
		return err
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "smoke-zone", IdempotencyKey: "enqueue-1",
		Tracks: []playback.QueueTrack{{ID: "track-a", Available: true}, {ID: "track-b", Available: true}},
	}); err != nil {
		return err
	}
	_, staleErr := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "smoke-zone", IdempotencyKey: "stale", ExpectedRevision: 0,
		Tracks: []playback.QueueTrack{{ID: "track-c", Available: true}},
	})
	if !errors.Is(staleErr, playback.ErrRevisionConflict) {
		return fmt.Errorf("stale revision was not rejected: %w", staleErr)
	}
	first, err := store.ReserveNext(ctx, "smoke-zone", playback.Boundary{ID: "start"})
	if err != nil {
		return err
	}
	if _, err := store.ConfirmStart(ctx, "smoke-zone", first.PlayID); err != nil {
		return err
	}
	if err := store.Pause(ctx, "smoke-zone"); err != nil {
		return err
	}
	if err := acknowledgePending(ctx, store, "smoke-zone"); err != nil {
		return err
	}
	if err := store.Resume(ctx, "smoke-zone"); err != nil {
		return err
	}
	if err := acknowledgePending(ctx, store, "smoke-zone"); err != nil {
		return err
	}
	if _, err := store.ReserveNext(ctx, "smoke-zone", playback.Boundary{ID: "next", PreviousPlayID: first.PlayID}); err != nil {
		return err
	}
	if err := store.Stop(ctx, "smoke-zone"); err != nil {
		return err
	}
	if err := acknowledgePending(ctx, store, "smoke-zone"); err != nil {
		return err
	}
	if _, err := store.UpdateContinuationPolicy(ctx, playback.PolicyUpdate{
		ZoneID: "automatic-zone", Mode: decision.PolicySimilar,
		ArtistGap: ranking.DefaultArtistGap, AlbumGap: ranking.DefaultAlbumGap,
	}); err != nil {
		return err
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "automatic-zone", IdempotencyKey: "seed",
		Tracks: []playback.QueueTrack{{ID: "seed", Available: true}},
	}); err != nil {
		return err
	}
	seed, err := store.ReserveNext(ctx, "automatic-zone", playback.Boundary{ID: "start"})
	if err != nil {
		return err
	}
	automaticPlaying, err := store.ConfirmStart(ctx, "automatic-zone", seed.PlayID)
	if err != nil {
		return err
	}
	next := playback.NextRequest{
		ZoneID:   "automatic-zone",
		Boundary: playback.Boundary{ID: "automatic-1", PreviousPlayID: automaticPlaying.CurrentPlay},
		Snapshot: smokeSimilarSnapshot(),
	}
	preview, err := store.PreviewNext(ctx, next)
	if err != nil {
		return err
	}
	if preview.TrackID != "generated" {
		return fmt.Errorf("automatic preview selected %q", preview.TrackID)
	}
	automatic, err := store.CommitNext(ctx, next)
	if err != nil {
		return err
	}
	if _, err := store.Enqueue(ctx, playback.EnqueueRequest{
		ZoneID: "automatic-zone", IdempotencyKey: "after-cutoff",
		ExpectedRevision: automatic.Revision,
		Tracks:           []playback.QueueTrack{{ID: "explicit-after", Available: true}},
	}); err != nil {
		return err
	}
	if err := store.Close(); err != nil {
		return err
	}
	reopened, err := playback.Open(ctx, config)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, reopened.Close()) }()
	snapshot, err := reopened.Snapshot(ctx, "smoke-zone")
	if err != nil {
		return err
	}
	pending, err := reopened.PendingOutbox(ctx, "smoke-zone")
	if err != nil {
		return err
	}
	states := make([]playback.QueueState, len(snapshot.Queue))
	for index, entry := range snapshot.Queue {
		states[index] = entry.State
	}
	output, err := json.Marshal(smokeResult{
		Revision: snapshot.Revision, Transport: snapshot.Transport, QueueStates: states,
		PendingCommands: len(pending), StaleRejected: true, SessionEnded: snapshot.SessionID == "",
		SQLiteVersion:   reopened.SQLiteVersion(),
		AutomaticReason: automatic.Reason,
	})
	if err != nil {
		return fmt.Errorf("encode smoke result: %w", err)
	}
	fmt.Println(string(output))
	return nil
}

func smokeSimilarSnapshot() decision.Snapshot {
	seed := candidates.Track{
		Catalog: catalog.Track{
			TrackID: "seed", RecordingID: "recording-seed", Format: catalog.FormatFLAC,
			Metadata: catalog.Metadata{Artist: "seed-artist"}, Available: true,
		},
		Signals: candidates.Signals{Genres: []string{"ambient"}},
	}
	generated := candidates.Track{
		Catalog: catalog.Track{
			TrackID: "generated", RecordingID: "recording-generated", Format: catalog.FormatFLAC,
			Metadata: catalog.Metadata{Artist: "generated-artist"}, Available: true,
		},
		Signals: candidates.Signals{Genres: []string{"ambient"}},
	}
	return decision.Snapshot{Similar: decision.SimilarSnapshot{
		Index: candidates.NewIndex(1, []candidates.Track{seed, generated}),
		Seed:  seed, Current: seed, PageSize: 1,
		RankingPolicy: ranking.DefaultPolicy(), SessionSeed: "smoke-session",
	}}
}

func acknowledgePending(ctx context.Context, store *playback.Store, zone playback.ZoneID) error {
	commands, err := store.PendingOutbox(ctx, zone)
	if err != nil {
		return err
	}
	for _, command := range commands {
		if command.Type == "play" {
			continue
		}
		if err := store.AcknowledgeOutbox(ctx, zone, command.ID); err != nil {
			return err
		}
	}
	return nil
}
