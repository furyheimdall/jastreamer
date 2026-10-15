package raat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type fakeReceiver struct {
	mu       sync.Mutex
	conn     net.Conn
	endpoint endpoint
	done     chan struct{}
}

func loopbackReceiver(t *testing.T, handle func(*fakeReceiver, uint32, uint32, []byte)) (*fakeReceiver, *controlClient) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeReceiver{endpoint: endpoint{address: "127.0.0.1", localAddress: "127.0.0.1", port: listener.Addr().(*net.TCPAddr).Port}, done: make(chan struct{})}
	accepted := make(chan struct{})
	go func() {
		defer close(fake.done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		fake.conn = conn
		close(accepted)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		for {
			kind, body, err := readControlFrame(conn)
			if err != nil {
				return
			}
			if kind == 0x80000003 || kind == 0x80000004 {
				continue
			}
			if len(body) < 4 {
				return
			}
			handle(fake, kind, binary.BigEndian.Uint32(body), body[4:])
		}
	}()
	client, err := newControlClient(context.Background(), fake.endpoint)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	<-accepted
	t.Cleanup(func() { client.Close(); listener.Close(); fake.conn.Close(); <-fake.done })
	return fake, client
}
func (f *fakeReceiver) reply(kind, id uint32, final bool, data []byte) {
	flag := byte(0)
	if final {
		flag = 1
	}
	body := binary.BigEndian.AppendUint32(nil, id)
	body = append(body, flag)
	body = append(body, data...)
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = writeControlBytes(f.conn, testControlFrame(kind, body))
}
func fakeSession(t *testing.T, client *controlClient) *session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pump, pumpCancel := context.WithCancel(ctx)
	s := &session{manager: &Manager{}, client: client, ctx: ctx, cancel: cancel, pumpContext: pump, pumpCancel: pumpCancel, deviceID: "raat:test", ownerID: "owner", resource: output.Resource{URL: "raat://private", TrackID: "track", PlayID: "play", DurationMS: 60000}, format: audioFormat{"pcm", 48000, 16, 2}, snapshot: receiverSnapshot{state: 2, capacity: 96000, epoch: 1}, controls: make(chan output.RemoteControl, 16)}
	s.generation.Store(1)
	s.active.Store(true)
	s.owned.Store(true)
	t.Cleanup(s.close)
	return s
}

