package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
)

func (service *server) renderers(w http.ResponseWriter, r *http.Request) {
	items := service.options.Devices.Devices()
	if items == nil {
		items = []output.Device{}
	}
	reply(w, 200, map[string]any{"items": items})
}

func (service *server) refreshRenderers(w http.ResponseWriter, r *http.Request) {
	go func() { _ = service.options.Devices.Refresh(service.options.Context) }()
	reply(w, 202, map[string]string{"status": "searching"})
}

func (service *server) pairRenderer(w http.ResponseWriter, r *http.Request) {
	var request output.PairingRequest
	if !decode(w, r, &request) {
		return
	}
	status, err := service.options.Player.PairOutput(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, status)
}

func (service *server) playerState(w http.ResponseWriter, r *http.Request) {
	state, err := service.options.Player.Snapshot(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, state)
}

func (service *server) command(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action     string `json:"action"`
		EntryID    string `json:"entry_id"`
		PositionMS int64  `json:"position_ms"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := service.options.Player.Command(r.Context(), player.Command{Action: body.Action, EntryID: body.EntryID, PositionMS: body.PositionMS})
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 202, result)
}

func (service *server) output(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RendererID string `json:"renderer_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := service.options.Player.SelectOutput(r.Context(), body.RendererID)
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, result)
}

func (service *server) queue(w http.ResponseWriter, r *http.Request) {
	result, err := service.options.Player.Queue(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, result)
}

func (service *server) mutateQueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action   string   `json:"action"`
		TrackIDs []string `json:"track_ids"`
		EntryID  string   `json:"entry_id"`
		Index    int      `json:"index"`
		Revision *int64   `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Revision == nil {
		writeError(w, fault.New(400, "REVISION_REQUIRED", "재생 대기열 버전이 필요합니다."))
		return
	}
	result, err := service.options.Player.MutateQueue(r.Context(), player.QueueMutation{Action: body.Action, TrackIDs: body.TrackIDs, EntryID: body.EntryID, Index: body.Index, Revision: *body.Revision})
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, result)
}

func (service *server) live(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	messages, cancel := service.options.Events.Subscribe()
	defer cancel()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-messages:
			if !open {
				return
			}
			if _, err := service.options.Auth.Validate(r.Context(), sessionID(r)); err != nil {
				return
			}
			body, err := json.Marshal(map[string]string{"type": event.Type})
			if err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err = fmt.Fprintf(w, "id: %d\nevent: change\ndata: %s\n\n", event.ID, body); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Time{})
		case <-heartbeat.C:
			if _, err := service.options.Auth.Validate(r.Context(), sessionID(r)); err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}
}
