package raat

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestPauseInterruptsBlockedAudioWithoutCorruptingControl(t *testing.T) {
	local, remote := net.Pipe()
	client := controlClientFromConn(local)
	t.Cleanup(func() { client.Close(); remote.Close() })
	s := fakeSession(t, client)
	audio, audioRemote := net.Pipe()
	s.audio = audio
	t.Cleanup(func() { audio.Close(); audioRemote.Close() })
	pcm := bytes.Repeat([]byte{1, 2, 3, 4}, s.chunkBytes()/4)
	s.decoder = io.NopCloser(bytes.NewReader(pcm))
	var err error
	s.source, err = os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	headerRead := make(chan struct{})
	received := make(chan error, 1)
	go func() {
		var header [13]byte
		if _, err := io.ReadFull(audioRemote, header[:]); err != nil {
			received <- err
			return
		}
		close(headerRead)
		// Leave PCM blocked while the independent control stream serves Pause.
		fake := &fakeReceiver{conn: remote}
		kind, body, err := readControlFrame(remote)
		if err != nil {
			received <- err
			return
		}
		if kind != luaRequest || len(body) != 5 || body[4] != opPause {
			received <- errControlFrame
			return
		}
		snap := receiverSnapshot{state: 3, capacity: 96000, epoch: 1}
		fake.reply(luaResponse, binary.BigEndian.Uint32(body), true, append([]byte{0}, encodeTestSnapshot(snap)...))
		received <- nil
	}()
	s.startPump()
	select {
	case <-headerRead:
	case <-time.After(time.Second):
		t.Fatal("PCM did not start")
	}
	paused := make(chan error, 1)
	go func() { paused <- s.pause(context.Background()) }()
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pause did not finish")
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		t.Fatal("audio cancellation corrupted the control connection")
	default:
	}
	if s.observation().State != "PAUSED_PLAYBACK" || s.observation().MediaFailed {
		t.Fatalf("unexpected observation: %+v", s.observation())
	}
}

func TestDisplayOwnershipRaceDoesNotFailAudio(t *testing.T) {
	_, client := loopbackReceiver(t, func(fake *fakeReceiver, kind, id uint32, data []byte) {
		if kind != luaRequest || len(data) == 0 || data[0] != opStatus {
			t.Errorf("unexpected operation during ownership loss: %x", data)
		}
		fake.reply(luaResponse, id, true, []byte{7})
	})
	s := fakeSession(t, client)
	s.features = featureStatus
	s.statusDone = make(chan struct{})
	go s.statusLoop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for s.owned.Load() {
		select {
		case <-deadline.C:
			t.Fatal("receiver ownership loss was not applied")
		case <-ticker.C:
		}
	}
	if got := s.observation(); got.MediaFailed || got.State != "PLAYING" {
		t.Fatalf("display ownership loss interrupted audio: %+v", got)
	}
	select {
	case <-client.done:
		t.Fatal("display ownership loss closed the audio connection")
	default:
	}
}
