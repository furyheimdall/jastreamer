package api

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func (session *rendererSocketSession) run(ctx context.Context) {
	ping := time.NewTicker(websocketPingPeriod)
	defer ping.Stop()
	retry := newRendererRetryTimer()
	defer retry.stop()
	if err := retry.schedule(ctx, session); err != nil {
		session.writeProtocolFailure(err)
		return
	}
	for {
		select {
		case message := <-session.messages:
			if err := session.handleMessage(ctx, message); err != nil {
				session.writeProtocolFailure(err)
				return
			}
			if err := retry.schedule(ctx, session); err != nil {
				session.writeProtocolFailure(err)
				return
			}
		case <-retry.channel:
			retry.channel = nil
			session.inFlight = ""
			if err := session.dispatch(ctx); err != nil {
				session.writeProtocolFailure(err)
				return
			}
			if err := retry.schedule(ctx, session); err != nil {
				session.writeProtocolFailure(err)
				return
			}
		case control := <-session.controls:
			if err := session.handleControl(control); err != nil {
				return
			}
			if session.inFlight == "" {
				if err := session.dispatch(ctx); err != nil {
					session.writeProtocolFailure(err)
					return
				}
				if err := retry.schedule(ctx, session); err != nil {
					session.writeProtocolFailure(err)
					return
				}
			}
		case readErr := <-session.readErrors:
			session.writeReadFailure(readErr)
			return
		case <-session.signal.changed:
			_ = session.terminate(rendererSessionCloseSignal{}, nil)
			return
		case <-ping.C:
			if err := session.writeActive(func() error {
				if err := writeFrame(session.writer, opcodePing, nil); err != nil {
					return err
				}
				return session.writer.Flush()
			}); err != nil {
				return
			}
			if session.inFlight == "" {
				if err := session.dispatch(ctx); err != nil {
					session.writeProtocolFailure(err)
					return
				}
				if err := retry.schedule(ctx, session); err != nil {
					session.writeProtocolFailure(err)
					return
				}
			}
		case <-ctx.Done():
			_ = session.terminate(rendererSessionCloseSignal{}, nil)
			return
		case <-session.handler.sessions.shutdown:
			_ = session.terminate(rendererSessionCloseSignal{
				code: closeGoingAway, reason: rendererShutdownCloseReason,
			}, nil)
			return
		}
	}
}

func (session *rendererSocketSession) handleMessage(ctx context.Context, message rendererInbound) error {
	switch value := message.(type) {
	case rendererAckMessage:
		ack := value.frame
		if err := session.handler.store.RecordRendererCommandAcknowledgement(ctx, playback.RendererCommandAcknowledgement{
			RendererID: session.rendererID, Epoch: session.epoch, CommandID: ack.CommandID,
			Sequence: playback.CommandSequence(ack.Sequence), Status: playback.CommandAckStatus(ack.Status),
			Error: ack.Error, RecordedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		if ack.Status == string(playback.CommandAckRejected) {
			session.inFlight = ""
			return session.dispatch(ctx)
		}
		return nil
	case rendererResultMessage:
		if err := session.acceptResult(ctx, value.frame, false); err != nil {
			return err
		}
		if session.inFlight == value.frame.CommandID {
			session.inFlight = ""
		}
		return session.dispatch(ctx)
	case rendererPlaybackEventMessage:
		observedAt, err := time.Parse(time.RFC3339Nano, value.frame.ObservedAt)
		if err != nil {
			return protocolError("INVALID_MESSAGE", "playback event timestamp is invalid", false)
		}
		if _, err := session.handler.store.HandleRendererPlaybackEvent(ctx, playback.RendererPlaybackEvent{
			RendererID: session.rendererID, Epoch: playback.SessionEpoch(value.frame.SessionEpoch),
			EventID: value.frame.EventID, PlayID: playback.PlayID(value.frame.PlayID),
			Kind: playback.PlaybackEventKind(value.frame.Kind), PositionMS: value.frame.PositionMS,
			ObservedAt: observedAt,
		}); err != nil {
			return err
		}
		if value.frame.Kind == string(playback.PlaybackEventEnded) {
			session.inFlight = ""
			return session.dispatch(ctx)
		}
		return nil
	default:
		return protocolError("INVALID_MESSAGE", "message variant is unsupported", false)
	}
}

func (session *rendererSocketSession) readClient(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	reader := newFrameReader(session.reader)
	for {
		payload, err := reader.readMessage(func(opcode byte, payload []byte) error {
			control := rendererControl{opcode: opcode, payload: append([]byte(nil), payload...)}
			select {
			case session.controls <- control:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		})
		if err != nil {
			session.reportReadError(ctx, err)
			return
		}
		message, err := decodeRendererInbound(payload)
		if err != nil {
			session.reportReadError(ctx, err)
			return
		}
		select {
		case session.messages <- message:
		case <-ctx.Done():
			return
		}
	}
}

func (session *rendererSocketSession) reportReadError(ctx context.Context, err error) {
	select {
	case session.readErrors <- err:
	case <-ctx.Done():
	}
}

func (session *rendererSocketSession) writeReadFailure(err error) {
	closing := rendererSessionCloseSignal{}
	var closeErr *websocketCloseError
	if errors.As(err, &closeErr) {
		if !closeErr.peer {
			closing = rendererSessionCloseSignal{code: closeErr.code, reason: closeErr.reason}
		}
		_ = session.terminate(closing, nil)
		return
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		closing = rendererSessionCloseSignal{code: closePolicyViolation, reason: rendererHeartbeatCloseReason}
	}
	_ = session.terminate(closing, nil)
}
