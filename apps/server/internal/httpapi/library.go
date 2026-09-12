package httpapi

import (
	"net/http"
	"strconv"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
)

func (service *server) browse(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		offset, err := integer(r, "offset", 0, 0, 10000000)
		if err != nil {
			writeError(w, err)
			return
		}
		limit, err := integer(r, "limit", 100, 1, 500)
		if err != nil {
			writeError(w, err)
			return
		}
		values := r.URL.Query()
		page, err := service.options.Library.Browse(r.Context(), library.Query{Kind: kind, Search: values.Get("q"), AlbumID: values.Get("album_id"), Artist: values.Get("artist"), Genre: values.Get("genre"), RootID: values.Get("root_id"), Path: values.Get("path"), Sort: values.Get("sort"), Offset: offset, Limit: limit})
		if err != nil {
			writeError(w, err)
			return
		}
		reply(w, 200, page)
	}
}

func (service *server) track(w http.ResponseWriter, r *http.Request) {
	track, err := service.options.Library.Track(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, track)
}

func (service *server) trackInfo(w http.ResponseWriter, r *http.Request) {
	info, err := service.options.Library.Info(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, info)
}

func (service *server) artwork(w http.ResponseWriter, r *http.Request) {
	file, kind, err := service.options.Library.Artwork(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", strconv.Quote(r.PathValue("id")))
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (service *server) scans(w http.ResponseWriter, r *http.Request) {
	items, err := service.options.Library.Scans(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if items == nil {
		items = []library.ScanJob{}
	}
	reply(w, 200, map[string]any{"items": items})
}

func (service *server) startScan(w http.ResponseWriter, r *http.Request) {
	job, err := service.options.Library.StartScan(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 202, job)
}

func (service *server) cancelScan(w http.ResponseWriter, r *http.Request) {
	if err := service.options.Library.CancelScan(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	reply(w, 204, nil)
}

func (service *server) playlists(w http.ResponseWriter, r *http.Request) {
	items, err := service.options.Library.Playlists(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if items == nil {
		items = []library.Playlist{}
	}
	reply(w, 200, map[string]any{"items": items})
}

func (service *server) playlist(w http.ResponseWriter, r *http.Request) {
	result, err := service.options.Library.Playlist(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, result)
}

func (service *server) savePlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string   `json:"name"`
		Tracks   []string `json:"track_ids"`
		Revision *int64   `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	if id != "" && body.Revision == nil {
		writeError(w, fault.New(400, "REVISION_REQUIRED", "플레이리스트 버전이 필요합니다."))
		return
	}
	var revision int64
	if body.Revision != nil {
		revision = *body.Revision
	}
	result, err := service.options.Library.SavePlaylist(r.Context(), id, body.Name, body.Tracks, revision)
	if err != nil {
		writeError(w, err)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	reply(w, status, result)
}

func (service *server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		writeError(w, fault.New(400, "REVISION_REQUIRED", "올바른 플레이리스트 버전이 필요합니다."))
		return
	}
	if err = service.options.Library.DeletePlaylist(r.Context(), r.PathValue("id"), revision); err != nil {
		writeError(w, err)
		return
	}
	reply(w, 204, nil)
}
