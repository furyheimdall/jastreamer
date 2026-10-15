package raat

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type session struct {
	manager           *Manager
	deviceID, ownerID string
	client            *controlClient
	endpoint          endpoint
	audio             net.Conn
	ctx               context.Context
	cancel            context.CancelFunc
	active, owned     atomic.Bool
	generation        atomic.Uint64
	mu                sync.Mutex
	snapshot          receiverSnapshot
	resource          output.Resource
	format            audioFormat
	signalPath        output.SignalPath
	baseMS            int64
	known, failed     bool
	features          uint32
	source            *os.File
	decoder           io.ReadCloser
	pumpContext       context.Context
	pumpCancel        context.CancelFunc
	pumpDone          chan struct{}
	eventDone         chan struct{}
	statusDone        chan struct{}
	controls          chan output.RemoteControl
	notifications     chan struct{}
	subscription      *controlSubscription
	closeOnce         sync.Once
	sentFrames        int64
	eof               bool
}

func newSession(command context.Context, m *Manager, record discoveredDevice, prepared preparedMedia, source *os.File, info library.TrackInfo, position int64) (result *session, err error) {
	command, commandCancel := context.WithTimeout(command, 15*time.Second)
	defer commandCancel()
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{manager: m, deviceID: record.device.ID, ownerID: uuid.NewString(), ctx: ctx, cancel: cancel, endpoint: record.endpoint, resource: prepared.resource, baseMS: position, source: source, controls: make(chan output.RemoteControl, 16)}
	s.notifications = make(chan struct{}, 1)
	s.pumpContext, s.pumpCancel = context.WithCancel(ctx)
	s.generation.Store(1)
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	s.client, err = newControlClient(command, record.endpoint)
	if err != nil {
		return nil, err
	}
	raw, err := s.client.call(command, managementRequest, []byte(`{"request":"get_info"}`))
	if err != nil {
		return nil, err
	}
	actual, err := parseDeviceInfo(raw)
	if err != nil {
		return nil, err
	}
	if actual.uniqueID != record.info.uniqueID {
		return nil, errors.New("raat: output identity changed")
	}
	if actual.protocol != "3" {
		return nil, errors.New("raat: unsupported protocol version")
	}
	if signingRequired(actual.codeSigningKeys) {
		return nil, errors.New("raat: signed receiver scripts are required by output")
	}
	if err = s.client.startKeepalive(ctx); err != nil {
		return nil, err
	}
	load, _ := json.Marshal(map[string]any{"request": "load_script", "name": "jastreamer-receiver.lua", "script": receiverScript})
	raw, err = s.client.call(command, managementRequest, load)
	if err != nil {
		return nil, err
	}
	var loaded struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &loaded) != nil || loaded.Status != "Success" {
		return nil, errors.New("raat: receiver script could not be loaded")
	}
	_, raw, err = receiverCall(command, s.client, opInfo, nil)
	if err != nil {
		return nil, err
	}
	var formats []audioFormat
	s.features, formats, err = receiverFormats(raw)
	if err != nil {
		return nil, err
	}
	s.format, err = selectAudioFormat(info.Audio, formats)
	if err != nil {
		return nil, err
	}
	s.signalPath = pcmSignalPath(info.Audio, s.format)
	s.subscription, err = s.client.subscribe(command, luaRequest, []byte{opEvents})
	if err != nil {
		return nil, err
	}
	s.eventDone = make(chan struct{})
	go s.eventLoop()
	claim, _ := json.Marshal(map[string]string{"command": "status", "owner_id": s.ownerID})
	if _, _, err = receiverCall(command, s.client, opClaim, claim); err != nil {
		return nil, err
	}
	s.owned.Store(true)
	snap, err := s.setupAudio(command, true)
	if err != nil {
		return nil, err
	}
	s.applySnapshot(snap)
	// Only startup is tied to the command context. Established playback survives it.
	stopStartup := context.AfterFunc(command, s.pumpCancel)
	defer stopStartup()
	s.decoder, err = openPCM(s.pumpContext, m.config.FFmpegPath, source, s.format, position)
	if err != nil {
		return nil, err
	}
	s.startPump()
	_, raw, err = receiverCall(command, s.client, opStart, nil)
	if err != nil {
		return nil, err
	}
	snap, err = parseSnapshot(raw)
	if err != nil {
		return nil, err
	}
	s.applySnapshot(snap)
	stopStartup()
	if err = command.Err(); err != nil {
		return nil, err
	}
	if err = s.ctx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *session) startWorkers() {
	go s.controlLoop()
	s.statusDone = make(chan struct{})
	go s.statusLoop()
}

