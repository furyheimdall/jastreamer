package api_test

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func (socket *rendererSocket) sendJSON(t *testing.T, payload json.RawMessage) {
	t.Helper()
	if !json.Valid(payload) {
		t.Fatalf("invalid renderer JSON frame: %q", payload)
	}
	socket.sendFrame(t, 0x1, payload)
}

func (socket *rendererSocket) sendFrame(t *testing.T, opcode byte, payload []byte) {
	t.Helper()
	if err := socket.trySendFrame(opcode, payload); err != nil {
		t.Fatalf("write renderer frame: %v", err)
	}
}

func (socket *rendererSocket) trySendFrame(opcode byte, payload []byte) error {
	if err := socket.connection.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	header := []byte{0x80 | opcode}
	switch {
	case len(payload) <= 125:
		header = append(header, 0x80|byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 0x80|127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(payload)))
		header = append(header, size[:]...)
	}
	mask := [4]byte{1, 2, 3, 4}
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%len(mask)]
	}
	_, err := socket.connection.Write(append(header, masked...))
	return err
}

func (socket *rendererSocket) readApplicationFrame(t *testing.T) rendererFrame {
	t.Helper()
	for {
		opcode, payload := socket.readFrame(t)
		if opcode == 0x9 {
			socket.sendFrame(t, 0xa, payload)
			continue
		}
		if opcode != 0x1 {
			t.Fatalf("renderer application opcode = %#x payload=%x", opcode, payload)
		}
		var frame rendererFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatalf("decode renderer frame %q: %v", payload, err)
		}
		return frame
	}
}

func (socket *rendererSocket) readFrame(t *testing.T) (byte, []byte) {
	t.Helper()
	if err := socket.connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set renderer read deadline: %v", err)
	}
	var header [2]byte
	if _, err := io.ReadFull(socket.reader, header[:]); err != nil {
		t.Fatalf("read renderer frame header: %v", err)
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var encoded [2]byte
		if _, err := io.ReadFull(socket.reader, encoded[:]); err != nil {
			t.Fatalf("read renderer frame length: %v", err)
		}
		length = uint64(binary.BigEndian.Uint16(encoded[:]))
	case 127:
		var encoded [8]byte
		if _, err := io.ReadFull(socket.reader, encoded[:]); err != nil {
			t.Fatalf("read renderer frame length: %v", err)
		}
		length = binary.BigEndian.Uint64(encoded[:])
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(socket.reader, payload); err != nil {
		t.Fatalf("read renderer frame payload: %v", err)
	}
	return header[0] & 0x0f, payload
}
