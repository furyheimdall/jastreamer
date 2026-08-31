package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func (service *server) getPolicy(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	persisted, err := service.config.Queue.ContinuationPolicy(
		request.Context(), playback.ZoneID(request.PathValue("zoneID")),
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	value := policyFromPlayback(persisted)
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(value.Revision, 10)))
	writeJSON(writer, http.StatusOK, value)
}

func (service *server) patchPolicy(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	expected, ok := revisionHeader(writer, request)
	if !ok {
		return
	}
	var body struct {
		Mode            *string `json:"mode"`
		ArtistGap       *int    `json:"artist_gap"`
		AlbumGap        *int    `json:"album_gap"`
		SessionOverride *string `json:"session_override"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	zoneID := playback.ZoneID(request.PathValue("zoneID"))
	current, err := service.config.Queue.ContinuationPolicy(request.Context(), zoneID)
	if err != nil {
		writeError(writer, err)
		return
	}
	if current.Revision != expected {
		invalid(writer, "STALE_POLICY_REVISION", "continuation policy revision is stale", http.StatusPreconditionFailed)
		return
	}
	update := playback.PolicyUpdate{
		ZoneID: zoneID, ExpectedRevision: expected, Mode: current.Mode,
		SessionOverride: current.SessionOverride, ArtistGap: current.ArtistGap, AlbumGap: current.AlbumGap,
	}
	if body.Mode != nil {
		mode, err := decision.ParsePolicy(*body.Mode)
		if err != nil {
			invalid(writer, "INVALID_REQUEST", "mode must be stop, album, or similar", http.StatusBadRequest)
			return
		}
		update.Mode = mode
	}
	if body.ArtistGap != nil {
		update.ArtistGap = *body.ArtistGap
	}
	if body.AlbumGap != nil {
		update.AlbumGap = *body.AlbumGap
	}
	if body.SessionOverride != nil {
		override, valid := parseSessionOverride(*body.SessionOverride)
		if !valid {
			invalid(writer, "INVALID_REQUEST", "session_override must be empty, stop, album, or similar", http.StatusBadRequest)
			return
		}
		update.SessionOverride = override
	}
	persisted, err := service.config.Queue.UpdateContinuationPolicy(request.Context(), update)
	if errors.Is(err, playback.ErrRevisionConflict) {
		invalid(writer, "STALE_POLICY_REVISION", "continuation policy revision is stale", http.StatusPreconditionFailed)
		return
	}
	if errors.Is(err, playback.ErrInvalidPolicy) {
		invalid(writer, "INVALID_REQUEST", "artist_gap and album_gap must be between 0 and 100", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeError(writer, err)
		return
	}
	value := policyFromPlayback(persisted)
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(value.Revision, 10)))
	writeJSON(writer, http.StatusOK, value)
	service.publishZoneState("continuation-policy", string(zoneID), value.Revision)
}

func policyFromPlayback(value playback.ContinuationPolicy) policy {
	return policy{
		Mode: string(value.Mode), ArtistGap: value.ArtistGap, AlbumGap: value.AlbumGap,
		Revision: value.Revision, SessionOverride: string(value.SessionOverride),
	}
}

func parseSessionOverride(raw string) (decision.Policy, bool) {
	if raw == "" {
		return "", true
	}
	mode, err := decision.ParsePolicy(raw)
	return mode, err == nil
}
