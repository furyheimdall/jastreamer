package api

import (
	"bufio"
	"context"
	"errors"
	"time"
)

const (
	eventRevocationCloseCode   = closePolicyViolation
	eventRevocationCloseReason = "device revoked"
	eventShutdownCloseCode     = closeGoingAway
	eventShutdownCloseReason   = "server shutting down"
)

type eventControl struct {
	opcode  byte
	payload []byte
}

type eventStream struct {
	service         *server
	writer          *bufio.Writer
	setReadDeadline func(time.Time) error
	subscription    eventSubscription
	controls        chan eventControl
	readErrors      chan error
}

func (stream eventStream) run(ctx context.Context) {
	ping := time.NewTicker(websocketPingPeriod)
	defer ping.Stop()
	sequence := stream.subscription.snapshot.Sequence
	for {
		select {
		case event := <-stream.subscription.events:
			if validateEventSequence(sequence, event) != nil {
				active, _ := stream.subscription.subscriber.writeIfActive(func() error {
					stream.service.writeResync(stream.writer, event.Epoch, event.Sequence)
					return nil
				})
				if !active {
					_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
				}
				return
			}
			sequence = event.Sequence
			active, err := stream.subscription.subscriber.writeIfActive(func() error {
				return stream.service.writeEvent(stream.writer, event)
			})
			if err != nil {
				return
			}
			if !active {
				_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
				return
			}
		case signal := <-stream.subscription.resync:
			active, _ := stream.subscription.subscriber.writeIfActive(func() error {
				stream.service.writeResync(stream.writer, signal.Epoch, signal.Sequence)
				return nil
			})
			if !active {
				_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
			}
			return
		case control := <-stream.controls:
			active, err := stream.subscription.subscriber.writeIfActive(func() error {
				if control.opcode == opcodePong {
					return stream.setReadDeadline(time.Now().Add(websocketPongWait))
				}
				if err := writeFrame(stream.writer, opcodePong, control.payload); err != nil {
					return err
				}
				return stream.writer.Flush()
			})
			if err != nil {
				return
			}
			if !active {
				_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
				return
			}
		case <-stream.subscription.revoked:
			_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
			return
		case readErr := <-stream.readErrors:
			active, _ := stream.subscription.subscriber.writeIfActive(func() error {
				var closeErr *websocketCloseError
				if errors.As(readErr, &closeErr) {
					return writeClose(stream.writer, closeErr.code, closeErr.reason)
				}
				return nil
			})
			if !active {
				_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
			}
			return
		case <-ping.C:
			active, err := stream.subscription.subscriber.writeIfActive(func() error {
				if err := writeFrame(stream.writer, opcodePing, nil); err != nil {
					return err
				}
				return stream.writer.Flush()
			})
			if err != nil {
				return
			}
			if !active {
				_ = writeClose(stream.writer, eventRevocationCloseCode, eventRevocationCloseReason)
				return
			}
		case <-ctx.Done():
			return
		case <-stream.service.eventHub.done:
			_ = writeClose(stream.writer, eventShutdownCloseCode, eventShutdownCloseReason)
			return
		}
	}
}

func (stream eventStream) readClientFrames(ctx context.Context, reader *bufio.Reader, done chan<- struct{}) {
	defer close(done)
	_, err := newFrameReader(reader).readMessage(func(opcode byte, payload []byte) error {
		select {
		case stream.controls <- eventControl{opcode: opcode, payload: append([]byte(nil), payload...)}:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	})
	if err == nil {
		err = &websocketCloseError{code: closeUnsupportedData, reason: "event stream accepts control frames only"}
	}
	select {
	case stream.readErrors <- err:
	case <-ctx.Done():
	}
}
