package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type queueTrack struct {
	TrackID   string `json:"track_id"`
	Available bool   `json:"available"`
}

func (service *server) enqueue(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		invalid(writer, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", http.StatusPreconditionRequired)
		return
	}
	expected, ok := revisionHeader(writer, request)
	if !ok {
		return
	}
	var body struct {
		Tracks []queueTrack `json:"tracks"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	if len(body.Tracks) == 0 {
		invalid(writer, "INVALID_REQUEST", "at least one track is required", http.StatusBadRequest)
		return
	}
	tracks := make([]playback.QueueTrack, len(body.Tracks))
	for index, track := range body.Tracks {
		if strings.TrimSpace(track.TrackID) == "" {
			invalid(writer, "INVALID_REQUEST", "track_id is required", http.StatusBadRequest)
			return
		}
		tracks[index] = playback.QueueTrack{ID: playback.TrackID(track.TrackID), Available: track.Available}
	}
	before, _ := service.config.Queue.Snapshot(request.Context(), playback.ZoneID(request.PathValue("zoneID")))
	result, err := service.config.Queue.Enqueue(request.Context(), playback.EnqueueRequest{
		ZoneID: playback.ZoneID(request.PathValue("zoneID")), IdempotencyKey: idempotencyKey,
		ExpectedRevision: playback.Revision(expected), Tracks: tracks,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	status := http.StatusCreated
	if int64(before.Revision) != expected {
		status = http.StatusOK
	}
	writeJSON(writer, status, map[string]any{"revision": result.Revision, "entry_ids": result.EntryIDs})
	service.publishState("queue", result.Revision)
}

func (service *server) playbackState(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	snapshot, err := service.config.Queue.Snapshot(request.Context(), playback.ZoneID(request.PathValue("zoneID")))
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		writeError(writer, err)
		return
	}
	entries := make([]map[string]any, 0, len(snapshot.Queue))
	for _, entry := range snapshot.Queue {
		entries = append(entries, map[string]any{
			"entry_id": entry.ID, "track_id": entry.TrackID, "state": entry.State, "position": entry.Position,
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"zone_id": request.PathValue("zoneID"), "revision": snapshot.Revision,
		"transport": snapshot.Transport, "queue": entries,
	})
}

func revisionHeader(writer http.ResponseWriter, request *http.Request) (int64, bool) {
	raw := strings.Trim(request.Header.Get("If-Match"), `" `)
	if raw == "" {
		invalid(writer, "REVISION_REQUIRED", "If-Match header is required", http.StatusPreconditionRequired)
		return 0, false
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 0 {
		invalid(writer, "INVALID_REQUEST", "If-Match must contain a non-negative revision", http.StatusBadRequest)
		return 0, false
	}
	return revision, true
}
