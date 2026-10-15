package raat

import (
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

// pcmSignalPath describes only the Server's decode and PCM delivery. Receiver
// volume, DSP, mixer and DAC processing are outside this evidence.
func pcmSignalPath(audio library.AudioProperties, format audioFormat) output.SignalPath {
	switch strings.ToLower(strings.TrimSpace(audio.Codec)) {
	case "pcm", "flac", "alac", "ieee float":
	default:
		return output.SignalPath{}
	}
	if _, err := selectAudioFormat(audio, []audioFormat{format}); err != nil {
		return output.SignalPath{}
	}
	return output.SignalPath{
		Scope: "server", Mode: "pcm", SampleRate: *audio.SampleRate,
		ValidBits: *audio.BitsPerSample, ContainerBits: format.bits, Channels: *audio.Channels,
	}
}

// SignalPath reads the current owned stream; preparing media or advertising a
// supported format is not evidence of successful PCM delivery.
func (m *Manager) SignalPath(rendererID, playID string) (output.SignalPath, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[rendererID]
	if m.closed || s == nil || playID == "" {
		return output.SignalPath{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prepared, exists := m.prepared[s.resource.URL]
	if !exists || prepared.deviceID != rendererID || prepared.resource.PlayID != playID ||
		m.selected[rendererID].resource.URL != s.resource.URL {
		return output.SignalPath{}, false
	}
	if !s.active.Load() || !s.owned.Load() || s.ctx.Err() != nil || s.failed || !s.known ||
		s.resource.PlayID != playID || s.resource.TrackID == "" || s.deviceID != rendererID ||
		s.signalPath.Scope != "server" || (s.snapshot.state != 2 && s.snapshot.state != 3) ||
		s.snapshot.written <= 0 || s.snapshot.dropped != 0 || s.snapshot.overrun != 0 {
		return output.SignalPath{}, false
	}
	path := s.signalPath
	path.RendererID, path.TrackID = rendererID, s.resource.TrackID
	return path, true
}
