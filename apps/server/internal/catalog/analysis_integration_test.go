package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/analysis"
)

func TestScanWorkerPersistCurationAndRestartExactlyOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	data := makeIntegrationWAV()
	if err := os.WriteFile(filepath.Join(root, "beat.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	schema, _ := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	config := StoreConfig{Path: filepath.Join(t.TempDir(), "catalog.db"), Root: root, Schema: string(schema), Now: time.Now}
	store, err := OpenStore(ctx, config)
	assertNoError(t, err)
	scanner, _ := NewScanner(root)
	scan, err := scanner.Scan(ctx, EmptySnapshot())
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, scan))
	id := onlyTrack(t, scan.Snapshot).TrackID
	p := NewProcessor(store, 2)
	completed := p.Completed()
	p.Start()
	select {
	case got := <-completed:
		if got != id {
			t.Fatalf("completed %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("analysis completion timeout")
	}
	vector, ok, err := store.FeatureVector(ctx, id)
	assertNoError(t, err)
	if !ok || len(vector) != 32 {
		t.Fatalf("persisted vector = %v/%v", vector, ok)
	}
	p.Close()
	assertNoError(t, store.Close())
	reopened, err := OpenStore(ctx, config)
	assertNoError(t, err)
	defer reopened.Close()
	jobs, err := reopened.ScheduleAnalysis(ctx, analysis.CurrentProvenance())
	assertNoError(t, err)
	if len(jobs) != 0 {
		t.Fatalf("unchanged restart jobs = %+v", jobs)
	}
	vector2, ok, err := reopened.FeatureVector(ctx, id)
	assertNoError(t, err)
	if !ok || string(vector2) != string(vector) {
		t.Fatal("restart changed vector")
	}
}

func makeIntegrationWAV() []byte {
	rate := 8000
	s := make([]byte, 44+rate*4*2)
	copy(s, "RIFF")
	put32(s[4:], len(s)-8)
	copy(s[8:], "WAVEfmt ")
	put32(s[16:], 16)
	s[20] = 1
	s[22] = 1
	put32(s[24:], rate)
	put32(s[28:], rate*2)
	s[32] = 2
	s[34] = 16
	copy(s[36:], "data")
	put32(s[40:], len(s)-44)
	for i := 44; i < len(s); i += 2 {
		if (i/2)%(rate/2) < 80 {
			s[i+1] = 40
		}
	}
	return s
}
func put32(b []byte, v int) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func TestStaleRescanPreservesCompletedAnalysisAndChangedContentQueuesOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "track.wav")
	if err := os.WriteFile(path, makeIntegrationWAV(), 0600); err != nil {
		t.Fatal(err)
	}
	schema, _ := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	store, err := OpenStore(ctx, StoreConfig{Path: filepath.Join(t.TempDir(), "catalog.db"), Root: root, Schema: string(schema), Now: time.Now})
	assertNoError(t, err)
	defer store.Close()
	scanner, _ := NewScanner(root)
	first, err := scanner.Scan(ctx, EmptySnapshot())
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, first))
	id := onlyTrack(t, first.Snapshot).TrackID
	p := analysis.CurrentProvenance()
	_, err = store.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	claimed, err := store.ClaimAnalysis(ctx, 1, p)
	assertNoError(t, err)
	vector := []byte{9, 8, 7}
	assertNoError(t, store.FinishAnalysis(ctx, id, p, claimed[0].Fingerprint, vector, ""))
	stale, err := scanner.Scan(ctx, first.Snapshot)
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, stale))
	loaded, err := store.Load(ctx)
	assertNoError(t, err)
	track := loaded.Tracks[id]
	if track.AnalysisStatus != AnalysisComplete || track.AnalysisVector != string(vector) || track.AnalysisProvenance != p {
		t.Fatalf("stale rescan downgraded analysis: %+v", track)
	}
	jobs, err := store.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 0 {
		t.Fatalf("stale rescan jobs = %+v", jobs)
	}
	changed := makeIntegrationWAV()
	changed[len(changed)-1] ^= 1
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	next, err := scanner.Scan(ctx, stale.Snapshot)
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, next))
	loaded, err = store.Load(ctx)
	assertNoError(t, err)
	track = loaded.Tracks[id]
	if track.AnalysisStatus != AnalysisQueued || track.AnalysisVector != "" || track.AnalysisFailure != "" || track.AnalysisProvenance != (analysis.Provenance{}) {
		t.Fatalf("changed reset = %+v", track)
	}
	jobs, err = store.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 1 {
		t.Fatalf("changed jobs = %+v", jobs)
	}
}

func TestProcessorReportsFatalStoreError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	schema, _ := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	store, err := OpenStore(ctx, StoreConfig{Path: filepath.Join(t.TempDir(), "catalog.db"), Root: root, Schema: string(schema), Now: time.Now})
	assertNoError(t, err)
	p := NewProcessor(store, 1)
	assertNoError(t, store.Close())
	p.Start()
	select {
	case err := <-p.Errors():
		if err == nil {
			t.Fatal("nil processor error")
		}
	case <-time.After(time.Second):
		t.Fatal("processor error timeout")
	}
	p.Close()
}
