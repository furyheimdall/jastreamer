package raat

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"strconv"
	"time"
)

const audioTokenBytes = 32

var (
	errAudioFrame = errors.New("raat: invalid audio frame")
	errAudioWrite = errors.New("raat: audio write failed")
)

func dialAudio(ctx context.Context, control endpoint, port int, token []byte) (net.Conn, error) {
	if !validEndpoint(control) || port < 1 || port > 65535 || len(token) != audioTokenBytes {
		return nil, errors.New("raat: invalid audio endpoint or token")
	}
	// The receiver supplies only a port, never a new destination address.
	dialer := net.Dialer{Timeout: controlWriteTimeout, LocalAddr: &net.TCPAddr{IP: net.ParseIP(control.localAddress)}}
	conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(control.address, strconv.Itoa(port)))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("raat: audio connection failed")
	}
	if err := writeAudioBuffers(ctx, conn, net.Buffers{token}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func sendAudioPCM(ctx context.Context, conn net.Conn, format audioFormat, offset int64, data []byte) error {
	frameBytes := format.frameBytes()
	if frameBytes == 0 || len(data) == 0 || len(data) > maxPCMBytes || len(data)%frameBytes != 0 || offset < 0 {
		return errAudioFrame
	}
	frames := len(data) / frameBytes
	if offset > math.MaxInt64-int64(frames) {
		return errAudioFrame
	}
	var header [13]byte
	header[0] = opPCM
	binary.BigEndian.PutUint64(header[1:9], uint64(offset))
	binary.BigEndian.PutUint32(header[9:13], uint32(frames))
	// Keep PCM in the caller's buffer and submit header + payload together on TCP.
	return writeAudioBuffers(ctx, conn, net.Buffers{header[:], data})
}

func sendAudioEOF(ctx context.Context, conn net.Conn) error {
	marker := [1]byte{opEOF}
	return writeAudioBuffers(ctx, conn, net.Buffers{marker[:]})
}

// writeAudioBuffers has one writer per audio connection. Cancellation interrupts
// only this write: the caller must retain the connection until control Pause/Stop
// completes, even when cancellation leaves a partially transmitted audio frame.
func writeAudioBuffers(ctx context.Context, conn net.Conn, buffers net.Buffers) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if conn == nil {
		return errAudioWrite
	}
	deadline := time.Now().Add(controlWriteTimeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return errAudioWrite
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetWriteDeadline(time.Now())
		close(interrupted)
	})
	var expected int64
	parts := buffers[:0]
	for _, part := range buffers {
		if len(part) != 0 {
			expected += int64(len(part))
			parts = append(parts, part)
		}
	}
	written, err := parts.WriteTo(conn)
	if err == nil && written != expected {
		err = io.ErrShortWrite
	}
	// Join an already-running cancellation callback before clearing its deadline.
	if !stop() {
		<-interrupted
	}
	if resetErr := conn.SetWriteDeadline(time.Time{}); err == nil {
		err = resetErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if value, ok := ctx.Deadline(); ok && !time.Now().Before(value) {
			return context.DeadlineExceeded
		}
		return errAudioWrite
	}
	return nil
}
