//go:build ignore

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
)

type output struct {
	Revision         uint64            `json:"revision"`
	TrackCount       int               `json:"track_count"`
	CandidateIDs     []catalog.TrackID `json:"candidate_ids"`
	TotalEligible    int               `json:"total_eligible"`
	FilteredEligible int               `json:"filtered_eligible"`
	ScoredEligible   int               `json:"scored_eligible"`
	PagesRead        int               `json:"pages_read"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) != 3 {
		return errors.New("usage: go run ./tooling/candidate-smoke.go <fixture-dir> <migration>")
	}
	temp, err := os.MkdirTemp("", "jastreamer-candidate-smoke-")
	if err != nil {
		return fmt.Errorf("create smoke directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(temp)) }()
	root := filepath.Join(temp, "music")
	if err := os.Mkdir(root, 0o700); err != nil {
		return fmt.Errorf("create music root: %w", err)
	}
	for _, name := range []string{"real.flac", "real.mp3", "real.ogg", "real.opus", "real.wav"} {
		encoded, readErr := os.ReadFile(filepath.Join(os.Args[1], name+".b64"))
		if readErr != nil {
			return fmt.Errorf("read %s: %w", name, readErr)
		}
		data, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if decodeErr != nil {
			return fmt.Errorf("decode %s: %w", name, decodeErr)
		}
		if writeErr := os.WriteFile(filepath.Join(root, name), data, 0o600); writeErr != nil {
			return fmt.Errorf("write %s: %w", name, writeErr)
		}
	}
	schema, err := os.ReadFile(os.Args[2])
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	scanner, err := catalog.NewScanner(root)
	if err != nil {
		return err
	}
	scan, err := scanner.Scan(context.Background(), catalog.EmptySnapshot())
	if err != nil {
		return err
	}
	ids := make([]catalog.TrackID, 0, len(scan.Snapshot.Tracks))
	for id := range scan.Snapshot.Tracks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) < 2 {
		return fmt.Errorf("fixture catalog has %d tracks, want at least two", len(ids))
	}
	seedID, candidateID := ids[0], catalog.TrackID("")
	for _, id := range ids[1:] {
		if scan.Snapshot.Tracks[id].RecordingID != scan.Snapshot.Tracks[seedID].RecordingID {
			candidateID = id
			break
		}
	}
	if candidateID == "" {
		return errors.New("fixture catalog has no distinct recording pair")
	}
	for _, id := range ids {
		track := scan.Snapshot.Tracks[id]
		track.Metadata.Genres, track.Metadata.Styles = nil, nil
		track.Metadata.Moods, track.Metadata.LocalTags = nil, nil
		if id == seedID || id == candidateID {
			track.Metadata.Genres = []string{"Ambient"}
		}
		scan.Snapshot.Tracks[id] = track
	}
	config := catalog.StoreConfig{
		Path: filepath.Join(temp, "catalog.sqlite"), Root: root,
		Schema: string(schema), Now: time.Now,
	}
	store, err := catalog.OpenStore(context.Background(), config)
	if err != nil {
		return err
	}
	if err := store.Save(context.Background(), scan); err != nil {
		return errors.Join(err, store.Close())
	}
	if err := store.Close(); err != nil {
		return err
	}
	store, err = catalog.OpenStore(context.Background(), config)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	loaded, err := store.Load(context.Background())
	if err != nil {
		return err
	}
	index := candidates.IndexSnapshot(loaded)
	seed := index.Tracks[slices.IndexFunc(index.Tracks, func(track candidates.Track) bool { return track.Catalog.TrackID == seedID })]
	result := candidates.Retrieve(candidates.Request{
		Index: index, CatalogRevision: loaded.Revision, Seed: seed, Current: seed, PageSize: 1,
	})
	candidateIDs := make([]catalog.TrackID, len(result.Candidates))
	for index := range result.Candidates {
		candidateIDs[index] = result.Candidates[index].Track.Catalog.TrackID
	}
	if !slices.Contains(candidateIDs, candidateID) {
		return fmt.Errorf("persisted related track %q absent from %v", candidateID, candidateIDs)
	}
	encoded, err := json.Marshal(output{
		Revision: loaded.Revision, TrackCount: len(index.Tracks), CandidateIDs: candidateIDs,
		TotalEligible: result.TotalEligible, FilteredEligible: result.FilteredEligible,
		ScoredEligible: result.ScoredEligible, PagesRead: result.PagesRead,
	})
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}
