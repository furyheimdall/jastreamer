package httpapi

import (
	"net/http"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func (s *server) registerHistoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/history", s.require(s.history))
	mux.HandleFunc("GET /api/v1/library/verification", s.require(s.verificationStatus))
}

func (s *server) history(w http.ResponseWriter, r *http.Request) {
	if s.options.History == nil {
		writeError(w, fault.New(http.StatusServiceUnavailable, "HISTORY_UNAVAILABLE", "Diagnostic history is unavailable."))
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "renderer" && kind != "integrity" {
		writeError(w, fault.New(http.StatusBadRequest, "INVALID_QUERY", "The history type is invalid."))
		return
	}
	rendererID := r.URL.Query().Get("renderer_id")
	if len(rendererID) > 256 || strings.ContainsRune(rendererID, '\x00') {
		writeError(w, fault.New(http.StatusBadRequest, "INVALID_QUERY", "The renderer filter is invalid."))
		return
	}
	offset, err := integer(r, "offset", 0, 0, 1_000_000_000)
	if err != nil {
		writeError(w, err)
		return
	}
	limit, err := integer(r, "limit", 50, 1, 100)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.options.History.List(r.Context(), errorhistory.ListOptions{Kind: kind, RendererID: rendererID, Offset: offset, Limit: limit})
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (s *server) verificationStatus(w http.ResponseWriter, r *http.Request) {
	if s.options.Library == nil {
		writeError(w, fault.New(http.StatusServiceUnavailable, "VERIFICATION_UNAVAILABLE", "Library verification is unavailable."))
		return
	}
	status, err := s.options.Library.VerificationStatus(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, status)
}
