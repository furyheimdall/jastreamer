package httpapi

import (
	"errors"
	"net/http"
	"strings"

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
		writeBrowserOutputError(w, err)
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
		writeBrowserOutputError(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (service *server) renewBrowserOutput(w http.ResponseWriter, r *http.Request) {
	result, err := service.options.Browser.Renew(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String())
	if err != nil {
		writeBrowserOutputError(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func (service *server) pollBrowserOutput(w http.ResponseWriter, r *http.Request) {
	result, err := service.options.Browser.Poll(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String())
	if err != nil {
		writeBrowserOutputError(w, err)
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
		writeBrowserOutputError(w, err)
		return
	}
	reply(w, http.StatusNoContent, nil)
}

func (service *server) disconnectBrowserOutput(w http.ResponseWriter, r *http.Request) {
	if err := service.options.Browser.Disconnect(r.PathValue("id"), browserOwnerToken(r), remoteIP(r).String()); err != nil {
		writeBrowserOutputError(w, err)
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

func writeBrowserOutputError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, browseroutput.ErrInvalidRequest):
		writeError(w, fault.New(http.StatusBadRequest, "INVALID_BROWSER_OUTPUT", "The browser output request is invalid."))
	case errors.Is(err, browseroutput.ErrUnauthorized):
		writeError(w, fault.New(http.StatusForbidden, "BROWSER_OUTPUT_FORBIDDEN", "This browser does not own that output registration."))
	case errors.Is(err, browseroutput.ErrNotFound):
		writeError(w, fault.New(http.StatusNotFound, "BROWSER_OUTPUT_NOT_FOUND", "The browser output registration expired or is unavailable."))
	case errors.Is(err, browseroutput.ErrCapacity):
		writeError(w, fault.New(http.StatusTooManyRequests, "BROWSER_OUTPUT_LIMIT", "Too many browser outputs are currently registered."))
	case errors.Is(err, browseroutput.ErrStaleReport):
		writeError(w, fault.New(http.StatusConflict, "STALE_BROWSER_REPORT", "The browser report no longer belongs to the active media command."))
	case errors.Is(err, browseroutput.ErrConflict):
		writeError(w, fault.New(http.StatusConflict, "BROWSER_COMMAND_IN_PROGRESS", "The browser output already has an active command."))
	default:
		writeError(w, err)
	}
}
