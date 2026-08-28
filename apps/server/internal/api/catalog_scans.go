package api

import (
	"errors"
	"net/http"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

type addCatalogRootRequest struct {
	Path        string `json:"path"`
	DisplayName string `json:"display_name"`
}

type startCatalogScanRequest struct {
	RootID catalog.RootID `json:"root_id"`
}

func (handlers *CatalogHandlers) Roots(writer http.ResponseWriter, _ *http.Request) {
	if handlers.coordinator == nil {
		invalid(writer, "INTERNAL", "catalog coordinator is unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, struct {
		Items []catalog.Root `json:"items"`
	}{Items: handlers.coordinator.Roots()})
}

func (handlers *CatalogHandlers) AddRoot(writer http.ResponseWriter, request *http.Request) {
	if handlers.coordinator == nil {
		invalid(writer, "INTERNAL", "catalog coordinator is unavailable", http.StatusInternalServerError)
		return
	}
	var input addCatalogRootRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	root, err := handlers.coordinator.PrepareRoot(input.Path, input.DisplayName)
	if err != nil {
		handlers.writeCoordinatorError(writer, err)
		return
	}
	if handlers.settings == nil {
		root, err = handlers.coordinator.AddRoot(request.Context(), input.Path, input.DisplayName)
		if err != nil {
			handlers.writeCoordinatorError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, root)
		return
	}
	snapshot := handlers.settings.Snapshot()
	alreadyDesired := false
	for _, existing := range snapshot.Settings.CatalogRoots {
		if existing.Path == root.CanonicalPath {
			root.ID = catalog.RootID(existing.ID)
			root.DisplayName = existing.DisplayName
			alreadyDesired = true
			break
		}
	}
	if !alreadyDesired {
		roots := append(snapshot.Settings.CatalogRoots, settings.CatalogRoot{
			ID: string(root.ID), DisplayName: root.DisplayName, Path: root.CanonicalPath,
		})
		if _, err := handlers.settings.Patch(request.Context(), settings.Mutation{
			ExpectedRevision: snapshot.Revision, Update: settings.Update{CatalogRoots: &roots},
		}); err != nil {
			writeConfigError(writer, err)
			return
		}
	}
	if err := handlers.coordinator.ReconcileRoots(request.Context(), desiredCatalogRoots(handlers.settings.Snapshot().Settings.CatalogRoots)); err != nil {
		handlers.writeCoordinatorError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, root)
}

func (handlers *CatalogHandlers) StartScan(writer http.ResponseWriter, request *http.Request) {
	if handlers.coordinator == nil {
		invalid(writer, "INTERNAL", "catalog coordinator is unavailable", http.StatusInternalServerError)
		return
	}
	var input startCatalogScanRequest
	if !decodeJSON(writer, request, &input) {
		return
	}
	if input.RootID == "" {
		roots := handlers.coordinator.Roots()
		if len(roots) != 1 {
			invalid(writer, "INVALID_SCAN_REQUEST", "root_id is required when multiple catalog roots exist", http.StatusBadRequest)
			return
		}
		input.RootID = roots[0].ID
	}
	job, err := handlers.coordinator.StartScan(request.Context(), input.RootID)
	if err != nil {
		handlers.writeCoordinatorError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, job)
}

func (handlers *CatalogHandlers) ScanStatus(writer http.ResponseWriter, request *http.Request) {
	if handlers.coordinator == nil {
		invalid(writer, "INTERNAL", "catalog coordinator is unavailable", http.StatusInternalServerError)
		return
	}
	job, err := handlers.coordinator.Job(catalog.ScanJobID(request.PathValue("jobID")))
	if err != nil {
		handlers.writeCoordinatorError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func (handlers *CatalogHandlers) CancelScan(writer http.ResponseWriter, request *http.Request) {
	if handlers.coordinator == nil {
		invalid(writer, "INTERNAL", "catalog coordinator is unavailable", http.StatusInternalServerError)
		return
	}
	if err := handlers.coordinator.CancelScan(request.Context(), catalog.ScanJobID(request.PathValue("jobID"))); err != nil {
		handlers.writeCoordinatorError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func desiredCatalogRoots(roots []settings.CatalogRoot) []catalog.DesiredRoot {
	desired := make([]catalog.DesiredRoot, len(roots))
	for index, root := range roots {
		desired[index] = catalog.DesiredRoot{ID: catalog.RootID(root.ID), DisplayName: root.DisplayName, Path: root.Path}
	}
	return desired
}

func (handlers *CatalogHandlers) writeCoordinatorError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrDuplicateRoot):
		invalid(writer, "CATALOG_ROOT_DUPLICATE", "catalog root already exists", http.StatusConflict)
	case errors.Is(err, catalog.ErrTooManyRoots):
		invalid(writer, "CATALOG_ROOT_LIMIT", "catalog root limit reached", http.StatusConflict)
	case errors.Is(err, catalog.ErrRootOutsideAllowedBase), errors.Is(err, catalog.ErrUnreadableRoot):
		invalid(writer, "CATALOG_ROOT_INVALID", "catalog root is unavailable", http.StatusBadRequest)
	case errors.Is(err, catalog.ErrRootNotFound), errors.Is(err, catalog.ErrScanNotFound):
		invalid(writer, "NOT_FOUND", "catalog resource was not found", http.StatusNotFound)
	case errors.Is(err, catalog.ErrScanInProgress):
		invalid(writer, "CATALOG_SCAN_IN_PROGRESS", "a catalog scan is already active", http.StatusConflict)
	case errors.Is(err, catalog.ErrScanFinished):
		invalid(writer, "CATALOG_SCAN_FINISHED", "catalog scan already finished", http.StatusConflict)
	default:
		writeError(writer, err)
	}
}
