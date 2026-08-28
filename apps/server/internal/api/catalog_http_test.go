package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func TestCatalogTracksHandlerReturnsStableJSON_withoutPathDisclosure(t *testing.T) {
	// Given
	snapshot := catalog.EmptySnapshot()
	snapshot.Revision = 12
	snapshot.Tracks["a"] = catalog.Track{TrackID: "a", Available: true, RelativePath: "/secret/a.flac", Metadata: catalog.Metadata{Title: "Café"}}
	handler := NewCatalogHandlers(func(context.Context) catalog.Snapshot { return snapshot }, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/tracks?query=CAFE%CC%81", nil)
	recorder := httptest.NewRecorder()

	// When
	handler.Tracks(recorder, request)

	// Then
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "relative_path") {
		t.Fatalf("tracks response = %d %s", recorder.Code, recorder.Body.String())
	}
	var page catalog.BrowsePage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.CatalogRevision != 12 {
		t.Fatalf("decode page = %+v, %v", page, err)
	}
}

func TestCatalogTracksHandlerReturnsConflict_whenCursorRevisionIsStale(t *testing.T) {
	// Given
	snapshot := catalog.EmptySnapshot()
	snapshot.Revision = 1
	snapshot.Tracks["a"] = catalog.Track{TrackID: "a", Available: true}
	snapshot.Tracks["b"] = catalog.Track{TrackID: "b", Available: true}
	handler := NewCatalogHandlers(func(context.Context) catalog.Snapshot { return snapshot }, nil)
	firstRecorder := httptest.NewRecorder()
	handler.Tracks(firstRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/tracks?limit=1", nil))
	var first catalog.BrowsePage
	if err := json.Unmarshal(firstRecorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	snapshot.Revision = 2
	recorder := httptest.NewRecorder()

	// When
	handler.Tracks(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/tracks?limit=1&cursor="+first.NextCursor, nil))

	// Then
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusConflict || failure.Code != "CATALOG_REVISION_CHANGED" {
		t.Fatalf("stale response = %d %s", recorder.Code, recorder.Body.String())
	}
}