func (s *session) startPump() {
	if s.decoder != nil {
		s.pumpDone = make(chan struct{})
		done, decoder, source := s.pumpDone, s.decoder, s.source
		s.mu.Lock()
		audio := s.audio
		s.mu.Unlock()
		go func() {
			defer close(done)
			defer decoder.Close()
			defer source.Close()
			buffer := make([]byte, s.chunkBytes())
			for !s.eof {
				if err := s.sendPCM(s.pumpContext, audio, buffer); err != nil {
					if s.pumpContext.Err() == nil {
						s.fail()
					}
					return
				}
			}
		}()
	}
}

func (s *session) chunkBytes() int {
	return min(maxPCMBytes/s.format.frameBytes(), max(1, s.format.sampleRate/10)) * s.format.frameBytes()
}

func (s *session) rpc(ctx context.Context, opcode byte, data []byte) (byte, []byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return receiverCall(callCtx, s.client, opcode, data)
}

func (s *session) sendPCM(ctx context.Context, audio net.Conn, buffer []byte) error {
	n, err := readPCMChunk(s.decoder, buffer)
	eof := errors.Is(err, io.EOF)
	if err != nil && !eof {
		return err
	}
	if n%s.format.frameBytes() != 0 {
		return errors.New("raat: decoder returned a partial PCM frame")
	}
	if n > 0 {
		if err := sendAudioPCM(ctx, audio, s.format, s.sentFrames, buffer[:n]); err != nil {
			return err
		}
		s.sentFrames += int64(n / s.format.frameBytes())
	}
	if eof {
		if err := sendAudioEOF(ctx, audio); err != nil {
			return err
		}
		s.eof = true
	}
	return nil
}

func (s *session) setupAudio(ctx context.Context, connect bool) (receiverSnapshot, error) {
	var token [audioTokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return receiverSnapshot{}, err
	}
	capacity := s.format.sampleRate * 2
	ready := capacity - s.chunkBytes()/s.format.frameBytes()
	_, raw, err := s.rpc(ctx, opSetup, setupPayload(s.format, capacity, ready, token[:]))
	if err != nil {
		return receiverSnapshot{}, err
	}
	if len(raw) != 91 {
		return receiverSnapshot{}, errors.New("raat: malformed audio setup")
	}
	port := int(binary.BigEndian.Uint16(raw))
	if port == 0 {
		return receiverSnapshot{}, errors.New("raat: invalid audio port")
	}
	snap, err := parseSnapshot(raw[2:])
	if err != nil {
		return receiverSnapshot{}, err
	}
	s.sentFrames, s.eof = 0, false
	if !connect {
		return snap, nil
	}
	audio, err := dialAudio(ctx, s.endpoint, port, token[:])
	if err != nil {
		return receiverSnapshot{}, err
	}
	s.mu.Lock()
	if s.failed || s.ctx.Err() != nil {
		s.mu.Unlock()
		audio.Close()
		return receiverSnapshot{}, errors.New("raat: audio session closed during setup")
	}
	s.audio = audio
	s.mu.Unlock()
	return snap, nil
}

func (s *session) closeAudio() {
	s.mu.Lock()
	audio := s.audio
	s.audio = nil
	s.mu.Unlock()
	if audio != nil {
		_ = audio.Close()
	}
}

