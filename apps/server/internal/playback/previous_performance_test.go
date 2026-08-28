package playback

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"
)

const (
	previousPerformanceZones        = 8
	previousPerformanceQueueEntries = 10_000
	previousPerformanceSamples      = 20
	previousPerformanceThreshold    = 500 * time.Millisecond
)

type previousPerformanceZone struct {
	id       ZoneID
	revision Revision
}

func Test_Performance_previousMutation_10000EntriesEightZonesP95Under500ms(t *testing.T) {
	// Given
	ctx := context.Background()
	store := openTestStore(t, testConfig(t))
	zones := make([]previousPerformanceZone, previousPerformanceZones)
	entriesPerZone := previousPerformanceQueueEntries
	for zoneIndex := range previousPerformanceZones {
		zoneID := ZoneID(fmt.Sprintf("performance-zone-%d", zoneIndex))
		rendererID := RendererID(fmt.Sprintf("performance-renderer-%d", zoneIndex))
		if _, err := store.CreateZone(ctx, ZoneDefinition{ID: zoneID, DisplayName: string(zoneID)}); err != nil {
			t.Fatalf("create zone %d: %v", zoneIndex, err)
		}
		if _, err := store.UpsertCustomRenderer(ctx, CustomRenderer{
			ID: rendererID, DisplayName: string(rendererID), State: RendererConnected, ProtocolMajor: 3,
			Capabilities: []string{"command:play", "command:seek"},
			LastSeenAt:   time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("create renderer %d: %v", zoneIndex, err)
		}
		if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: zoneID, RendererID: rendererID}); err != nil {
			t.Fatalf("assign renderer %d: %v", zoneIndex, err)
		}
		tracks := make([]QueueTrack, entriesPerZone)
		for trackIndex := range entriesPerZone {
			tracks[trackIndex] = QueueTrack{ID: TrackID(fmt.Sprintf("z%d-track-%04d", zoneIndex, trackIndex)), Available: true}
		}
		enqueued, err := store.Enqueue(ctx, EnqueueRequest{ZoneID: zoneID, IdempotencyKey: "performance-seed", Tracks: tracks})
		if err != nil {
			t.Fatalf("enqueue zone %d: %v", zoneIndex, err)
		}
		started, err := store.MutateTransport(ctx, TransportMutationRequest{
			ZoneID: zoneID, IdempotencyKey: "performance-start", ExpectedRevision: enqueued.Revision, Command: TransportStart,
		})
		if err != nil {
			t.Fatalf("start zone %d: %v", zoneIndex, err)
		}
		if _, err := store.ConfirmStart(ctx, zoneID, started.PlayID); err != nil {
			t.Fatalf("confirm zone %d: %v", zoneIndex, err)
		}
		current := started.PlayID
		for historyIndex := 1; historyIndex <= previousPerformanceSamples; historyIndex++ {
			decision, err := store.CommitNext(ctx, NextRequest{
				ZoneID:   zoneID,
				Boundary: Boundary{ID: BoundaryID(fmt.Sprintf("performance-ended-%d", historyIndex)), PreviousPlayID: current},
			})
			if err != nil {
				t.Fatalf("advance zone %d history %d: %v", zoneIndex, historyIndex, err)
			}
			if _, err := store.ConfirmStart(ctx, zoneID, decision.PlayID); err != nil {
				t.Fatalf("confirm zone %d history %d: %v", zoneIndex, historyIndex, err)
			}
			current = decision.PlayID
		}
		snapshot, err := store.Snapshot(ctx, zoneID)
		if err != nil {
			t.Fatalf("snapshot zone %d: %v", zoneIndex, err)
		}
		if len(snapshot.Queue) != previousPerformanceQueueEntries {
			t.Fatalf("queue entries per zone = %d, want %d", len(snapshot.Queue), previousPerformanceQueueEntries)
		}
		zones[zoneIndex] = previousPerformanceZone{id: zoneID, revision: snapshot.Revision}
	}
	durations := make([]time.Duration, 0, previousPerformanceZones*previousPerformanceSamples)

	// When
	for sampleIndex := range previousPerformanceSamples {
		for zoneIndex := range zones {
			request := TransportMutationRequest{
				ZoneID:           zones[zoneIndex].id,
				IdempotencyKey:   fmt.Sprintf("performance-previous-%02d", sampleIndex),
				ExpectedRevision: zones[zoneIndex].revision,
				Command:          TransportPrevious,
				PositionMS:       0,
			}
			startedAt := time.Now()
			result, err := store.MutateTransport(ctx, request)
			durations = append(durations, time.Since(startedAt))
			if err != nil {
				t.Fatalf("previous zone %d sample %d: %v", zoneIndex, sampleIndex, err)
			}
			wantTrack := TrackID(fmt.Sprintf("z%d-track-%04d", zoneIndex, previousPerformanceSamples-1-sampleIndex))
			if result.TrackID != wantTrack {
				t.Fatalf("continuation zone %d sample %d = %s, want %s", zoneIndex, sampleIndex, result.TrackID, wantTrack)
			}
			snapshot, err := store.ConfirmStart(ctx, zones[zoneIndex].id, result.PlayID)
			if err != nil {
				t.Fatalf("confirm previous zone %d sample %d: %v", zoneIndex, sampleIndex, err)
			}
			zones[zoneIndex].revision = snapshot.Revision
		}
	}

	// Then
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	p95 := durations[(len(durations)*95+99)/100-1]
	t.Logf("samples=%d queue_entries_per_zone=%d queue_entries_total=%d zones=%d p95=%s threshold=%s durations=%v host_os=%s host_arch=%s host_cpus=%d",
		len(durations), previousPerformanceQueueEntries, previousPerformanceQueueEntries*previousPerformanceZones, previousPerformanceZones, p95,
		previousPerformanceThreshold, durations, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	if p95 >= previousPerformanceThreshold {
		t.Fatalf("previous mutation p95 %s exceeded %s", p95, previousPerformanceThreshold)
	}
}
