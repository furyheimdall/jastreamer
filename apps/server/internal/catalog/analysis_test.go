package catalog

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
)

func TestAnalysisStateDurableAndScheduledOnlyOnProvenanceChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeRealFixture(t, "real.wav", filepath.Join(root, "track.wav"))
	schema, _ := os.ReadFile(filepath.Join("..", "..", "migrations", "001_catalog.sql"))
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	config := StoreConfig{Path: dbPath, Root: root, Schema: string(schema), Now: func() time.Time { return time.Unix(1, 0) }}
	store, err := OpenStore(ctx, config)
	assertNoError(t, err)
	scanner, _ := NewScanner(root)
	scan, err := scanner.Scan(ctx, EmptySnapshot())
	assertNoError(t, err)
	assertNoError(t, store.Save(ctx, scan))
	p := analysis.CurrentProvenance()
	jobs, err := store.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	claimed, err := store.ClaimAnalysis(ctx, 2, p)
	assertNoError(t, err)
	if len(claimed) != 1 || claimed[0].Status != AnalysisRunning {
		t.Fatalf("claimed = %+v", claimed)
	}
	vector := []byte{1, 2, 3}
	assertNoError(t, store.FinishAnalysis(ctx, claimed[0].TrackID, p, claimed[0].Fingerprint, vector, ""))
	jobs, err = store.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 0 {
		t.Fatalf("unchanged jobs = %+v", jobs)
	}
	got, ok, err := store.FeatureVector(ctx, claimed[0].TrackID)
	assertNoError(t, err)
	if !ok || !bytes.Equal(got, vector) {
		t.Fatalf("curation vector = %v/%v", got, ok)
	}
	assertNoError(t, store.Close())
	reopened, err := OpenStore(ctx, config)
	assertNoError(t, err)
	defer reopened.Close()
	loaded, err := reopened.Load(ctx)
	assertNoError(t, err)
	track := loaded.Tracks[claimed[0].TrackID]
	if track.AnalysisStatus != AnalysisComplete || track.AnalysisFingerprint != claimed[0].Fingerprint || track.AnalysisProvenance != p || track.AnalysisFailure != "" || track.AnalysisVector != string(vector) {
		t.Fatalf("round trip = %+v", track)
	}
	changes := []func(*analysis.Provenance){func(v *analysis.Provenance) { v.SchemaVersion++ }, func(v *analysis.Provenance) { v.AnalyzerID += "-next" }, func(v *analysis.Provenance) { v.AnalyzerVersion = "next" }, func(v *analysis.Provenance) { v.NormalizerID += "-next" }, func(v *analysis.Provenance) { v.NormalizerVersion = "next" }}
	for index, change := range changes {
		change(&p)
		jobs, err = reopened.ScheduleAnalysis(ctx, p)
		assertNoError(t, err)
		if len(jobs) != 1 {
			t.Fatalf("provenance change %d jobs = %+v", index, jobs)
		}
		claimed, err = reopened.ClaimAnalysis(ctx, 1, p)
		assertNoError(t, err)
		assertNoError(t, reopened.FinishAnalysis(ctx, claimed[0].TrackID, p, claimed[0].Fingerprint, vector, ""))
	}
	jobs, err = reopened.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 0 {
		t.Fatalf("stable provenance repeated: %+v", jobs)
	}
	loaded.Tracks[claimed[0].TrackID] = track
	changed := loaded.Tracks[claimed[0].TrackID]
	changed.AudioFingerprint = "changed-content"
	loaded.Tracks[claimed[0].TrackID] = changed
	assertNoError(t, reopened.Save(ctx, ScanResult{Snapshot: loaded, Complete: true}))
	jobs, err = reopened.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 1 {
		t.Fatalf("fingerprint change jobs = %+v", jobs)
	}
	claimed, err = reopened.ClaimAnalysis(ctx, 1, p)
	assertNoError(t, err)
	jobs, err = reopened.ScheduleAnalysis(ctx, p)
	assertNoError(t, err)
	if len(jobs) != 1 {
		t.Fatalf("running restart jobs = %+v", jobs)
	}
	claimed, err = reopened.ClaimAnalysis(ctx, 1, p)
	assertNoError(t, err)
	assertNoError(t, reopened.FinishAnalysis(ctx, claimed[0].TrackID, p, claimed[0].Fingerprint, nil, "decode failed"))
	failed, err := reopened.Load(ctx)
	assertNoError(t, err)
	failure := failed.Tracks[claimed[0].TrackID]
	if failure.AnalysisStatus != AnalysisFailed || failure.AnalysisFailure != "decode failed" || failure.AnalysisVector != "" {
		t.Fatalf("failure round trip = %+v", failure)
	}
}