func readPCMChunk(reader io.Reader, buffer []byte) (int, error) {
	offset, empty := 0, 0
	for offset < len(buffer) {
		n, err := reader.Read(buffer[offset:])
		offset += n
		if err != nil {
			return offset, err
		}
		if n == 0 {
			empty++
			if empty >= 100 {
				return offset, io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
	return offset, nil
}

func (s *session) applySnapshot(snap receiverSnapshot) {
	s.mu.Lock()
	if snap.epoch < s.snapshot.epoch {
		s.mu.Unlock()
		return
	}
	if snap.epoch == s.snapshot.epoch && (snap.written < s.snapshot.written || snap.samplesOut < s.snapshot.samplesOut || (s.snapshot.state == 2 && snap.state == 1) || (s.snapshot.state == 3 && snap.state < 3) || (s.snapshot.state >= 4 && s.snapshot.state != 5 && snap.state < s.snapshot.state)) {
		s.mu.Unlock()
		return
	}
	if snap.epoch == s.snapshot.epoch && snap.position < s.snapshot.position && snap.state == s.snapshot.state {
		snap.position = s.snapshot.position
	}
	s.snapshot = snap
	if snap.state == 2 || snap.state == 3 || snap.state == 4 || snap.state == 6 {
		s.known = true
	}
	s.mu.Unlock()
	if snap.state == 6 {
		s.fail()
		return
	}
	if s.active.Load() {
		s.notify()
	}
}

func (s *session) fail() {
	s.owned.Store(false)
	s.generation.Add(1)
	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return
	}
	s.failed = true
	s.known = true
	s.snapshot.state = 6
	s.mu.Unlock()
	s.notify()
	s.cancel()
	s.closeAudio()
	if s.client != nil {
		_ = s.client.Close()
	}
}

func (s *session) eventLoop() {
	defer close(s.eventDone)
	for {
		select {
		case <-s.ctx.Done():
			return
		case reply, open := <-s.subscription.Replies:
			if !open || reply.Err != nil || reply.Final || len(reply.Payload) == 0 {
				if s.ctx.Err() == nil {
					s.fail()
				}
				return
			}
			data := reply.Payload[1:]
			switch reply.Payload[0] {
			case 3:
				snap, err := parseSnapshot(data)
				if err != nil {
					s.fail()
					return
				}
				s.applySnapshot(snap)
			case 4:
				if len(data) < 8 {
					s.fail()
					return
				}
				epoch := int64(binary.BigEndian.Uint64(data))
				commands, err := parseControls(data[8:])
				if err != nil {
					s.fail()
					return
				}
				s.mu.Lock()
				playID := s.resource.PlayID
				currentEpoch := s.snapshot.epoch
				generation := s.generation.Load()
				s.mu.Unlock()
				if epoch != currentEpoch || !s.active.Load() || !s.owned.Load() {
					continue
				}
				for _, command := range commands {
					command.DeviceID = s.deviceID
					command.PlayID = playID
					command.Current = func() bool { return s.active.Load() && s.owned.Load() && s.generation.Load() == generation }
					select {
					case s.controls <- command:
					default:
						s.fail()
						return
					}
				}
			case 5:
				if err := s.busEvent(data); err != nil {
					s.fail()
					return
				}
			case 6:
				s.fail()
				return
			default:
				s.fail()
				return
			}
		}
	}
}

func (s *session) busEvent(data []byte) error {
	if len(data) > 16<<10 {
		return errors.New("raat: ownership announcement too large")
	}
	var event struct {
		Command string `json:"command"`
		OwnerID string `json:"owner_id"`
	}
	if json.Unmarshal(data, &event) != nil {
		return errors.New("raat: invalid ownership announcement")
	}
	switch event.Command {
	case "status":
		if event.OwnerID != s.ownerID && s.owned.Swap(false) {
			s.generation.Add(1)
			// The receiver has already relinquished display/button ownership
			// before publishing this event; never wait for an RPC in this reader.
		}
	}
	return nil
}

func (s *session) notify() {
	select {
	case s.notifications <- struct{}{}:
	default:
	}
}

func (s *session) controlLoop() {
	publish := func() {
		if s.manager.config.Notify != nil {
			s.manager.config.Notify("player")
		}
	}
	for {
		select {
		case <-s.ctx.Done():
			select {
			case <-s.notifications:
				publish()
			default:
			}
			return
		case <-s.notifications:
			publish()
		case command := <-s.controls:
			if command.Current() && s.manager.config.OnControl != nil {
				_ = s.manager.config.OnControl(s.ctx, command)
			}
		}
	}
}

func (s *session) statusLoop() {
	defer close(s.statusDone)
	s.publishArtwork()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.owned.Load() && s.features&featureStatus != 0 {
			s.mu.Lock()
			resource, format := s.resource, s.format
			position, state := s.positionLocked(), s.snapshot.state
			s.mu.Unlock()
			status := TransportState{RendererID: s.deviceID, PositionMS: position, DurationMS: resource.DurationMS, CanPlay: true, CanPause: state == 2, CanSeek: resource.PlayID != "", State: "stopped"}
			switch state {
			case 1:
				status.State = "loading"
			case 2:
				status.State = "playing"
			case 3:
				status.State = "paused"
			}
			publish := true
			if s.manager.config.State != nil {
				actual, err := s.manager.config.State(s.ctx)
				if err != nil || actual.RendererID != s.deviceID {
					publish = false
				} else {
					status = actual
				}
			}
			if publish {
				raw, err := nativeStatus(resource, format, status)
				if err == nil && s.owned.Load() {
					if _, _, err = s.rpc(s.ctx, opStatus, raw); errors.Is(err, errTransportOwnership) {
						s.owned.Store(false)
						s.generation.Add(1)
					} else if err != nil && s.ctx.Err() == nil {
						s.fail()
						return
					}
				}
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *session) publishArtwork() {
	if !s.owned.Load() || s.features&featureArtwork == 0 {
		return
	}
	s.mu.Lock()
	uri := s.resource.URL
	s.mu.Unlock()
	s.manager.mu.Lock()
	prepared, ok := s.manager.prepared[uri]
	s.manager.mu.Unlock()
	var image []byte
	if ok && prepared.track.ArtworkID != "" {
		file, mime, err := s.manager.library.Artwork(s.ctx, prepared.track.ArtworkID)
		if err != nil {
			return
		}
		defer file.Close()
		if mime != "image/jpeg" {
			return
		}
		image, err = io.ReadAll(io.LimitReader(file, maxArtworkBytes+1))
		if err != nil || len(image) > maxArtworkBytes {
			return
		}
	}
	if s.owned.Load() {
		if _, _, err := s.rpc(s.ctx, opArtwork, image); errors.Is(err, errTransportOwnership) {
			s.owned.Store(false)
			s.generation.Add(1)
		}
	}
}

func (s *session) stopPump() {
	if s.pumpCancel != nil {
		s.pumpCancel()
	}
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	if s.pumpDone != nil {
		<-s.pumpDone
		s.pumpDone = nil
	}
	if s.source != nil {
		_ = s.source.Close()
		s.source = nil
	}
}
func (s *session) pause(ctx context.Context) error {
	s.generation.Add(1)
	s.stopPump()
	_, raw, err := s.rpc(ctx, opPause, nil)
	s.closeAudio()
	if err != nil {
		s.fail()
		return err
	}
	snap, err := parseSnapshot(raw)
	if err != nil {
		s.fail()
		return err
	}
	s.applySnapshot(snap)
	return nil
}
func (s *session) stop(ctx context.Context) error {
	s.generation.Add(1)
	s.stopPump()
	_, raw, err := s.rpc(ctx, opStop, nil)
	s.closeAudio()
	if err != nil {
		s.fail()
		return err
	}
	snap, err := parseSnapshot(raw)
	if err != nil {
		s.fail()
		return err
	}
	s.mu.Lock()
	s.resource.PlayID = ""
	s.known = false
	s.generation.Add(1)
	s.mu.Unlock()
	s.applySnapshot(snap)
	return nil
}

// Seek replaces only the stream, not source ownership or the control subscription.
func (s *session) seek(command context.Context, prepared preparedMedia, position int64) (err error) {
	if position < 0 || (prepared.resource.DurationMS > 0 && position >= prepared.resource.DurationMS) {
		return errors.New("raat: seek outside media")
	}
	s.mu.Lock()
	state := s.snapshot.state
	s.mu.Unlock()
	if state != 2 && state != 3 {
		return errors.New("raat: cannot seek inactive stream")
	}
	command, cancel := context.WithTimeout(command, 15*time.Second)
	defer cancel()
	if state == 3 {
		s.active.Store(false)
		s.generation.Add(1)
		defer func() {
			if err != nil {
				s.fail()
			}
		}()
		snap, callErr := s.setupAudio(command, false)
		if callErr != nil {
			return callErr
		}
		s.mu.Lock()
		s.baseMS = position
		s.snapshot = snap
		s.known = false
		s.mu.Unlock()
		_, raw, callErr := s.rpc(command, opPause, nil)
		err = callErr
		if err != nil {
			return err
		}
		snap, err = parseSnapshot(raw)
		if err != nil {
			return err
		}
		s.applySnapshot(snap)
		s.generation.Add(1)
		s.active.Store(true)
		s.notify()
		return nil
	}
	file, info, err := s.manager.library.OpenAudio(command, prepared.track.ID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = file.Close()
			s.fail()
			s.stopPump()
		}
	}()
	if !sameTrack(prepared.track, info.Track) {
		return errors.New("raat: prepared media changed")
	}
	if _, err = selectAudioFormat(info.Audio, []audioFormat{s.format}); err != nil {
		return err
	}
	s.active.Store(false)
	if err = s.pause(command); err != nil {
		return err
	}
	s.source = file
	s.eof = false
	s.pumpContext, s.pumpCancel = context.WithCancel(s.ctx)
	stopStartup := context.AfterFunc(command, s.pumpCancel)
	defer stopStartup()
	snap, err := s.setupAudio(command, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.baseMS = position
	s.snapshot = snap
	s.known = false
	s.signalPath = pcmSignalPath(info.Audio, s.format)
	s.generation.Add(1)
	s.mu.Unlock()
	s.decoder, err = openPCM(s.pumpContext, s.manager.config.FFmpegPath, file, s.format, position)
	if err != nil {
		return err
	}
	s.startPump()
	_, raw, err := s.rpc(command, opStart, nil)
	if err != nil {
		return err
	}
	snap, err = parseSnapshot(raw)
	if err != nil {
		return err
	}
	s.applySnapshot(snap)
	stopStartup()
	if err = command.Err(); err != nil {
		return err
	}
	if err = s.ctx.Err(); err != nil {
		return err
	}
	s.active.Store(true)
	s.notify()
	return nil
}
func (s *session) close() {
	s.closeOnce.Do(func() {
		s.active.Store(false)
		s.owned.Store(false)
		s.generation.Add(1)
		s.cancel()
		s.closeAudio()
		s.stopPump()
		if s.client != nil {
			_ = s.client.Close()
		}
		if s.eventDone != nil {
			<-s.eventDone
		}
		if s.statusDone != nil {
			<-s.statusDone
		}
	})
}
func (s *session) positionLocked() int64 {
	if !s.format.valid() {
		return s.baseMS
	}
	frames := s.snapshot.position
	return s.baseMS + frames/int64(s.format.sampleRate)*1000 + (frames%int64(s.format.sampleRate))*1000/int64(s.format.sampleRate)
}
func (s *session) observation() output.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "TRANSITIONING"
	switch s.snapshot.state {
	case 2:
		state = "PLAYING"
	case 3:
		state = "PAUSED_PLAYBACK"
	case 4, 5, 6:
		state = "STOPPED"
	}
	status := "OK"
	if s.failed {
		status = "ERROR_OCCURRED"
	}
	return output.Observation{State: state, PositionMS: s.positionLocked(), DurationMS: s.resource.DurationMS, URI: s.resource.URL, HasURI: s.resource.PlayID != "", HasPosition: s.known, ObservedAt: time.Now(), TransportStatus: status, CompletionKnown: s.known, Completed: s.snapshot.state == 4, MediaFailed: s.failed, PlayID: s.resource.PlayID}
}
