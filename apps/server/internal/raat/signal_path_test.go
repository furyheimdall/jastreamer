package raat

import (
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

func TestPCMSignalPathRequiresLosslessSource(t *testing.T) {
	format := audioFormat{"pcm", 48000, 24, 2}
	for _, codec := range []string{"MP3", "AAC", "Opus", "Vorbis"} {
		audio := pcmTestProperties(codec, 48000, 2, 16)
		if _, err := selectAudioFormat(audio, []audioFormat{format}); err != nil {
			t.Fatalf("lossy source must remain playable: %v", err)
		}
		if path := pcmSignalPath(audio, format); path.Scope != "" {
			t.Errorf("lossy %s received bit-perfect evidence: %+v", codec, path)
		}
	}
	for _, audio := range []library.AudioProperties{
		pcmTestProperties("FLAC", 48000, 2, 32),
		pcmTestProperties("FLAC", 44100, 2, 16),
		pcmTestProperties("FLAC", 48000, 6, 16),
		pcmTestProperties("FLAC", 48000, 2, 0),
		pcmTestProperties("IEEE Float", 48000, 2, 32),
		{Codec: "FLAC"},
	} {
		if path := pcmSignalPath(audio, format); path.Scope != "" {
			t.Errorf("altered or unknown source received bit-perfect evidence: %+v", path)
		}
	}
	path := pcmSignalPath(pcmTestProperties("FLAC", 48000, 2, 16), format)
	if path.Scope != "server" || path.Mode != "pcm" || path.ValidBits != 16 || path.ContainerBits != 24 {
		t.Fatalf("zero padding must preserve source precision without claiming 24 source bits: %+v", path)
	}
}

func qualifiedSignalSession(t *testing.T, client *controlClient) (*Manager, *session) {
	t.Helper()
	m := testManager()
	s := fakeSession(t, client)
	s.manager = m
	s.known = true
	s.snapshot.written = 48000
	s.signalPath = pcmSignalPath(pcmTestProperties("FLAC", 48000, 2, 16), s.format)
	prepared := preparedMedia{deviceID: s.deviceID, resource: s.resource}
	m.prepared[s.resource.URL] = prepared
	m.selected[s.deviceID] = prepared
	m.sessions[s.deviceID] = s
	return m, s
}

func TestRAATSignalPathFollowsLiveStreamEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Manager, *session)
	}{
		{"before frames arrive", func(_ *Manager, s *session) { s.snapshot.written = 0 }},
		{"underrun", func(_ *Manager, s *session) { s.snapshot.dropped = 1 }},
		{"overrun", func(_ *Manager, s *session) { s.snapshot.overrun = 1 }},
		{"completed", func(_ *Manager, s *session) { s.snapshot.state = 4 }},
		{"failed", func(_ *Manager, s *session) { s.failed = true }},
		{"unconfirmed", func(_ *Manager, s *session) { s.known = false }},
		{"ownership lost", func(_ *Manager, s *session) { s.owned.Store(false) }},
		{"seek starting", func(_ *Manager, s *session) { s.active.Store(false) }},
		{"cancelled", func(_ *Manager, s *session) { s.cancel() }},
		{"revoked grant", func(m *Manager, s *session) { delete(m.prepared, s.resource.URL) }},
		{"replaced selection", func(m *Manager, s *session) { delete(m.selected, s.deviceID) }},
		{"closed manager", func(m *Manager, _ *session) { m.closed = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, s := qualifiedSignalSession(t, nil)
			if path, ok := m.SignalPath(s.deviceID, s.resource.PlayID); !ok || path.TrackID != "track" || path.RendererID != s.deviceID {
				t.Fatalf("live owned lossless stream not qualified: %+v, %v", path, ok)
			}
			test.change(m, s)
			if path, ok := m.SignalPath(s.deviceID, s.resource.PlayID); ok {
				t.Fatalf("invalidated stream retained evidence: %+v", path)
			}
		})
	}
}

func TestRAATSignalPathPauseAndRevoke(t *testing.T) {
	_, client := loopbackReceiver(t, func(fake *fakeReceiver, kind, id uint32, data []byte) {
		if kind != luaRequest || len(data) != 1 || data[0] != opStop {
			t.Errorf("unexpected receiver request: %d %x", kind, data)
		}
		fake.reply(luaResponse, id, true, append([]byte{0}, encodeTestSnapshot(receiverSnapshot{state: 5, epoch: 2})...))
	})
	m, s := qualifiedSignalSession(t, client)
	playID := s.resource.PlayID
	s.applySnapshot(receiverSnapshot{state: 3, written: 48000, epoch: 1})
	if _, ok := m.SignalPath(s.deviceID, playID); !ok {
		t.Fatal("Pause lost an unchanged owned PCM path")
	}
	if _, ok := m.SignalPath(s.deviceID, "previous-play"); ok {
		t.Fatal("previous play identity received current PCM evidence")
	}
	if _, ok := m.SignalPath("raat:other", playID); ok {
		t.Fatal("another receiver received current PCM evidence")
	}
	m.Revoke(playID)
	if path, ok := m.SignalPath(s.deviceID, playID); ok {
		t.Fatalf("revoked playback retained PCM evidence: %+v", path)
	}
}
