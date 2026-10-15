package raat

import (
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

// receiverScript is independently authored; no vendor Lua modules are loaded.
//
//go:embed receiver.lua
var receiverScript string

var errTransportOwnership = errors.New("raat: transport controls are owned by another controller")

const (
	opInfo           byte   = 1
	opSetup          byte   = 2
	opPCM            byte   = 3
	opStart          byte   = 4
	opPause          byte   = 5
	opStop           byte   = 6
	opEOF            byte   = 7
	opStatus         byte   = 8
	opArtwork        byte   = 9
	opEvents         byte   = 10
	opClaim          byte   = 12
	featureOutput    uint32 = 1
	featureTransport uint32 = 2
	featureStatus    uint32 = 4
	featureArtwork   uint32 = 8
	maxPCMBytes             = 64 << 10
	maxArtworkBytes         = 3 << 20
)

type receiverSnapshot struct {
	state                                                                                                byte
	position, written, samplesOut, fill, capacity, dropped, overrun, delay, deviceTime, startTime, epoch int64
}

func parseSnapshot(data []byte) (receiverSnapshot, error) {
	if len(data) != 89 || data[0] < 1 || data[0] > 6 {
		return receiverSnapshot{}, errors.New("raat: malformed receiver snapshot")
	}
	s := receiverSnapshot{state: data[0]}
	fields := []*int64{&s.position, &s.written, &s.samplesOut, &s.fill, &s.capacity, &s.dropped, &s.overrun, &s.delay, &s.deviceTime, &s.startTime, &s.epoch}
	for i, field := range fields {
		*field = int64(binary.BigEndian.Uint64(data[1+i*8:]))
	}
	if s.position < 0 || s.written < 0 || s.samplesOut < 0 || s.fill < 0 || s.capacity < 0 || s.fill > s.capacity || s.dropped < 0 || s.overrun < 0 || s.delay < 0 || s.epoch < 0 {
		return receiverSnapshot{}, errors.New("raat: invalid receiver counters")
	}
	return s, nil
}

func receiverCall(ctx context.Context, client *controlClient, opcode byte, data []byte) (byte, []byte, error) {
	payload := make([]byte, 1+len(data))
	payload[0] = opcode
	copy(payload[1:], data)
	return receiverRequest(ctx, client, payload)
}

func receiverRequest(ctx context.Context, client *controlClient, payload []byte) (byte, []byte, error) {
	reply, err := client.call(ctx, luaRequest, payload)
	if err != nil {
		return 0, nil, err
	}
	if len(reply) == 0 {
		return 0, nil, errors.New("raat: empty receiver response")
	}
	switch reply[0] {
	case 0:
		return 0, reply[1:], nil
	case 1:
		return 0, nil, errors.New("raat: receiver rejected operation")
	case 7:
		if payload[0] != opStatus && payload[0] != opArtwork {
			return 0, nil, errors.New("raat: unexpected ownership response")
		}
		return 0, nil, errTransportOwnership
	default:
		return 0, nil, errors.New("raat: malformed receiver response")
	}
}

func receiverFormats(data []byte) (uint32, []audioFormat, error) {
	if len(data) < 8 {
		return 0, nil, errors.New("raat: malformed receiver capabilities")
	}
	features, count := binary.BigEndian.Uint32(data), binary.BigEndian.Uint32(data[4:])
	if count > 4096 || len(data) != 8+int(count)*9 {
		return 0, nil, errors.New("raat: malformed receiver formats")
	}
	formats := make([]audioFormat, 0, count)
	for offset := 8; offset < len(data); offset += 9 {
		kind := ""
		switch data[offset] {
		case 1:
			kind = "pcm"
		case 2:
			kind = "pcm_float32"
		case 3:
			kind = "pcm_float64"
		}
		f := audioFormat{kind, int(binary.BigEndian.Uint32(data[offset+1:])), int(binary.BigEndian.Uint16(data[offset+5:])), int(binary.BigEndian.Uint16(data[offset+7:]))}
		if !f.valid() {
			return 0, nil, errors.New("raat: invalid receiver PCM format")
		}
		formats = append(formats, f)
	}
	if features&featureOutput == 0 || len(formats) == 0 {
		return 0, nil, errors.New("raat: receiver has no supported PCM output")
	}
	return features, formats, nil
}

func setupPayload(format audioFormat, capacity, ready int, token []byte) []byte {
	data := make([]byte, 17+audioTokenBytes)
	switch format.sampleType {
	case "pcm":
		data[0] = 1
	case "pcm_float32":
		data[0] = 2
	case "pcm_float64":
		data[0] = 3
	}
	binary.BigEndian.PutUint32(data[1:], uint32(format.sampleRate))
	binary.BigEndian.PutUint16(data[5:], uint16(format.bits))
	binary.BigEndian.PutUint16(data[7:], uint16(format.channels))
	binary.BigEndian.PutUint32(data[9:], uint32(capacity))
	binary.BigEndian.PutUint32(data[13:], uint32(ready))
	copy(data[17:], token)
	return data
}

func signingRequired(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	if value == nil {
		return false
	}
	keys, ok := value.([]any)
	return !ok || len(keys) != 0
}

func parseControls(data []byte) ([]output.RemoteControl, error) {
	if len(data) > 16<<10 || !utf8.Valid(data) {
		return nil, errors.New("raat: invalid transport control")
	}
	var wire struct {
		Button string `json:"button"`
		Seek   *struct {
			Mode    string `json:"mode"`
			Seconds *int64 `json:"seconds"`
		} `json:"seek"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return nil, errors.New("raat: malformed transport control")
	}
	controls := make([]output.RemoteControl, 0, 2)
	switch wire.Button {
	case "play", "pause", "stop", "next", "previous", "playpause":
		controls = append(controls, output.RemoteControl{Action: wire.Button})
	}
	if wire.Seek != nil && (wire.Seek.Mode == "absolute" || wire.Seek.Mode == "relative") {
		if wire.Seek.Seconds == nil {
			return nil, errors.New("raat: transport seek has no position")
		}
		seconds := *wire.Seek.Seconds
		if seconds > math.MaxInt64/1000 || seconds < math.MinInt64/1000 {
			return nil, errors.New("raat: transport seek overflow")
		}
		if wire.Seek.Mode == "absolute" && seconds < 0 {
			return nil, errors.New("raat: negative transport position")
		}
		controls = append(controls, output.RemoteControl{Action: "seek", PositionMS: seconds * 1000, Relative: wire.Seek.Mode == "relative"})
	}
	return controls, nil
}

func nativeStatus(resource output.Resource, format audioFormat, state TransportState) ([]byte, error) {
	loop := "disabled"
	switch state.RepeatMode {
	case "all":
		loop = "loop"
	case "one":
		loop = "loopone"
	}
	transport := "stopped"
	switch state.State {
	case "PLAYING", "playing":
		transport = "playing"
	case "PAUSED_PLAYBACK", "paused":
		transport = "paused"
	case "TRANSITIONING", "loading", "starting":
		transport = "loading"
	}
	status := map[string]any{"loop": loop, "shuffle": state.Shuffle, "state": transport, "seek": state.PositionMS / 1000, "is_previous_allowed": resource.TrackID != "", "is_next_allowed": resource.TrackID != "", "is_pause_allowed": state.CanPause, "is_play_allowed": state.CanPlay, "is_seek_allowed": state.CanSeek}
	if resource.TrackID != "" {
		status["now_playing"] = map[string]any{"title": resource.Title, "artist": resource.Artist, "album": resource.Album, "composer": "", "channel": "", "length": resource.DurationMS / 1000, "one_line": resource.Title, "two_line_title": resource.Title, "two_line_subtitle": resource.Artist, "three_line_title": resource.Title, "three_line_subtitle": resource.Artist, "three_line_subsubtitle": resource.Album, "artwork_url": nil}
	}
	if format.valid() {
		status["stream_format"] = map[string]any{"sample_type": "pcm", "sample_rate": format.sampleRate, "bits_per_sample": format.bits, "channels": format.channels}
	}
	data, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("raat: encode transport status: %w", err)
	}
	return data, nil
}
