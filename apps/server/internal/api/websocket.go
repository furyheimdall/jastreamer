package api

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	websocketGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxWebSocketMessage = 64 << 10
	websocketPingPeriod = 20 * time.Second
	websocketPongWait   = 60 * time.Second

	opcodeContinuation byte = 0x0
	opcodeText         byte = 0x1
	opcodeBinary       byte = 0x2
	opcodeClose        byte = 0x8
	opcodePing         byte = 0x9
	opcodePong         byte = 0xa

	closeNormal          uint16 = 1000
	closeGoingAway       uint16 = 1001
	closeUnsupportedData uint16 = 1003
	closeProtocolError   uint16 = 1002
	closeInvalidPayload  uint16 = 1007
	closeTryAgainLater   uint16 = 1013
	closeMessageTooBig   uint16 = 1009
	closePolicyViolation uint16 = 1008
)

type websocketCloseError struct {
	code   uint16
	reason string
	peer   bool
}

func (err *websocketCloseError) Error() string {
	return fmt.Sprintf("websocket close %d: %s", err.code, err.reason)
}

type frameReader struct{ reader *bufio.Reader }

func newFrameReader(reader *bufio.Reader) *frameReader { return &frameReader{reader: reader} }

func (reader *frameReader) readMessage(control func(byte, []byte) error) ([]byte, error) {
	message := make([]byte, 0, 256)
	fragmented := false
	for {
		final, opcode, payload, err := reader.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case opcodeClose:
			code := closeNormal
			if len(payload) == 1 {
				return nil, &websocketCloseError{code: closeProtocolError, reason: "invalid close payload"}
			}
			if len(payload) >= 2 {
				code = binary.BigEndian.Uint16(payload[:2])
			}
			return nil, &websocketCloseError{code: code, peer: true}
		case opcodePing, opcodePong:
			if err := control(opcode, payload); err != nil {
				return nil, err
			}
			continue
		case opcodeText:
			if fragmented {
				return nil, &websocketCloseError{code: closeProtocolError, reason: "new data frame during fragmentation"}
			}
			message = append(message, payload...)
			fragmented = !final
		case opcodeContinuation:
			if !fragmented {
				return nil, &websocketCloseError{code: closeProtocolError, reason: "unexpected continuation"}
			}
			message = append(message, payload...)
			fragmented = !final
		case opcodeBinary:
			return nil, &websocketCloseError{code: closeUnsupportedData, reason: "binary messages are unsupported"}
		default:
			return nil, &websocketCloseError{code: closeProtocolError, reason: "reserved opcode"}
		}
		if len(message) > maxWebSocketMessage {
			return nil, &websocketCloseError{code: closeMessageTooBig, reason: "message exceeds 64 KiB"}
		}
		if !fragmented {
			if !utf8.Valid(message) {
				return nil, &websocketCloseError{code: closeInvalidPayload, reason: "text is not UTF-8"}
			}
			return message, nil
		}
	}
}

func (reader *frameReader) readFrame() (bool, byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader.reader, header); err != nil {
		return false, 0, nil, err
	}
	final := header[0]&0x80 != 0
	if header[0]&0x70 != 0 || header[1]&0x80 == 0 {
		return false, 0, nil, &websocketCloseError{code: closeProtocolError, reason: "invalid frame flags or missing mask"}
	}
	opcode := header[0] & 0x0f
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var encoded [2]byte
		if _, err := io.ReadFull(reader.reader, encoded[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(encoded[:]))
	case 127:
		var encoded [8]byte
		if _, err := io.ReadFull(reader.reader, encoded[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(encoded[:])
		if length>>63 != 0 {
			return false, 0, nil, &websocketCloseError{code: closeProtocolError, reason: "invalid frame length"}
		}
	}
	control := opcode >= opcodeClose
	if (control && (!final || length > 125)) || length > maxWebSocketMessage {
		code := closeProtocolError
		if !control && length > maxWebSocketMessage {
			code = closeMessageTooBig
		}
		return false, 0, nil, &websocketCloseError{code: code, reason: "invalid frame length"}
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader.reader, mask[:]); err != nil {
		return false, 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader.reader, payload); err != nil {
		return false, 0, nil, err
	}
	for index := range payload {
		payload[index] ^= mask[index%len(mask)]
	}
	return final, opcode, payload, nil
}

func writeFrame(writer *bufio.Writer, opcode byte, payload []byte) error {
	if len(payload) > maxWebSocketMessage || (opcode >= opcodeClose && len(payload) > 125) {
		return &websocketCloseError{code: closeMessageTooBig, reason: "outgoing frame too large"}
	}
	if err := writer.WriteByte(0x80 | opcode); err != nil {
		return err
	}
	switch {
	case len(payload) <= 125:
		if err := writer.WriteByte(byte(len(payload))); err != nil {
			return err
		}
	case len(payload) <= 65535:
		if err := writer.WriteByte(126); err != nil {
			return err
		}
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], uint16(len(payload)))
		if _, err := writer.Write(encoded[:]); err != nil {
			return err
		}
	default:
		if err := writer.WriteByte(127); err != nil {
			return err
		}
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(len(payload)))
		if _, err := writer.Write(encoded[:]); err != nil {
			return err
		}
	}
	_, err := writer.Write(payload)
	return err
}

func writeClose(writer *bufio.Writer, code uint16, reason string) error {
	payload := make([]byte, 2, 125)
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, reason...)
	if len(payload) > 125 {
		payload = payload[:125]
	}
	if err := writeFrame(writer, opcodeClose, payload); err != nil {
		return err
	}
	return writer.Flush()
}

func (service *server) writeEvent(writer *bufio.Writer, event eventEnvelope) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := writeFrame(writer, opcodeText, payload); err != nil {
		return err
	}
	return writer.Flush()
}

func (service *server) writeResync(writer *bufio.Writer, epoch, sequence uint64) {
	_ = service.writeEvent(writer, eventEnvelope{Type: eventTypeResyncRequired, Epoch: epoch, Sequence: sequence})
	_ = writeClose(writer, closeTryAgainLater, "resync required")
}

func (service *server) originAllowed(request *http.Request, origin string) bool {
	return origin == "https://"+request.Host || origin == "http://"+request.Host || slices.Contains(service.config.AllowedOrigins, origin)
}

func websocketKey(request *http.Request) (string, bool) {
	if request.Header.Get("Sec-WebSocket-Version") != "13" {
		return "", false
	}
	key := request.Header.Get("Sec-WebSocket-Key")
	decoded, err := base64.StdEncoding.DecodeString(key)
	return key, err == nil && len(decoded) == 16
}

func headerContains(header http.Header, name, token string) bool {
	for part := range strings.SplitSeq(header.Get(name), ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
