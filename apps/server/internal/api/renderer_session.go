package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

const rendererCommandTTL = 5 * time.Minute

type rendererSocketSession struct {
	handler    *RendererZoneAPI
	rendererID playback.RendererID
	epoch      playback.SessionEpoch
	connection net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	inFlight   string
	messages   chan rendererInbound
	controls   chan rendererControl
	readErrors chan error
	signal     *rendererSessionSignal
}

type rendererControl struct {
	opcode  byte
	payload []byte
}

func (session *rendererSocketSession) serve(parent context.Context) {
	defer session.connection.Close()
	signal := newRendererSessionSignal(func() { _ = session.connection.SetReadDeadline(time.Now()) })
	session.signal = signal
	unregister := session.handler.sessions.subscribe(session.rendererID, signal)
	untrack := session.handler.TrackRendererResource(session.rendererID, signal)
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	defer func() {
		if session.epoch != "" {
			closeErr := session.handler.store.CloseRendererSession(context.WithoutCancel(parent), playback.RendererSessionClose{
				RendererID: session.rendererID, Epoch: session.epoch, DisconnectedAt: time.Now().UTC(),
			})
			if closeErr != nil && !errors.Is(closeErr, playback.ErrClosed) &&
				!errors.Is(closeErr, playback.ErrStaleRendererEpoch) {
				session.writeProtocolFailure(closeErr)
			}
		}
		cancel()
		untrack()
		unregister()
		_ = session.connection.Close()
		if session.messages != nil {
			<-done
		}
	}()
	if err := session.writeActive(func() error {
		return session.connection.SetReadDeadline(time.Now().Add(websocketPongWait))
	}); err != nil {
		return
	}
	payload, err := newFrameReader(session.reader).readMessage(func(opcode byte, payload []byte) error {
		return session.handleControl(rendererControl{opcode: opcode, payload: payload})
	})
	if err != nil {
		session.writeReadFailure(err)
		return
	}
	hello, err := decodeRendererHello(payload, session.rendererID)
	if err != nil {
		session.writeProtocolFailure(err)
		return
	}
	now := time.Now().UTC()
	state, err := session.handler.store.OpenRendererSession(ctx, playback.RendererSessionRequest{
		RendererID: session.rendererID, LastServerSequence: playback.CommandSequence(hello.LastServerSequence),
		ConnectedAt: now,
	})
	if err != nil {
		session.writeProtocolFailure(err)
		return
	}
	session.epoch = state.Epoch
	session.handler.sessions.activate(session.rendererID, signal)
	capabilities := make([]string, 0, len(hello.Capabilities.Commands)+len(hello.Capabilities.MediaTypes)+3)
	for _, command := range hello.Capabilities.Commands {
		capabilities = append(capabilities, "command:"+command)
	}
	for _, mediaType := range hello.Capabilities.MediaTypes {
		capabilities = append(capabilities, "media:"+mediaType)
	}
	if hello.Capabilities.SupportsRange {
		capabilities = append(capabilities, "range")
	}
	capabilities = append(capabilities,
		"max-channels:"+strconv.Itoa(hello.Capabilities.MaxChannels),
		"max-sample-rate-hz:"+strconv.Itoa(hello.Capabilities.MaxSampleRateHz),
	)
	if err := session.handler.store.ObserveRendererSession(ctx, playback.RendererSessionObservation{
		RendererID: session.rendererID, Epoch: session.epoch, ProtocolMajor: rendererProtocolMajor,
		Capabilities: capabilities, ObservedAt: now,
	}); err != nil {
		session.writeProtocolFailure(err)
		return
	}
	if err := session.writeJSON(rendererWelcomeFrame{
		ProtocolMajor: rendererProtocolMajor, Type: "welcome", SelectedMajor: rendererProtocolMajor,
		SessionEpoch: string(state.Epoch), NextSequence: int64(state.NextSequence),
		Capabilities: []string{"at-least-once-delivery", "durable-results", "session-fencing"},
	}); err != nil {
		return
	}
	for _, pending := range hello.PendingResults {
		if err := session.acceptPendingResult(ctx, pending); err != nil {
			session.writeProtocolFailure(err)
			return
		}
	}
	if err := session.dispatch(ctx); err != nil {
		session.writeProtocolFailure(err)
		return
	}
	session.messages = make(chan rendererInbound, 8)
	session.controls = make(chan rendererControl, 8)
	session.readErrors = make(chan error, 1)
	go session.readClient(ctx, done)
	session.run(ctx)
}

func (session *rendererSocketSession) dispatch(ctx context.Context) error {
	if session.inFlight != "" {
		return nil
	}
	now := time.Now().UTC()
	command, err := session.handler.store.AcquireRendererCommand(ctx, playback.RendererCommandRequest{
		RendererID: session.rendererID, Epoch: session.epoch,
		AttemptedAt: now, Deadline: now.Add(rendererCommandTTL),
	})
	if errors.Is(err, playback.ErrNoRendererCommand) {
		return nil
	}
	if err != nil {
		return err
	}
	payload, err := decodeRendererJSON[rendererCommandPayload](command.Payload)
	if err != nil || payload.ZoneID != string(command.ZoneID) ||
		payload.SessionID != string(command.SessionID) || payload.PlayID != string(command.PlayID) ||
		payload.TrackID != string(command.TrackID) || payload.Kind != command.Type {
		return playback.ErrCommandDeliveryConflict
	}
	playID := string(command.PlayID)
	frame := rendererCommandFrame{
		ProtocolMajor: rendererProtocolMajor, Type: "command", CommandID: command.ID,
		Sequence: int64(command.Sequence), SessionEpoch: string(session.epoch),
		ZoneID: string(command.ZoneID), PlayID: &playID, Kind: command.Type,
		Deadline: command.Deadline.Format(time.RFC3339Nano), PositionMS: payload.PositionMS, Media: payload.Media,
	}
	if err := session.writeJSON(frame); err != nil {
		return err
	}
	session.inFlight = command.ID
	return nil
}
