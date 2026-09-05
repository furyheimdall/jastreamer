package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func TestCatalogPagesReuseSnapshotUntilCoordinatorRevisionChanges(t *testing.T) {
	snapshot := catalog.EmptySnapshot()
	snapshot.Revision = 12
	snapshot.Tracks["a"] = catalog.Track{TrackID: "a", RootID: "root", Available: true}
	snapshot.Tracks["b"] = catalog.Track{TrackID: "b", RootID: "root", Available: true}
	coordinator, err := catalog.OpenCoordinator(t.Context(), catalog.CoordinatorConfig{
		StatePath: filepath.Join(t.TempDir(), "catalog.json"), InitialSnapshot: snapshot,
		AllowedBases: []string{t.TempDir()}, Now: func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := coordinator.Close(); err != nil {
			t.Error(err)
		}
	})
	loads := 0
	handler := NewCatalogHandlers(func(context.Context) catalog.Snapshot {
		loads++
		return coordinator.Snapshot()
	}, coordinator)
	request := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.Tracks(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}
	first := request("/api/v1/catalog/tracks?limit=1")
	var page catalog.BrowsePage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || first.Code != http.StatusOK || page.NextCursor == "" {
		t.Fatalf("first page = %d %s, %v", first.Code, first.Body.String(), err)
	}
	second := request("/api/v1/catalog/tracks?limit=1&cursor=" + page.NextCursor)
	if second.Code != http.StatusOK || loads != 1 {
		t.Fatalf("second page = %d, snapshot loads = %d; want 200 and 1", second.Code, loads)
	}
	if err := coordinator.ReconcileRoots(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	stale := request("/api/v1/catalog/tracks?limit=1&cursor=" + page.NextCursor)
	if stale.Code != http.StatusConflict || loads != 2 {
		t.Fatalf("stale page = %d, snapshot loads = %d; want 409 and 2", stale.Code, loads)
	}
	current := request("/api/v1/catalog/tracks")
	if err := json.Unmarshal(current.Body.Bytes(), &page); err != nil || current.Code != http.StatusOK || len(page.Items) != 0 || page.CatalogRevision != 13 || loads != 2 {
		t.Fatalf("updated page = %d %s, loads = %d, error = %v", current.Code, current.Body.String(), loads, err)
	}
}
