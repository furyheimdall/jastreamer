package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
	"github.com/jakestreamer/jstreamer-server/internal/curation/ranking"
)

type fixtureTrack struct {
	TrackID     catalog.TrackID     `json:"track_id"`
	RecordingID catalog.RecordingID `json:"recording_id"`
	Artist      string              `json:"artist"`
	AlbumID     catalog.AlbumID     `json:"album_id"`
	Signals     candidates.Signals  `json:"signals"`
}

type fixtureCandidate struct {
	Track           fixtureTrack               `json:"track"`
	Tier            candidates.Tier            `json:"tier"`
	SeedAcoustic    ranking.AcousticSimilarity `json:"seed_acoustic"`
	CurrentAcoustic ranking.AcousticSimilarity `json:"current_acoustic"`
}

type fixtureHistory struct {
	Track     fixtureTrack `json:"track"`
	Generated bool         `json:"generated"`
}

type expected struct {
	TrackID        catalog.TrackID     `json:"track_id"`
	RecordingKey   string              `json:"recording_key"`
	RelaxationPass int                 `json:"relaxation_pass"`
	MetadataScore  ranking.BasisPoints `json:"metadata_score"`
	RelatedScore   ranking.BasisPoints `json:"related_score"`
	TiePrefix      string              `json:"tie_prefix"`
}

type fixture struct {
	SessionSeed      string             `json:"session_seed"`
	DecisionSequence uint64             `json:"decision_sequence"`
	Seed             fixtureTrack       `json:"seed"`
	Current          fixtureTrack       `json:"current"`
	Candidates       []fixtureCandidate `json:"candidates"`
	History          []fixtureHistory   `json:"history"`
	Seen             []string           `json:"seen"`
	Policy           ranking.Policy     `json:"policy"`
	Expected         expected           `json:"expected"`
}

type benchmarkResult struct {
	Tracks             int    `json:"tracks"`
	Samples            int    `json:"samples"`
	P95Nanoseconds     int64  `json:"p95_nanoseconds"`
	StorageBytes       uint64 `json:"storage_bytes"`
	ResidentBytes      uint64 `json:"resident_bytes"`
	MaximumNanoseconds int64  `json:"maximum_nanoseconds"`
}

func main() {
	fixturePath := flag.String("fixture", "", "ranking fixture path")
	benchmark := flag.Bool("benchmark", false, "run the 100000-track warm benchmark")
	flag.Parse()
	if *benchmark {
		writeJSON(runBenchmark())
		return
	}
	if *fixturePath == "" {
		exit(errors.New("--fixture is required"))
	}
	payload, err := os.ReadFile(*fixturePath)
	if err != nil {
		exit(err)
	}
	var input fixture
	if err := json.Unmarshal(payload, &input); err != nil {
		exit(err)
	}
	result := ranking.Select(input.request())
	if result.Decision == nil {
		exit(fmt.Errorf("unexpected stop %q after %d passes", result.StopReason, result.PassesExamined))
	}
	explanation := result.Decision.Explanation
	if explanation.TrackID != input.Expected.TrackID ||
		explanation.RecordingKey != input.Expected.RecordingKey ||
		explanation.RelaxationPass != input.Expected.RelaxationPass ||
		explanation.CurrentMetadataScore != input.Expected.MetadataScore ||
		explanation.RelatedScore != input.Expected.RelatedScore ||
		(input.Expected.TiePrefix != "" && explanation.TiePrefix != input.Expected.TiePrefix) {
		exit(fmt.Errorf("decision %+v does not match expected %+v", explanation, input.Expected))
	}
	writeJSON(explanation)
}

func (input fixture) request() ranking.Request {
	values := make([]ranking.RankedCandidate, len(input.Candidates))
	for index, value := range input.Candidates {
		values[index] = ranking.RankedCandidate{
			Candidate:    candidates.Candidate{Track: value.Track.value(), Tier: value.Tier},
			SeedAcoustic: value.SeedAcoustic, CurrentAcoustic: value.CurrentAcoustic,
		}
	}
	history := make([]ranking.StartedTrack, len(input.History))
	for index, value := range input.History {
		history[index] = ranking.StartedTrack{Track: value.Track.value(), Generated: value.Generated}
	}
	seen := make(map[candidates.RecordingKey]struct{}, len(input.Seen))
	for _, value := range input.Seen {
		seen[candidates.RecordingKey(value)] = struct{}{}
	}
	return ranking.Request{
		Candidates: values, Seed: input.Seed.value(), Current: input.Current.value(),
		SessionSeed: input.SessionSeed, DecisionSequence: input.DecisionSequence,
		Policy: input.Policy, History: history, Seen: seen,
	}
}

func (track fixtureTrack) value() candidates.Track {
	return candidates.Track{
		Catalog: catalog.Track{
			TrackID: track.TrackID, RecordingID: track.RecordingID,
			AlbumID: track.AlbumID, Metadata: catalog.Metadata{Artist: track.Artist},
		},
		Signals: track.Signals,
	}
}

func runBenchmark() benchmarkResult {
	const count, samples = 100000, 9
	signal := candidates.Signals{Genres: []string{"rock"}}
	values := make([]ranking.RankedCandidate, count)
	for index := range values {
		id := catalog.TrackID(fmt.Sprintf("track-%06d", index))
		values[index] = ranking.RankedCandidate{Candidate: candidates.Candidate{
			Track: candidates.Track{
				Catalog: catalog.Track{
					TrackID: id, RecordingID: catalog.RecordingID("recording-" + string(id)),
					AlbumID:  catalog.AlbumID("album-" + string(id)),
					Metadata: catalog.Metadata{Artist: "artist-" + string(id)},
				},
				Signals: signal,
			},
			Tier: candidates.TierMetadata,
		}}
	}
	request := ranking.Request{
		Candidates: values, Seed: values[0].Candidate.Track, Current: values[0].Candidate.Track,
		SessionSeed: "benchmark", Policy: ranking.DefaultPolicy(),
	}
	ranking.Select(request)
	durations := make([]time.Duration, samples)
	for index := range durations {
		started := time.Now()
		ranking.Select(request)
		durations[index] = time.Since(started)
	}
	slices.Sort(durations)
	var memoryStats runtime.MemStats
	runtime.ReadMemStats(&memoryStats)
	residentBytes := memoryStats.Sys
	if statm, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(statm))
		if len(fields) > 1 {
			if pages, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
				residentBytes = pages * uint64(os.Getpagesize())
			}
		}
	}
	return benchmarkResult{
		Tracks: count, Samples: samples, P95Nanoseconds: durations[8].Nanoseconds(),
		MaximumNanoseconds: durations[len(durations)-1].Nanoseconds(),
		StorageBytes:       uint64(unsafe.Sizeof(values[0])) * count,
		ResidentBytes:      residentBytes,
	}
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
