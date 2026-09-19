package httpapi

import (
	"net/http"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func (service *server) registerLikesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/v1/library/tracks/{id}/like", service.require(service.mutating(service.setTrackLike)))
	mux.HandleFunc("POST /api/v1/playlists/from-likes", service.require(service.mutating(service.savePlaylistFromLikes)))
}

func (service *server) setTrackLike(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Liked *bool `json:"liked"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Liked == nil {
		writeError(w, fault.New(http.StatusBadRequest, "INVALID_REQUEST", "좋아요 상태가 필요합니다."))
		return
	}
	track, err := service.options.Library.SetLiked(r.Context(), r.PathValue("id"), *body.Liked)
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, track)
}

func (service *server) savePlaylistFromLikes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	playlist, err := service.options.Library.PlaylistFromLikes(r.Context(), body.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusCreated, playlist)
}
