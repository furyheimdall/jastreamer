package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorPublishesCompleteAggregate_whenTwoRootsFinish(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	base := t.TempDir()
	leftPath, rightPath := filepath.Join(base, "left"), filepath.Join(base, "right")
	for _, path := range []string{leftPath, rightPath} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	scan := func(_ context.Context, root Root, _ Snapshot) (ScanResult, error) {
		id := TrackID(root.DisplayName)
		snapshot := EmptySnapshot()
		snapshot.Tracks[id] = Track{RootID: root.ID, TrackID: id, Available: true}
		return ScanResult{Snapshot: snapshot, Complete: true}, nil
	}
	coordinator, err := OpenCoordinator(ctx, CoordinatorConfig{
		StatePath: filepath.Join(t.TempDir(), "state.json"), AllowedBases: []string{base},
		Now: fixedCatalogTime, Scan: scan,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	left, err := coordinator.AddRoot(ctx, leftPath, "left")
	if err != nil {
		t.Fatal(err)
	}
	right, err := coordinator.AddRoot(ctx, rightPath, "right")
	if err != nil {
		t.Fatal(err)
	}

	// When
	for _, root := range []Root{left, right} {
		job, startErr := coordinator.StartScan(ctx, root.ID)
		if startErr != nil {
			t.Fatal(startErr)
		}
		if _, waitErr := coordinator.Wait(ctx, job.ID); waitErr != nil {
			t.Fatal(waitErr)
		}
	}
	snapshot := coordinator.Snapshot()

	// Then
	if snapshot.Revision != 2 || len(snapshot.Tracks) != 2 || !snapshot.Tracks["left"].Available || !snapshot.Tracks["right"].Available {
		t.Fatalf("aggregate snapshot = %+v", snapshot)
	}
}
