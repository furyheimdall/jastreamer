package player

import (
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

const (
	StateStopped     = "stopped"
	StateStarting    = "starting"
	StatePlaying     = "playing"
	StatePaused      = "paused"
	StateUnavailable = "unavailable"
	StateError       = "error"

	EntryPending   = "pending"
	EntryPlaying   = "playing"
	EntryCompleted = "completed"
	EntryError     = "error"
)

type Command struct {
	Action     string `json:"action"`
	EntryID    string `json:"entry_id,omitempty"`
	PositionMS int64  `json:"position_ms,omitempty"`
}

type QueueMutation struct {
	Action   string   `json:"action"`
	TrackIDs []string `json:"track_ids,omitempty"`
	EntryID  string   `json:"entry_id,omitempty"`
	Index    int      `json:"index,omitempty"`
	Revision int64    `json:"revision"`
}

type Entry struct {
	ID      string        `json:"id"`
	TrackID string        `json:"track_id"`
	Track   library.Track `json:"track"`
	Status  string        `json:"status"`
}

type Queue struct {
	Revision int64   `json:"revision"`
	Entries  []Entry `json:"entries"`
}

type State struct {
	Revision       int64               `json:"revision"`
	State          string              `json:"state"`
	RendererID     string              `json:"renderer_id"`
	CurrentEntryID string              `json:"current_entry_id"`
	Track          *library.Track      `json:"track"`
	PositionMS     int64               `json:"position_ms"`
	DurationMS     int64               `json:"duration_ms"`
	ObservedAt     string              `json:"observed_at"`
	PendingCommand string              `json:"pending_command"`
	Error          string              `json:"error"`
	Capabilities   output.Capabilities `json:"capabilities"`
}
