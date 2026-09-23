package httpapi

import (
	"encoding/csv"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func (s *server) registerHistoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/history", s.require(s.history))
	mux.HandleFunc("GET /api/v1/history/export", s.require(s.historyExport))
	mux.HandleFunc("GET /api/v1/library/verification", s.require(s.verificationStatus))
}

func (s *server) history(w http.ResponseWriter, r *http.Request) {
	if s.options.History == nil {
		writeError(w, fault.New(http.StatusServiceUnavailable, "HISTORY_UNAVAILABLE", "Diagnostic history is unavailable."))
		return
	}
	filter, err := historyFilter(r)
	if err != nil {
		writeError(w, err)
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
	result, err := s.options.History.List(r.Context(), errorhistory.ListOptions{Kind: filter.Kind, RendererID: filter.RendererID, Offset: offset, Limit: limit})
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (s *server) historyExport(w http.ResponseWriter, r *http.Request) {
	if s.options.History == nil {
		writeError(w, fault.New(http.StatusServiceUnavailable, "HISTORY_UNAVAILABLE", "Diagnostic history is unavailable."))
		return
	}
	filter, err := historyFilter(r)
	if err != nil {
		writeError(w, err)
		return
	}
	file, err := os.CreateTemp("", "jastreamer-history-*.csv")
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if _, err = file.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		writeError(w, err)
		return
	}
	csvWriter := csv.NewWriter(file)
	if err = csvWriter.Write(historyCSVHeader); err != nil {
		writeError(w, err)
		return
	}
	err = s.options.History.Stream(r.Context(), filter, func(event errorhistory.Event) error {
		return csvWriter.Write(historyCSVRecord(event))
	})
	if err != nil {
		writeError(w, err)
		return
	}
	csvWriter.Flush()
	if err = csvWriter.Error(); err != nil {
		writeError(w, err)
		return
	}
	if err = file.Sync(); err != nil {
		writeError(w, err)
		return
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="jastreamer-diagnostic-history.csv"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func historyFilter(r *http.Request) (errorhistory.Filter, error) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "renderer" && kind != "integrity" {
		return errorhistory.Filter{}, fault.New(http.StatusBadRequest, "INVALID_QUERY", "The history type is invalid.")
	}
	rendererID := r.URL.Query().Get("renderer_id")
	if len(rendererID) > 256 || !utf8.ValidString(rendererID) || strings.ContainsRune(rendererID, '\x00') {
		return errorhistory.Filter{}, fault.New(http.StatusBadRequest, "INVALID_QUERY", "The renderer filter is invalid.")
	}
	return errorhistory.Filter{Kind: kind, RendererID: rendererID}, nil
}

var historyCSVHeader = []string{
	"event_id",
	"timestamp",
	"type",
	"outcome",
	"track_title",
	"track_id",
	"folder",
	"relative_path",
	"stage",
	"code",
	"message",
	"renderer_name",
	"renderer_id",
	"renderer_protocol",
	"position_ms",
	"play_id",
	"command_id",
	"details_json",
}

func historyCSVRecord(event errorhistory.Event) []string {
	position := ""
	if event.PositionMS != nil {
		position = strconv.FormatInt(*event.PositionMS, 10)
	}
	values := []string{
		strconv.FormatInt(event.ID, 10),
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		event.Kind,
		event.Outcome,
		event.TrackTitle,
		event.TrackID,
		event.RootName,
		event.RelativePath,
		event.Stage,
		event.Code,
		event.Message,
		event.RendererName,
		event.RendererID,
		event.Protocol,
		position,
		event.PlayID,
		event.CommandID,
		string(event.Details),
	}
	for index := range values {
		values[index] = neutralizeCSVFormula(values[index])
	}
	return values
}

func neutralizeCSVFormula(value string) string {
	first := true
	for _, current := range value {
		if first {
			first = false
			if unicode.IsControl(current) || unicode.In(current, unicode.Cf) {
				return "'" + value
			}
		}
		if unicode.IsSpace(current) || unicode.IsControl(current) || unicode.In(current, unicode.Cf) {
			continue
		}
		if current == '=' || current == '+' || current == '-' || current == '@' {
			return "'" + value
		}
		break
	}
	return value
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
