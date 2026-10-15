package raat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"testing"
	"time"
)

func testAudioPipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	local, peer := net.Pipe()
	t.Cleanup(func() { _ = local.Close(); _ = peer.Close() })
	if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return local, peer
}

func testAudioRead(t *testing.T, conn net.Conn, size int) []byte {
	t.Helper()
	data := make([]byte, size)
	// Deliberately split headers and samples across read boundaries.
	for offset := 0; offset < len(data); {
		length := min(1+offset%97, len(data)-offset)
		if _, err := io.ReadFull(conn, data[offset:offset+length]); err != nil {
			t.Fatal(err)
		}
		offset += length
	}
	return data
}

func testAudioResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("audio write did not finish")
		return nil
	}
}

func testAudioPacket(offset int64, frames int, data []byte) []byte {
	packet := []byte{3}
	packet = binary.BigEndian.AppendUint64(packet, uint64(offset))
	packet = binary.BigEndian.AppendUint32(packet, uint32(frames))
	return append(packet, data...)
}

func TestAudioPCMExactBytesAndEOFOrdering(t *testing.T) {
	formats := []audioFormat{
		{sampleType: "pcm", sampleRate: 44100, bits: 16, channels: 1},
		{sampleType: "pcm", sampleRate: 96000, bits: 24, channels: 2},
		{sampleType: "pcm", sampleRate: 192000, bits: 32, channels: 8},
		{sampleType: "pcm_float32", sampleRate: 48000, bits: 32, channels: 2},
		{sampleType: "pcm_float64", sampleRate: maximumPCMSampleRate, bits: 64, channels: maximumPCMChannels},
	}
	for _, format := range formats {
		for _, frames := range []int{1, maxPCMBytes / format.frameBytes()} {
			local, peer := testAudioPipe(t)
			data := make([]byte, frames*format.frameBytes())
			for i := range data {
				data[i] = byte(i*37 + 11)
			}
			original := bytes.Clone(data)
			offset := int64(math.MaxInt64) - int64(frames) - 1
			next := data[:format.frameBytes()]
			result := make(chan error, 1)
			go func() {
				if err := sendAudioPCM(context.Background(), local, format, offset, data); err != nil {
					result <- err
					return
				}
				if err := sendAudioPCM(context.Background(), local, format, offset+int64(frames), next); err != nil {
					result <- err
					return
				}
				result <- sendAudioEOF(context.Background(), local)
			}()
			want := testAudioPacket(offset, frames, data)
			want = append(want, testAudioPacket(offset+int64(frames), 1, next)...)
			want = append(want, 7)
			if got := testAudioRead(t, peer, len(want)); !bytes.Equal(got, want) {
				t.Fatalf("PCM bytes changed for format %+v, frames %d", format, frames)
			}
			if err := testAudioResult(t, result); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, original) {
				t.Fatal("audio write mutated caller PCM")
			}
		}
	}
}

func TestAudioPCMRejectsMalformedBoundsWithoutWriting(t *testing.T) {
	format := audioFormat{sampleType: "pcm", sampleRate: 48000, bits: 16, channels: 2}
	cases := []struct {
		name   string
		format audioFormat
		offset int64
		data   []byte
	}{
		{"empty", format, 0, nil},
		{"unaligned", format, 0, []byte{1, 2, 3}},
		{"oversized", format, 0, make([]byte, maxPCMBytes+4)},
		{"negative offset", format, -1, make([]byte, 4)},
		{"overflow one frame", format, math.MaxInt64, make([]byte, 4)},
		{"overflow multiple frames", format, math.MaxInt64 - 1, make([]byte, 8)},
		{"unknown format", audioFormat{}, 0, make([]byte, 4)},
		{"invalid bits", audioFormat{sampleType: "pcm", sampleRate: 48000, bits: 8, channels: 2}, 0, make([]byte, 4)},
		{"invalid float", audioFormat{sampleType: "pcm_float32", sampleRate: 48000, bits: 64, channels: 2}, 0, make([]byte, 16)},
		{"too many channels", audioFormat{sampleType: "pcm", sampleRate: 48000, bits: 16, channels: maximumPCMChannels + 1}, 0, make([]byte, 66)},
		{"too high rate", audioFormat{sampleType: "pcm", sampleRate: maximumPCMSampleRate + 1, bits: 16, channels: 2}, 0, make([]byte, 4)},
	}
	local, peer := testAudioPipe(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := sendAudioPCM(context.Background(), local, tc.format, tc.offset, tc.data); !errors.Is(err, errAudioFrame) {
				t.Fatalf("malformed PCM: %v", err)
			}
		})
	}
	if err := peer.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if n, err := peer.Read(data[:]); n != 0 || err == nil {
		t.Fatalf("invalid input wrote bytes: %x, %v", data[:n], err)
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("invalid input closed the audio connection: %v", err)
	}
}

