package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func TestNew_mounts_admin_config_route(t *testing.T) {
	// Given
	value := newFixture(t)
	directory := t.TempDir()
	media := filepath.Join(directory, "media")
	if err := os.Mkdir(media, 0o700); err != nil {
		t.Fatalf("create media: %v", err)
	}
	store, err := settings.Open(settings.Config{
		Path: filepath.Join(directory, "config", "settings.json"),
		Defaults: settings.Values{
			DisplayName: "Integrated Server", CatalogRoots: []settings.CatalogRoot{{ID: "media", DisplayName: "Media", Path: media}},
			PairingTTLSeconds: 300,
		},
		Locks: settings.Locks{
			ListenAddress: ":0", CertificateFingerprint: strings.Repeat("a", 64), DataDirectory: directory,
			AllowedCatalogBases: []string{media}, Environment: "test",
		},
	})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	handler := api.New(api.Config{Security: value.manager, Queue: value.store, Settings: store})

	// When
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/config", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+value.admin.Token)
	response := requestRecorder(handler, request)

	// Then
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"0"` {
		t.Fatalf("config route = %d ETag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
}

func TestNew_mounts_catalog_zone_and_event_routes_with_role_bound_authorization(t *testing.T) {
	// Given
	value := newFixture(t)
	base := t.TempDir()
	scanCount := 0
	coordinator, err := catalog.OpenCoordinator(t.Context(), catalog.CoordinatorConfig{
		StatePath: filepath.Join(t.TempDir(), "catalog.json"), AllowedBases: []string{base}, Now: time.Now,
		Scan: func(_ context.Context, root catalog.Root, _ catalog.Snapshot) (catalog.ScanResult, error) {
			scanCount++
			snapshot := catalog.EmptySnapshot()
			snapshot.Tracks["one"] = catalog.Track{RootID: root.ID, TrackID: "one", Available: true}
			if scanCount == 1 {
				snapshot.Tracks["two"] = catalog.Track{RootID: root.ID, TrackID: "two", Available: true}
			}
			return catalog.ScanResult{Snapshot: snapshot, Complete: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.AddRoot(t.Context(), base, "Music"); err != nil {
		t.Fatalf("add root: %v", err)
	}
	handler := api.New(api.Config{
		Security: value.manager, Queue: value.store, CatalogCoordinator: coordinator,
		CatalogSnapshot: func(context.Context) catalog.Snapshot { return coordinator.Snapshot() },
	})
	controller := pairController(t, value)

	firstScan := request(t, handler, http.MethodPost, "/api/v1/catalog/scans", value.admin.Token, `{}`, nil)
	var firstJob catalog.ScanJob
	if err := json.Unmarshal(firstScan.Body.Bytes(), &firstJob); err != nil {
		t.Fatalf("decode first scan job: %v", err)
	}
	if _, err := coordinator.Wait(t.Context(), firstJob.ID); err != nil {
		t.Fatalf("wait for first scan: %v", err)
	}
	firstPage := request(t, handler, http.MethodGet, "/api/v1/catalog/tracks?limit=1", controller.Token, "", nil)
	var page catalog.BrowsePage
	if err := json.Unmarshal(firstPage.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode first page: %v", err)
	}

	// When
	rootsForbidden := request(t, handler, http.MethodGet, "/api/v1/catalog/roots", controller.Token, "", nil)
	roots := request(t, handler, http.MethodGet, "/api/v1/catalog/roots", value.admin.Token, "", nil)
	created := request(t, handler, http.MethodPost, "/api/v1/zones", value.admin.Token, `{"zone_id":"living","name":"Living"}`, nil)
	zones := request(t, handler, http.MethodGet, "/api/v1/zones", controller.Token, "", nil)
	ticket := request(t, handler, http.MethodPost, "/api/v1/event-tickets", controller.Token, "", nil)
	scan := request(t, handler, http.MethodPost, "/api/v1/catalog/scans", value.admin.Token, `{}`, nil)
	var scanJob catalog.ScanJob
	if err := json.Unmarshal(scan.Body.Bytes(), &scanJob); err != nil {
		t.Fatalf("decode scan job: %v", err)
	}
	if _, err := coordinator.Wait(t.Context(), scanJob.ID); err != nil {
		t.Fatalf("wait for scan: %v", err)
	}
	scanStatus := request(t, handler, http.MethodGet, "/api/v1/catalog/scans/"+string(scanJob.ID), value.admin.Token, "", nil)
	staleCursor := request(t, handler, http.MethodGet, "/api/v1/catalog/tracks?limit=1&cursor="+page.NextCursor, controller.Token, "", nil)

	// Then
	if firstScan.Code != http.StatusAccepted || firstPage.Code != http.StatusOK || page.NextCursor == "" ||
		rootsForbidden.Code != http.StatusForbidden || roots.Code != http.StatusOK || created.Code != http.StatusCreated ||
		zones.Code != http.StatusOK || ticket.Code != http.StatusCreated || scan.Code != http.StatusAccepted ||
		scanStatus.Code != http.StatusOK || staleCursor.Code != http.StatusConflict {
		t.Fatalf("integrated statuses = first:%d page:%d forbidden:%d roots:%d create:%d zones:%d ticket:%d scan:%d status:%d stale:%d",
			firstScan.Code, firstPage.Code, rootsForbidden.Code, roots.Code, created.Code, zones.Code, ticket.Code,
			scan.Code, scanStatus.Code, staleCursor.Code)
	}
}

func requestRecorder(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
