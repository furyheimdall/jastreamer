package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/ranking"
)

func (service *server) discovery(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	major := 2
	if raw := request.Header.Get("X-Jake-Protocol-Major"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			invalid(writer, "INVALID_REQUEST", "protocol major must be an integer", http.StatusBadRequest)
			return
		}
		major = parsed
	}
	if major < 1 || major > 2 {
		invalid(writer, "UNSUPPORTED_PROTOCOL_MAJOR", "supported protocol majors are 1 and 2", http.StatusUpgradeRequired)
		return
	}
	snapshot := service.catalogSnapshot(request.Context())
	writeJSON(writer, http.StatusOK, map[string]any{
		"protocol_major": major, "supported_protocol_majors": []int{1, 2},
		"capabilities": []string{"catalog-status", "queue", "continuation-policy", "automatic-preview", "decision-explanation", "wss-state"},
		"pairing_url":  "/pair/", "certificate_sha256": service.config.CertificateFingerprint,
		"contract_revision": contractRevision, "algorithm_revision": ranking.AlgorithmVersion,
		"analysis_revision": analysis.CurrentSchemaVersion, "catalog_revision": snapshot.Revision,
	})
}

func (service *server) catalogStatus(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	snapshot := service.catalogSnapshot(request.Context())
	total, complete, queued, failed := 0, 0, 0, 0
	for _, track := range snapshot.Tracks {
		if !track.Available {
			continue
		}
		total++
		switch track.AnalysisStatus {
		case catalog.AnalysisComplete:
			complete++
		case catalog.AnalysisQueued:
			queued++
		case catalog.AnalysisFailed:
			failed++
		}
	}
	coverage := 0
	if total > 0 {
		coverage = complete * 100 / total
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"scan_status": "ready", "catalog_revision": snapshot.Revision,
		"track_count": total, "analysis_complete": complete, "analysis_queued": queued,
		"analysis_failed": failed, "analysis_coverage": coverage,
		"analysis_revision": analysis.CurrentSchemaVersion,
	})
}

func (service *server) scanCatalog(writer http.ResponseWriter, request *http.Request) {
	if !service.requireAdmin(writer, request) {
		return
	}
	if service.config.Scan == nil {
		invalid(writer, "INTERNAL", "catalog scanner is unavailable", http.StatusInternalServerError)
		return
	}
	previous := service.catalogSnapshot(request.Context())
	next, err := service.config.Scan(request.Context(), previous)
	if err != nil {
		writeError(writer, err)
		return
	}
	service.catalogMu.Lock()
	service.catalog = next
	service.catalogMu.Unlock()
	writeJSON(writer, http.StatusAccepted, map[string]any{"scan_status": "complete", "catalog_revision": next.Revision})
	service.publishState("catalog", next.Revision)
}

func (service *server) catalogSnapshot(ctx context.Context) catalog.Snapshot {
	service.catalogMu.RLock()
	cached := service.catalog
	service.catalogMu.RUnlock()
	if service.config.LoadCatalog != nil {
		loaded, err := service.config.LoadCatalog(ctx)
		if err == nil && loaded.Revision >= cached.Revision {
			return loaded
		}
	}
	return cached
}
