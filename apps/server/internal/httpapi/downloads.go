package httpapi

import (
	"net/http"
	"strconv"

	"github.com/jastreamer/jastreamer-server/internal/downloads"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func (service *server) registerDownloadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/downloads/capabilities", service.require(service.downloadCapabilities))
	mux.HandleFunc("POST /api/v1/downloads", service.require(service.mutating(service.createDownload)))
	mux.HandleFunc("GET /api/v1/downloads/{id}", service.require(service.downloadManifest))
	mux.HandleFunc("DELETE /api/v1/downloads/{id}", service.require(service.mutating(service.cancelDownload)))
	mux.HandleFunc("GET /api/v1/downloads/{id}/files/{index}", service.require(service.downloadFile))
	mux.HandleFunc("HEAD /api/v1/downloads/{id}/files/{index}", service.require(service.downloadFile))
}

func (service *server) downloadOwner(r *http.Request) (string, error) {
	user, err := service.options.Auth.Validate(r.Context(), sessionID(r))
	if err != nil {
		return "", err
	}
	return user.ID, nil
}

func (service *server) downloadCapabilities(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusOK, service.options.Downloads.Capabilities())
}

func (service *server) createDownload(w http.ResponseWriter, r *http.Request) {
	owner, err := service.downloadOwner(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var request downloads.Request
	if !decode(w, r, &request) {
		return
	}
	manifest, err := service.options.Downloads.Create(r.Context(), owner, request)
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusAccepted, manifest)
}

func (service *server) downloadManifest(w http.ResponseWriter, r *http.Request) {
	owner, err := service.downloadOwner(r)
	if err != nil {
		writeError(w, err)
		return
	}
	manifest, err := service.options.Downloads.Manifest(r.Context(), owner, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, manifest)
}

func (service *server) cancelDownload(w http.ResponseWriter, r *http.Request) {
	owner, err := service.downloadOwner(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := service.options.Downloads.Cancel(r.Context(), owner, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusNoContent, nil)
}

func (service *server) downloadFile(w http.ResponseWriter, r *http.Request) {
	owner, err := service.downloadOwner(r)
	if err != nil {
		writeError(w, err)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		writeError(w, fault.New(http.StatusNotFound, "DOWNLOAD_FILE_NOT_FOUND", "The download file was not found."))
		return
	}
	artifact, err := service.options.Downloads.Artifact(r.Context(), owner, r.PathValue("id"), index)
	if err != nil {
		writeError(w, err)
		return
	}
	defer artifact.File.Close()
	info, err := artifact.File.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Size {
		writeError(w, fault.New(http.StatusGone, "DOWNLOAD_FILE_MISSING", "The prepared download file is missing or changed."))
		return
	}
	w.Header().Set("Content-Type", artifact.Mime)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", strconv.Quote(artifact.SHA256))
	http.ServeContent(w, r, info.Name(), artifact.ModTime, artifact.File)
}
