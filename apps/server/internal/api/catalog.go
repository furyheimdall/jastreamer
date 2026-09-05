package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

type CatalogHandlers struct {
	snapshot    func(context.Context) catalog.Snapshot
	coordinator *catalog.Coordinator
	settings    *settings.Store
	mu          sync.Mutex
	revision    uint64
	browser     *catalog.Browser
}

func NewCatalogHandlers(snapshot func(context.Context) catalog.Snapshot, coordinator *catalog.Coordinator) *CatalogHandlers {
	return &CatalogHandlers{snapshot: snapshot, coordinator: coordinator}
}

func (handlers *CatalogHandlers) Tracks(writer http.ResponseWriter, request *http.Request) {
	if handlers.snapshot == nil {
		invalid(writer, "INTERNAL", "catalog snapshot is unavailable", http.StatusInternalServerError)
		return
	}
	limit := 0
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			invalid(writer, "INVALID_CATALOG_LIMIT", "catalog limit must be an integer", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	query := request.URL.Query().Get("query")
	if query == "" {
		query = request.URL.Query().Get("q")
	}
	page, err := handlers.currentBrowser(request.Context()).Browse(catalog.BrowseRequest{
		Query: query, Limit: limit, Cursor: request.URL.Query().Get("cursor"),
	})
	if err != nil {
		handlers.writeBrowseError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handlers *CatalogHandlers) currentBrowser(ctx context.Context) *catalog.Browser {
	handlers.mu.Lock()
	defer handlers.mu.Unlock()
	if handlers.browser != nil && handlers.coordinator != nil && handlers.revision == handlers.coordinator.Revision() {
		return handlers.browser
	}
	snapshot := handlers.snapshot(ctx)
	if handlers.browser == nil || handlers.revision != snapshot.Revision {
		handlers.browser = catalog.NewBrowser(snapshot)
		handlers.revision = snapshot.Revision
	}
	return handlers.browser
}

func (handlers *CatalogHandlers) writeBrowseError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrCatalogRevisionChanged):
		invalid(writer, "CATALOG_REVISION_CHANGED", "catalog changed while browsing", http.StatusConflict)
	case errors.Is(err, catalog.ErrInvalidCursor):
		invalid(writer, "INVALID_CATALOG_CURSOR", "catalog cursor is invalid", http.StatusBadRequest)
	case errors.Is(err, catalog.ErrInvalidLimit):
		invalid(writer, "INVALID_CATALOG_LIMIT", "catalog limit must be between 1 and 500", http.StatusBadRequest)
	case errors.Is(err, catalog.ErrInvalidQuery):
		invalid(writer, "INVALID_CATALOG_QUERY", "catalog query exceeds 200 characters", http.StatusBadRequest)
	default:
		writeError(writer, err)
	}
}