func TestSourceEOFCompletesOnlyAfterReceiverConsumption(t *testing.T) {
	s := fakeSession(t, nil)
	sender, receiver := net.Pipe()
	defer sender.Close()
	defer receiver.Close()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, receiver)
	}()
	s.snapshot.capacity = 16
	s.decoder = io.NopCloser(bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
	if err := s.sendPCM(context.Background(), sender, make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	sender.Close()
	<-drained
	if !s.eof || s.observation().Completed {
		t.Fatal("EOF was confused with receiver completion")
	}
	s.applySnapshot(receiverSnapshot{state: 4, written: 2, samplesOut: 2, position: 2, capacity: 16, epoch: 1})
	if !s.observation().Completed || !s.observation().CompletionKnown {
		t.Fatal("genuine receiver completion was not reported")
	}
}

type failingPCMReader struct{ err error }

func (r failingPCMReader) Read([]byte) (int, error) { return 0, r.err }
func TestPCMDecoderErrorsNeverBecomeEOF(t *testing.T) {
	for _, decoderErr := range []error{io.ErrUnexpectedEOF, errors.New("decoder exit failure")} {
		s := &session{decoder: io.NopCloser(failingPCMReader{decoderErr}), format: audioFormat{"pcm", 48000, 16, 2}}
		if err := s.sendPCM(context.Background(), nil, make([]byte, 16)); !errors.Is(err, decoderErr) {
			t.Fatalf("decoder error lost: %v", err)
		}
		if s.eof {
			t.Fatal("decoder failure became EOF")
		}
	}
	s := &session{decoder: io.NopCloser(bytes.NewReader([]byte{1, 2, 3})), format: audioFormat{"pcm", 48000, 16, 2}}
	if err := s.sendPCM(context.Background(), nil, make([]byte, 16)); err == nil {
		t.Fatal("partial frame accepted")
	}
}

func TestStopRetainsSubscriptionAndFencesOldNativeControlEpoch(t *testing.T) {
	subscribed := make(chan uint32, 1)
	snap := receiverSnapshot{state: 2, capacity: 96000, epoch: 1}
	fake, client := loopbackReceiver(t, func(fake *fakeReceiver, kind, id uint32, data []byte) {
		switch data[0] {
		case opEvents:
			fake.reply(luaResponse, id, false, append([]byte{3}, encodeTestSnapshot(snap)...))
			subscribed <- id
		case opStop:
			snap = receiverSnapshot{state: 5, epoch: 2}
			fake.reply(luaResponse, id, true, append([]byte{0}, encodeTestSnapshot(snap)...))
		default:
			t.Errorf("unexpected opcode %d", data[0])
		}
	})
	s := fakeSession(t, client)
	received := make(chan output.RemoteControl, 4)
	s.manager.config.OnControl = func(_ context.Context, c output.RemoteControl) error { received <- c; return nil }
	var err error
	s.subscription, err = client.subscribe(context.Background(), luaRequest, []byte{opEvents})
	if err != nil {
		t.Fatal(err)
	}
	s.eventDone = make(chan struct{})
	go s.eventLoop()
	go s.controlLoop()
	eventID := <-subscribed
	emit := func(epoch uint64, button string) {
		data := binary.BigEndian.AppendUint64([]byte{4}, epoch)
		data = append(data, []byte(`{"button":"`+button+`"}`)...)
		fake.reply(luaResponse, eventID, false, data)
	}
	emit(1, "pause")
	var old output.RemoteControl
	select {
	case old = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("initial control not delivered")
	}
	if old.PlayID != "play" || !old.Current() {
		t.Fatal("initial identity missing")
	}
	if err := s.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if old.Current() {
		t.Fatal("pre-stop callback remains current")
	}
	emit(1, "next")
	emit(2, "play")
	select {
	case current := <-received:
		if current.Action != "play" || current.PlayID != "" || !current.Current() {
			t.Fatalf("stale event crossed Stop: %+v", current)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle subscription lost physical Play")
	}
	if s.observation().CompletionKnown || s.observation().MediaFailed {
		t.Fatal("explicit Stop became natural completion or error")
	}
}

func TestPausedSeekSetsUpFreshEpochWithoutClaimStartOrDecoder(t *testing.T) {
	var opcodes []byte
	var mu sync.Mutex
	initial := receiverSnapshot{state: 3, capacity: 96000, position: 48000, epoch: 1}
	snap := initial
	_, client := loopbackReceiver(t, func(fake *fakeReceiver, kind, id uint32, data []byte) {
		mu.Lock()
		opcodes = append(opcodes, data[0])
		mu.Unlock()
		switch data[0] {
		case opSetup:
			snap = receiverSnapshot{state: 1, capacity: 96000, epoch: 2}
		case opPause:
			snap.state = 3
		default:
			t.Errorf("paused seek issued opcode %d", data[0])
		}
		prefix := []byte{0}
		if data[0] == opSetup {
			prefix = append(prefix, 0, 1)
		}
		fake.reply(luaResponse, id, true, append(prefix, encodeTestSnapshot(snap)...))
	})
	s := fakeSession(t, client)
	s.snapshot = initial
	if err := s.seek(context.Background(), preparedMedia{resource: s.resource}, 23000); err != nil {
		t.Fatal(err)
	}
	observed := s.observation()
	if observed.State != "PAUSED_PLAYBACK" || observed.PositionMS != 23000 {
		t.Fatalf("paused seek = %+v", observed)
	}
	mu.Lock()
	defer mu.Unlock()
	if !bytes.Equal(opcodes, []byte{opSetup, opPause}) {
		t.Fatalf("paused seek operations = %v", opcodes)
	}
	if s.decoder != nil || !s.owned.Load() {
		t.Fatal("paused seek created decoder or lost transport ownership")
	}
	s.applySnapshot(receiverSnapshot{state: 3, position: 48000, capacity: 96000, epoch: 1})
	if s.observation().PositionMS != 23000 {
		t.Fatal("old epoch rewound paused seek")
	}
}

func TestOwnershipBusReleasesControlsWithoutStoppingAudio(t *testing.T) {
	s := fakeSession(t, nil)
	if err := s.busEvent([]byte(`{"command":"status","owner_id":"other"}`)); err != nil {
		t.Fatal(err)
	}
	if s.owned.Load() || s.observation().State != "PLAYING" {
		t.Fatal("ownership takeover stopped audio or retained controls")
	}
	if err := s.busEvent([]byte(`not JSON`)); err == nil {
		t.Fatal("malformed ownership JSON accepted")
	}
}

func TestSessionRechecksIdentityBeforeLoadingAnyScript(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		kind, body, err := readControlFrame(conn)
		if err != nil {
			return
		}
		if kind != managementRequest {
			t.Error("expected get_info")
		}
		_ = writeControlBytes(conn, testControlResponse(managementResponse, binary.BigEndian.Uint32(body), 1, `{"status":"Success","info":{"unique_id":"different","protocol_version":"3","code_signing_keys":[]}}`))
		if _, _, err = readControlFrame(conn); err == nil {
			t.Error("identity mismatch still loaded receiver")
		}
	}()
	source, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	record := discoveredDevice{device: output.Device{ID: "raat:test"}, endpoint: endpoint{"127.0.0.1", "127.0.0.1", listener.Addr().(*net.TCPAddr).Port}, info: deviceInfo{uniqueID: "expected"}}
	if _, err = newSession(context.Background(), &Manager{}, record, preparedMedia{}, source, library.TrackInfo{}, 0); err == nil {
		t.Fatal("changed identity accepted")
	}
	if _, err = source.Stat(); err == nil {
		t.Fatal("failed session leaked media descriptor")
	}
	<-done
}

func TestStopCancelsBlockedProducerAndJoinsWithoutMediaFailure(t *testing.T) {
	_, client := loopbackReceiver(t, func(fake *fakeReceiver, kind, id uint32, data []byte) {
		if data[0] != opStop {
			t.Errorf("unexpected request %d", data[0])
		}
		fake.reply(luaResponse, id, true, append([]byte{0}, encodeTestSnapshot(receiverSnapshot{state: 5, epoch: 2})...))
	})
	s := fakeSession(t, client)
	reader, writer := io.Pipe()
	defer writer.Close()
	s.decoder = reader
	source, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	s.source = source
	s.startPump()
	stopped := make(chan error, 1)
	go func() { stopped <- s.stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not join blocked producer")
	}
	if s.pumpDone != nil {
		t.Fatal("producer was not joined")
	}
	if _, err = source.Stat(); err == nil {
		t.Fatal("Stop retained media descriptor")
	}
	observed := s.observation()
	if observed.MediaFailed || observed.PlayID != "" || observed.State != "STOPPED" {
		t.Fatalf("intentional cancellation became media failure: %+v", observed)
	}
}
