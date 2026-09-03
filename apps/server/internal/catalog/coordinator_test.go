package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorCancelsSingleFlightScan_andRestartKeepsLastCompleteSnapshot(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	base := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "catalog-state.json")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	calls := 0
	scan := func(scanCtx context.Context, root Root, previous Snapshot) (ScanResult, error) {
		calls++
		if calls == 1 {
			next := EmptySnapshot()
			next.Tracks["stable"] = Track{RootID: root.ID, TrackID: "stable", Available: true}
			return ScanResult{Snapshot: next, Complete: true}, nil
		}
		started <- struct{}{}
		select {
		case <-scanCtx.Done():
			return ScanResult{Snapshot: previous, Complete: false}, scanCtx.Err()
		case <-release:
			return ScanResult{Snapshot: previous, Complete: true}, nil
		}
	}
	config := CoordinatorConfig{StatePath: statePath, AllowedBases: []string{base}, Now: fixedCatalogTime, Scan: scan}
	coordinator, err := OpenCoordinator(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	root, err := coordinator.AddRoot(ctx, base, "Library")
	if err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.StartScan(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Wait(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	prior := coordinator.Snapshot()
	second, err := coordinator.StartScan(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-started:
	}

	// When
	_, concurrentErr := coordinator.StartScan(ctx, root.ID)
	cancelErr := coordinator.CancelScan(ctx, second.ID)
	cancelled, waitErr := coordinator.Wait(ctx, second.ID)
	closeErr := coordinator.Close()
	reopened, restartErr := OpenCoordinator(ctx, config)
	if restartErr == nil {
		t.Cleanup(func() { _ = reopened.Close() })
	}

	// Then
	if !errors.Is(concurrentErr, ErrScanInProgress) || cancelErr != nil || waitErr != nil || closeErr != nil || restartErr != nil {
		t.Fatalf("lifecycle errors = concurrent %v cancel %v wait %v close %v restart %v", concurrentErr, cancelErr, waitErr, closeErr, restartErr)
	}
	if cancelled.Status != ScanCancelled || reopened.Snapshot().Revision != prior.Revision || len(reopened.Snapshot().Tracks) != 1 {
		t.Fatalf("cancel/restart = job %+v snapshot %+v prior %+v", cancelled, reopened.Snapshot(), prior)
	}
}

func TestCoordinatorReconcileRoots_cancels_in_progress_scan_and_applies_desired_set(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	base := t.TempDir()
	left := filepath.Join(base, "left")
	right := filepath.Join(base, "right")
	for _, path := range []string{left, right} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{}, 1)
	scan := func(scanCtx context.Context, root Root, previous Snapshot) (ScanResult, error) {
		started <- struct{}{}
		<-scanCtx.Done()
		return ScanResult{Snapshot: previous, Complete: false}, scanCtx.Err()
	}
	coordinator, err := OpenCoordinator(ctx, CoordinatorConfig{
		StatePath: filepath.Join(t.TempDir(), "state.json"), AllowedBases: []string{base},
		Now: fixedCatalogTime, Scan: scan,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if err := coordinator.ReconcileRoots(ctx, []DesiredRoot{{ID: "left", DisplayName: "Left", Path: left}}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.StartScan(ctx, "left"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-started:
	}

	// When
	reconcileErr := coordinator.ReconcileRoots(ctx, []DesiredRoot{{ID: "right", DisplayName: "Right", Path: right}})

	// Then
	roots := coordinator.Roots()
	if reconcileErr != nil || len(roots) != 1 || roots[0].ID != "right" {
		t.Fatalf("reconcile error=%v roots=%+v", reconcileErr, roots)
	}
}

func TestCoordinatorReconcileRoots_replaces_exact_set_and_rolls_back_on_persist_failure(t *testing.T) {
	// Given
	base := t.TempDir()
	left := filepath.Join(base, "left")
	right := filepath.Join(base, "right")
	for _, path := range []string{left, right} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stateDirectory := t.TempDir()
	statePath := filepath.Join(stateDirectory, "state.json")
	coordinator, err := OpenCoordinator(t.Context(), CoordinatorConfig{
		StatePath: statePath, AllowedBases: []string{base}, Now: fixedCatalogTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if err := coordinator.ReconcileRoots(t.Context(), []DesiredRoot{{ID: "left", DisplayName: "Left", Path: left}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}

	// When
	reconcileErr := coordinator.ReconcileRoots(t.Context(), []DesiredRoot{{ID: "right", DisplayName: "Right", Path: right}})

	// Then
	roots := coordinator.Roots()
	if reconcileErr == nil || len(roots) != 1 || roots[0].ID != "left" || roots[0].CanonicalPath != left {
		t.Fatalf("reconcile error=%v roots=%+v", reconcileErr, roots)
	}
}

func TestCoordinatorReconcileRoots_preserves_authoritative_ids_across_restart(t *testing.T) {
	// Given
	base := t.TempDir()
	rootPath := filepath.Join(base, "music")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "state.json")
	config := CoordinatorConfig{StatePath: statePath, AllowedBases: []string{base}, Now: fixedCatalogTime}
	coordinator, err := OpenCoordinator(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileRoots(t.Context(), []DesiredRoot{{ID: "settings-id", DisplayName: "Music", Path: rootPath}}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}

	// When
	restarted, err := OpenCoordinator(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })

	// Then
	roots := restarted.Roots()
	if len(roots) != 1 || roots[0].ID != "settings-id" || roots[0].CanonicalPath != rootPath {
		t.Fatalf("restarted roots = %+v", roots)
	}
}

func TestCoordinatorStartsFromLastCompleteSnapshot_whenStateDoesNotExist(t *testing.T) {
	// Given
	initial := EmptySnapshot()
	initial.Revision = 9
	initial.Tracks["existing"] = Track{TrackID: "existing", Available: true}

	// When
	coordinator, err := OpenCoordinator(t.Context(), CoordinatorConfig{
		StatePath: filepath.Join(t.TempDir(), "state.json"), AllowedBases: []string{t.TempDir()},
		Now: fixedCatalogTime, InitialSnapshot: initial,
	})
	if err != nil {
		t.Fatalf("open coordinator: %v", err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })

	// Then
	if snapshot := coordinator.Snapshot(); snapshot.Revision != 9 || len(snapshot.Tracks) != 1 {
		t.Fatalf("initial snapshot = %+v", snapshot)
	}
}

func TestCoordinatorMarksInterruptedJobCancelled_whenProcessRestarts(t *testing.T) {
	// Given: persisted state from a process interrupted while a job was running.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	base := t.TempDir()
	path := filepath.Join(t.TempDir(), "state.json")
	registry, err := NewRootRegistry([]string{base})
	if err != nil {
		t.Fatal(err)
	}
	root, err := registry.Add(base, "Library")
	if err != nil {
		t.Fatal(err)
	}
	state := coordinatorState{Roots: []persistedRoot{{ID: root.ID, DisplayName: root.DisplayName, CanonicalPath: root.CanonicalPath}}, Jobs: []ScanJob{{ID: "job-1", RootID: root.ID, Status: ScanRunning}}, Snapshot: EmptySnapshot()}
	if err := writeCoordinatorState(path, state); err != nil {
		t.Fatal(err)
	}

	// When
	coordinator, err := OpenCoordinator(ctx, CoordinatorConfig{
		StatePath: path, AllowedBases: []string{base}, Now: fixedCatalogTime,
		Scan: func(_ context.Context, _ Root, previous Snapshot) (ScanResult, error) {
			return ScanResult{Snapshot: previous, Complete: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	job, err := coordinator.Job("job-1")

	// Then
	if err != nil || job.Status != ScanCancelled || job.ErrorCode != "PROCESS_RESTARTED" {
		t.Fatalf("recovered job = %+v, %v", job, err)
	}
}

func fixedCatalogTime() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }
