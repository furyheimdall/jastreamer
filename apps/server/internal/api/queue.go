package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type queueTrack struct {
	TrackID   string `json:"track_id"`
	Available bool   `json:"available"`
}

type queueMutationBody struct {
	Command       playback.QueueCommand `json:"command"`
	TrackIDs      []string              `json:"track_ids"`
	Tracks        []queueTrack          `json:"tracks"`
	EntryID       string                `json:"entry_id"`
	BeforeEntryID *string               `json:"before_entry_id"`
}

func (service *server) enqueue(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		invalid(writer, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", http.StatusPreconditionRequired)
		return
	}
	if idempotencyKey != strings.TrimSpace(idempotencyKey) || len(idempotencyKey) > 128 {
		invalid(writer, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be exact and at most 128 characters", http.StatusBadRequest)
		return
	}
	expected, ok := revisionHeader(writer, request)
	if !ok {
		return
	}
	var body queueMutationBody
	if !decodeJSON(writer, request, &body) {
		return
	}
	if body.Command == "" {
		body.Command = playback.QueueAppend
	}
	trackIDs := body.TrackIDs
	if len(trackIDs) == 0 && len(body.Tracks) > 0 {
		trackIDs = make([]string, len(body.Tracks))
		for index, track := range body.Tracks {
			trackIDs[index] = track.TrackID
		}
	}
	snapshot := service.catalogSnapshot(request.Context())
	tracks, valid := authoritativeQueueTracks(trackIDs, snapshot)
	if !valid {
		invalid(writer, "INVALID_REQUEST", "queue command fields are invalid", http.StatusBadRequest)
		return
	}
	beforeEntryID := playback.QueueEntryID("")
	if body.BeforeEntryID != nil {
		beforeEntryID = playback.QueueEntryID(*body.BeforeEntryID)
	}
	result, err := service.config.Queue.MutateQueue(request.Context(), playback.QueueMutationRequest{
		ZoneID: playback.ZoneID(request.PathValue("zoneID")), IdempotencyKey: idempotencyKey,
		ExpectedRevision: playback.Revision(expected), Command: body.Command, Tracks: tracks,
		EntryID: playback.QueueEntryID(body.EntryID), BeforeEntryID: beforeEntryID,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.Header().Set("ETag", `"`+revisionString(result.Revision)+`"`)
	status := http.StatusOK
	legacyAppend := len(body.Tracks) > 0 && len(body.TrackIDs) == 0
	if (body.Command == playback.QueueAppend || body.Command == playback.QueueInsert) && (!result.Replayed || !legacyAppend) {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"revision": result.Revision, "entry_ids": result.EntryIDs})
	if !result.Replayed {
		service.publishState("queue", result.Revision)
	}
}

func authoritativeQueueTracks(trackIDs []string, snapshot catalog.Snapshot) ([]playback.QueueTrack, bool) {
	tracks := make([]playback.QueueTrack, len(trackIDs))
	for index, id := range trackIDs {
		if strings.TrimSpace(id) == "" {
			return nil, false
		}
		track, found := snapshot.Tracks[catalog.TrackID(id)]
		tracks[index] = playback.QueueTrack{ID: playback.TrackID(id), Available: found && track.Available}
	}
	return tracks, true
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
		if entry.State == playback.QueueCompleted || entry.State == playback.QueueRemoved {
			continue
		}
		entries = append(entries, map[string]any{
			"entry_id": entry.ID, "track_id": entry.TrackID, "state": entry.State, "position": entry.Position,
		})
	}
	observedTransport, pendingCommandID := service.observedTransport(request, snapshot.ZoneID)
	writeJSON(writer, http.StatusOK, map[string]any{
		"zone_id": request.PathValue("zoneID"), "revision": snapshot.Revision,
		"transport": snapshot.Transport, "observed_transport": observedTransport,
		"pending_command_id": pendingCommandID, "queue": entries,
	})
}

func (service *server) observedTransport(request *http.Request, zoneID playback.ZoneID) (string, string) {
	observed := "unknown"
	renderer, err := service.config.Queue.AssignedRenderer(request.Context(), zoneID)
	if err == nil {
		truth, truthErr := service.config.Queue.RendererSessionTruth(request.Context(), renderer.ID)
		if truthErr == nil && truth.ObservedState != "" {
			observed = truth.ObservedState
		}
	}
	commands, err := service.config.Queue.PendingOutbox(request.Context(), zoneID)
	if err == nil && len(commands) > 0 {
		return observed, commands[0].ID
	}
	return observed, ""
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