func TestAudioCancellationLeavesControlAndAudioOpen(t *testing.T) {
	local, peer := testAudioPipe(t)
	control, controlPeer := testControlPipe(t)
	format := audioFormat{sampleType: "pcm", sampleRate: 48000, bits: 16, channels: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- sendAudioPCM(ctx, local, format, 0, make([]byte, maxPCMBytes)) }()
	// Reading only the header makes cancellation interrupt a partially sent packet.
	if got := testAudioRead(t, peer, 13); !bytes.Equal(got, testAudioPacket(0, maxPCMBytes/4, nil)) {
		t.Fatalf("unexpected PCM header: %x", got)
	}
	cancel()
	if err := testAudioResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	go func() { result <- control.send(context.Background(), luaRequest, []byte{opPause}) }()
	kind, payload, err := readControlFrame(controlPeer)
	if err != nil || kind != luaRequest || !bytes.Equal(payload, []byte{opPause}) {
		t.Fatalf("control Pause bytes after audio cancellation: %d %x %v", kind, payload, err)
	}
	if err := testAudioResult(t, result); err != nil {
		t.Fatal(err)
	}
	// This probes lifetime/deadline cleanup, not recovery of the truncated stream.
	go func() { result <- sendAudioEOF(context.Background(), local) }()
	if got := testAudioRead(t, peer, 1); got[0] != 7 {
		t.Fatalf("audio connection no longer writable: %x", got)
	}
	if err := testAudioResult(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestAudioEOFCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		local, peer := testAudioPipe(t)
		var ctx context.Context
		var cancel context.CancelFunc
		wantErr := error(context.Canceled)
		if deadline {
			ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
			wantErr = context.DeadlineExceeded
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		result := make(chan error, 1)
		go func() { result <- sendAudioEOF(ctx, local) }()
		err := testAudioResult(t, result)
		cancel()
		if !errors.Is(err, wantErr) {
			t.Fatalf("EOF cancellation (deadline=%v): %v", deadline, err)
		}
		go func() { result <- sendAudioEOF(context.Background(), local) }()
		if got := testAudioRead(t, peer, 1); got[0] != 7 {
			t.Fatalf("EOF after cancellation: %x", got)
		}
		if err := testAudioResult(t, result); err != nil {
			t.Fatal(err)
		}
		// Successful EOF must not close the connection either.
		go func() { result <- sendAudioEOF(context.Background(), local) }()
		if got := testAudioRead(t, peer, 1); got[0] != 7 {
			t.Fatalf("connection closed after EOF: %x", got)
		}
		if err := testAudioResult(t, result); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDialAudioTokenAndTCPFraming(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	token := make([]byte, audioTokenBytes)
	for i := range token {
		token[i] = byte(i + 1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	control := endpoint{address: "127.0.0.1", localAddress: "127.0.0.1", port: 1}
	format := audioFormat{sampleType: "pcm", sampleRate: 48000, bits: 24, channels: 2}
	pcm := []byte{0, 255, 128, 127, 1, 254, 2, 253, 3, 252, 4, 251}
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := dialAudio(ctx, control, port, token)
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		if err := sendAudioPCM(ctx, conn, format, 0, pcm); err != nil {
			result <- err
			return
		}
		result <- sendAudioEOF(ctx, conn)
	}()
	peer, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := peer.RemoteAddr().(*net.TCPAddr).IP.String(); got != control.localAddress {
		t.Fatalf("audio did not bind control local address: %s", got)
	}
	want := append(bytes.Clone(token), testAudioPacket(0, 2, pcm)...)
	want = append(want, 7)
	if got := testAudioRead(t, peer, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("audio TCP bytes: %x; want %x", got, want)
	}
	if err := testAudioResult(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestDialAudioRejectsInvalidEndpointsAndTokens(t *testing.T) {
	valid := endpoint{address: "127.0.0.1", localAddress: "127.0.0.1", port: 1}
	token := make([]byte, audioTokenBytes)
	cases := []struct {
		name    string
		control endpoint
		port    int
		token   []byte
	}{
		{"zero audio port", valid, 0, token},
		{"negative audio port", valid, -1, token},
		{"oversized audio port", valid, 65536, token},
		{"missing token", valid, 1, nil},
		{"short token", valid, 1, token[:audioTokenBytes-1]},
		{"long token", valid, 1, make([]byte, audioTokenBytes+1)},
		{"public peer", endpoint{address: "8.8.8.8", localAddress: "192.168.1.2", port: 1}, 1, token},
		{"hostname peer", endpoint{address: "localhost", localAddress: "127.0.0.1", port: 1}, 1, token},
		{"mismatched local address", endpoint{address: "127.0.0.1", localAddress: "192.168.1.2", port: 1}, 1, token},
		{"invalid control port", endpoint{address: "127.0.0.1", localAddress: "127.0.0.1", port: 0}, 1, token},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := dialAudio(ctx, tc.control, tc.port, tc.token)
			if conn != nil {
				_ = conn.Close()
				t.Fatal("invalid input returned a connection")
			}
			if err == nil || errors.Is(err, context.Canceled) {
				t.Fatalf("invalid input reached dialing: %v", err)
			}
		})
	}
	if conn, err := dialAudio(ctx, valid, 1, token); conn != nil || !errors.Is(err, context.Canceled) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("cancelled audio dial: conn=%v err=%v", conn, err)
	}
}
