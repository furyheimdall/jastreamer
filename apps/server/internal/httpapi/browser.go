package httpapi

import (
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/browseroutput"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

const browserOwnerHeader = "X-Jastreamer-Browser-Token"

func (service *server) registerBrowserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/browser-output/registrations", service.require(service.mutating(service.registerBrowserOutput)))
	mux.HandleFunc("PUT /api/v1/browser-output/registrations/{id}", service.require(service.mutating(service.renameBrowserOutput)))
	mux.HandleFunc("PUT /api/v1/browser-output/registrations/{id}/lease", service.require(service.renewBrowserOutput))
	mux.HandleFunc("GET /api/v1/browser-output/registrations/{id}/commands", service.require(service.pollBrowserOutput))
	mux.HandleFunc("POST /api/v1/browser-output/registrations/{id}/reports", service.require(service.reportBrowserOutput))
	mux.HandleFunc("DELETE /api/v1/browser-output/registrations/{id}", service.require(service.disconnectBrowserOutput))
}

func (service *server) registerBrowserOutput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		ProtocolInfo []string `json:"protocol_info"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := service.options.Browser.Register(remoteIP(r).String(), body.Name, body.ProtocolInfo)
	if err != nil {
		service.writeBrowserOutputError(w, "register", "", err)
		return
	}
	reply(w, http.StatusCreated, result)
}

func (service *server) renameBrowserOutput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := service.options.Browser.Rename(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String(), body.Name)
	if err != nil {
		service.writeBrowserOutputError(w, "rename", r.PathValue("id"), err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (service *server) renewBrowserOutput(w http.ResponseWriter, r *http.Request) {
	result, err := service.options.Browser.Renew(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String())
	if err != nil {
		service.writeBrowserOutputError(w, "renew_lease", r.PathValue("id"), err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (service *server) pollBrowserOutput(w http.ResponseWriter, r *http.Request) {
	result, err := service.options.Browser.Poll(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String())
	if err != nil {
		service.writeBrowserOutputError(w, "poll_commands", r.PathValue("id"), err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (service *server) reportBrowserOutput(w http.ResponseWriter, r *http.Request) {
	var body browseroutput.Report
	if !decode(w, r, &body) {
		return
	}
	if err := service.options.Browser.Report(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String(), body); err != nil {
		service.writeBrowserOutputError(w, "report_command", r.PathValue("id"), err)
		return
	}
	reply(w, http.StatusNoContent, nil)
}

func (service *server) disconnectBrowserOutput(w http.ResponseWriter, r *http.Request) {
	if err := service.options.Browser.Disconnect(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String()); err != nil {
		service.writeBrowserOutputError(w, "disconnect", r.PathValue("id"), err)
		return
	}
	reply(w, http.StatusNoContent, nil)
}

func browserOwnerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get(browserOwnerHeader))
	if len(value) > 128 {
		return ""
	}
	return value
}

func (service *server) writeBrowserOutputError(w http.ResponseWriter, operation, id string, err error) {
	var status int
	var code, message string
	switch {
	case errors.Is(err, browseroutput.ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, "INVALID_BROWSER_OUTPUT", "The browser output request is invalid."
	case errors.Is(err, browseroutput.ErrUnauthorized):
		status, code, message = http.StatusForbidden, "BROWSER_OUTPUT_FORBIDDEN", "This browser does not own that output registration."
	case errors.Is(err, browseroutput.ErrNotFound):
		status, code, message = http.StatusNotFound, "BROWSER_OUTPUT_NOT_FOUND", "The browser output registration expired or is unavailable."
	case errors.Is(err, browseroutput.ErrCapacity):
		status, code, message = http.StatusTooManyRequests, "BROWSER_OUTPUT_LIMIT", "Too many browser outputs are currently registered."
	case errors.Is(err, browseroutput.ErrStaleReport):
		status, code, message = http.StatusConflict, "STALE_BROWSER_REPORT", "The browser report no longer belongs to the active media command."
	case errors.Is(err, browseroutput.ErrConflict):
		status, code, message = http.StatusConflict, "BROWSER_COMMAND_IN_PROGRESS", "The browser output already has an active command."
	default:
		service.logRequestRejection(operation, id, http.StatusInternalServerError, "INTERNAL_ERROR")
		writeError(w, err)
		return
	}
	service.logRequestRejection(operation, id, status, code)
	writeError(w, fault.New(status, code, message))
}

const (
	httpDiagnosticInterval   = 30 * time.Second
	maximumDiagnosticWindows = 128
)

type diagnosticLogWindow struct {
	last       time.Time
	suppressed uint64
}

type diagnosticLimiter struct {
	mu      sync.Mutex
	windows map[string]diagnosticLogWindow
}

func (limiter *diagnosticLimiter) allow(key string) (uint64, bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	if limiter.windows == nil {
		limiter.windows = make(map[string]diagnosticLogWindow)
	}
	if _, exists := limiter.windows[key]; !exists && len(limiter.windows) >= maximumDiagnosticWindows-1 {
		key = "_overflow"
	}
	window := limiter.windows[key]
	if window.last.IsZero() || now.Sub(window.last) >= httpDiagnosticInterval {
		suppressed := window.suppressed
		window.last = now
		window.suppressed = 0
		limiter.windows[key] = window
		return suppressed, true
	}
	window.suppressed++
	limiter.windows[key] = window
	return 0, false
}

func (service *server) logRequestRejection(operation, id string, status int, reason string) {
	suppressed, allowed := service.diagnostics.allow(operation + ":" + reason)
	if !allowed {
		return
	}
	log.Printf("diagnostic component=httpapi event=request_rejected operation=%q registration_id=%q status=%d reason=%q suppressed=%d", operation, safeBrowserOutputID(id), status, reason, suppressed)
}

func safeBrowserOutputID(id string) string {
	const prefix = "browser:"
	if len(id) != len(prefix)+24 || !strings.HasPrefix(id, prefix) {
		return ""
	}
	token := strings.TrimPrefix(id, prefix)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 18 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return ""
	}
	return id
}

func protectedOperation(r *http.Request) (string, string) {
	id := safeBrowserOutputID(r.PathValue("id"))
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/browser-output/registrations":
		return "register", ""
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/lease"):
		return "renew_lease", id
	case r.Method == http.MethodPut && id != "":
		return "rename", id
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/commands"):
		return "poll_commands", id
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reports"):
		return "report_command", id
	case r.Method == http.MethodDelete && id != "":
		return "disconnect", id
	default:
		return "protected_api", ""
	}
}
