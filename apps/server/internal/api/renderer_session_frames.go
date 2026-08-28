package api

import "encoding/json"

const (
	rendererSessionSubprotocol = "jastreamer.renderer.v3"
	rendererProtocolMajor      = 3
	maxRendererPendingResults  = 32
	maxRendererCapabilities    = 64
)

type rendererCapabilitiesFrame struct {
	Commands        []string `json:"commands"`
	MediaTypes      []string `json:"mediaTypes"`
	SupportsRange   bool     `json:"supportsRange"`
	MaxChannels     int      `json:"maxChannels"`
	MaxSampleRateHz int      `json:"maxSampleRateHz"`
}

type rendererHelloFrame struct {
	ProtocolMajor      int                       `json:"protocolMajor"`
	Type               string                    `json:"type"`
	RendererID         string                    `json:"rendererId"`
	SupportedMajors    []int                     `json:"supportedMajors"`
	Capabilities       rendererCapabilitiesFrame `json:"capabilities"`
	LastServerSequence int64                     `json:"lastServerSequence"`
	PendingResults     []rendererResultFrame     `json:"pendingResults"`
}

type rendererAckFrame struct {
	ProtocolMajor int             `json:"protocolMajor"`
	Type          string          `json:"type"`
	CommandID     string          `json:"commandId"`
	Sequence      int64           `json:"sequence"`
	Status        string          `json:"status"`
	Error         json.RawMessage `json:"error"`
}

type rendererResultFrame struct {
	ProtocolMajor int             `json:"protocolMajor"`
	Type          string          `json:"type"`
	CommandID     string          `json:"commandId"`
	ResultID      string          `json:"resultId"`
	Status        string          `json:"status"`
	ObservedState *string         `json:"observedState"`
	PositionMS    *int64          `json:"positionMs"`
	Error         json.RawMessage `json:"error"`
}

type rendererPlaybackEventFrame struct {
	ProtocolMajor int    `json:"protocolMajor"`
	Type          string `json:"type"`
	EventID       string `json:"eventId"`
	SessionEpoch  string `json:"sessionEpoch"`
	PlayID        string `json:"playId"`
	Kind          string `json:"kind"`
	PositionMS    *int64 `json:"positionMs"`
	ObservedAt    string `json:"observedAt"`
}

type rendererInbound interface{ rendererInboundMessage() }

type (
	rendererAckMessage           struct{ frame rendererAckFrame }
	rendererResultMessage        struct{ frame rendererResultFrame }
	rendererPlaybackEventMessage struct{ frame rendererPlaybackEventFrame }
)

func (rendererAckMessage) rendererInboundMessage()           {}
func (rendererResultMessage) rendererInboundMessage()        {}
func (rendererPlaybackEventMessage) rendererInboundMessage() {}

type rendererWelcomeFrame struct {
	ProtocolMajor int      `json:"protocolMajor"`
	Type          string   `json:"type"`
	SelectedMajor int      `json:"selectedMajor"`
	SessionEpoch  string   `json:"sessionEpoch"`
	NextSequence  int64    `json:"nextSequence"`
	Capabilities  []string `json:"capabilities"`
}

type rendererCommandFrame struct {
	ProtocolMajor int             `json:"protocolMajor"`
	Type          string          `json:"type"`
	CommandID     string          `json:"commandId"`
	Sequence      int64           `json:"sequence"`
	SessionEpoch  string          `json:"sessionEpoch"`
	ZoneID        string          `json:"zoneId"`
	PlayID        *string         `json:"playId"`
	Kind          string          `json:"kind"`
	Deadline      string          `json:"deadline"`
	PositionMS    *int64          `json:"positionMs,omitempty"`
	Media         json.RawMessage `json:"media,omitempty"`
}

type rendererResultAckFrame struct {
	ProtocolMajor int    `json:"protocolMajor"`
	Type          string `json:"type"`
	ResultID      string `json:"resultId"`
}

type rendererErrorFrame struct {
	ProtocolMajor int             `json:"protocolMajor"`
	Type          string          `json:"type"`
	CommandID     *string         `json:"commandId,omitempty"`
	Code          string          `json:"code"`
	Message       string          `json:"message"`
	Retryable     bool            `json:"retryable"`
	Details       json.RawMessage `json:"details"`
}

type rendererCommandPayload struct {
	ZoneID     string          `json:"zoneId"`
	SessionID  string          `json:"sessionId"`
	PlayID     string          `json:"playId"`
	TrackID    string          `json:"trackId"`
	Kind       string          `json:"kind"`
	PositionMS *int64          `json:"positionMs,omitempty"`
	Media      json.RawMessage `json:"media,omitempty"`
}

type rendererOutbound interface{ rendererOutboundMessage() }

func (rendererWelcomeFrame) rendererOutboundMessage()   {}
func (rendererCommandFrame) rendererOutboundMessage()   {}
func (rendererResultAckFrame) rendererOutboundMessage() {}
func (rendererErrorFrame) rendererOutboundMessage()     {}

type rendererProtocolError struct {
	code      string
	message   string
	retryable bool
}

func (err *rendererProtocolError) Error() string { return err.code + ": " + err.message }
