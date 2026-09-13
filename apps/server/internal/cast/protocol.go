package cast

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	maxFrameSize  = 1 << 20
	maxIdentifier = 1024
	maxNamespace  = 4096
	maxPayload    = 768 << 10
)

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

type castMessage struct {
	source      string
	destination string
	namespace   string
	payload     []byte
}

func marshalCastMessage(message castMessage) ([]byte, error) {
	if !validWireString(message.source, maxIdentifier) || !validWireString(message.destination, maxIdentifier) ||
		!validWireString(message.namespace, maxNamespace) || len(message.payload) == 0 || len(message.payload) > maxPayload || !utf8.Valid(message.payload) {
		return nil, ErrInvalidResponse
	}
	result := make([]byte, 0, len(message.payload)+len(message.source)+len(message.destination)+len(message.namespace)+32)
	result = appendVarintField(result, 1, 0) // CASTV2_1_0
	result = appendBytesField(result, 2, []byte(message.source))
	result = appendBytesField(result, 3, []byte(message.destination))
	result = appendBytesField(result, 4, []byte(message.namespace))
	result = appendVarintField(result, 5, 0) // STRING
	result = appendBytesField(result, 6, message.payload)
	if len(result) > maxFrameSize {
		return nil, ErrInvalidResponse
	}
	return result, nil
}

func unmarshalCastMessage(data []byte) (castMessage, error) {
	if len(data) == 0 || len(data) > maxFrameSize {
		return castMessage{}, ErrInvalidResponse
	}
	var result castMessage
	var protocol, payloadType uint64
	var hasProtocol, hasSource, hasDestination, hasNamespace, hasPayloadType, hasPayload bool
	for offset := 0; offset < len(data); {
		key, next, ok := consumeVarint(data, offset)
		if !ok || key == 0 {
			return castMessage{}, ErrInvalidResponse
		}
		offset = next
		field, wire := int(key>>3), int(key&7)
		switch field {
		case 1, 5:
			if wire != wireVarint {
				return castMessage{}, ErrInvalidResponse
			}
			value, end, valid := consumeVarint(data, offset)
			if !valid {
				return castMessage{}, ErrInvalidResponse
			}
			offset = end
			if field == 1 {
				protocol, hasProtocol = value, true
			} else {
				payloadType, hasPayloadType = value, true
			}
		case 2, 3, 4, 6:
			if wire != wireBytes {
				return castMessage{}, ErrInvalidResponse
			}
			value, end, valid := consumeBytes(data, offset)
			if !valid {
				return castMessage{}, ErrInvalidResponse
			}
			offset = end
			switch field {
			case 2:
				if hasSource || !validWireBytes(value, maxIdentifier) {
					return castMessage{}, ErrInvalidResponse
				}
				result.source, hasSource = string(value), true
			case 3:
				if hasDestination || !validWireBytes(value, maxIdentifier) {
					return castMessage{}, ErrInvalidResponse
				}
				result.destination, hasDestination = string(value), true
			case 4:
				if hasNamespace || !validWireBytes(value, maxNamespace) {
					return castMessage{}, ErrInvalidResponse
				}
				result.namespace, hasNamespace = string(value), true
			case 6:
				if hasPayload || len(value) == 0 || len(value) > maxPayload || !utf8.Valid(value) {
					return castMessage{}, ErrInvalidResponse
				}
				result.payload = append([]byte(nil), value...)
				hasPayload = true
			}
		default:
			end, valid := skipWireValue(data, offset, wire)
			if !valid {
				return castMessage{}, ErrInvalidResponse
			}
			offset = end
		}
	}
	if !hasProtocol || protocol != 0 || !hasSource || !hasDestination || !hasNamespace || !hasPayloadType || payloadType != 0 || !hasPayload {
		return castMessage{}, ErrInvalidResponse
	}
	return result, nil
}

func writeCastMessage(writer io.Writer, message castMessage) error {
	data, err := marshalCastMessage(message)
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, data)
}

func readCastMessage(reader io.Reader) (castMessage, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return castMessage{}, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameSize {
		return castMessage{}, ErrInvalidResponse
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return castMessage{}, err
	}
	return unmarshalCastMessage(data)
}

func appendVarintField(target []byte, field int, value uint64) []byte {
	target = appendVarint(target, uint64(field<<3|wireVarint))
	return appendVarint(target, value)
}

func appendBytesField(target []byte, field int, value []byte) []byte {
	target = appendVarint(target, uint64(field<<3|wireBytes))
	target = appendVarint(target, uint64(len(value)))
	return append(target, value...)
}

func appendVarint(target []byte, value uint64) []byte {
	for value >= 0x80 {
		target = append(target, byte(value)|0x80)
		value >>= 7
	}
	return append(target, byte(value))
}

func consumeVarint(data []byte, offset int) (uint64, int, bool) {
	var value uint64
	for index := 0; index < 10 && offset+index < len(data); index++ {
		current := data[offset+index]
		if index == 9 && current > 1 {
			return 0, 0, false
		}
		value |= uint64(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return value, offset + index + 1, true
		}
	}
	return 0, 0, false
}

func consumeBytes(data []byte, offset int) ([]byte, int, bool) {
	length, start, ok := consumeVarint(data, offset)
	if !ok || length > uint64(len(data)-start) {
		return nil, 0, false
	}
	end := start + int(length)
	return data[start:end], end, true
}

func skipWireValue(data []byte, offset, wire int) (int, bool) {
	switch wire {
	case wireVarint:
		_, end, ok := consumeVarint(data, offset)
		return end, ok
	case wireFixed64:
		if len(data)-offset < 8 {
			return 0, false
		}
		return offset + 8, true
	case wireBytes:
		_, end, ok := consumeBytes(data, offset)
		return end, ok
	case wireFixed32:
		if len(data)-offset < 4 {
			return 0, false
		}
		return offset + 4, true
	default:
		return 0, false
	}
}

func validWireString(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value)
}

func validWireBytes(value []byte, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.Valid(value)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := writer.Write(data)
		if err != nil {
			return err
		}
		if count <= 0 || count > len(data) {
			return errors.New("cast: invalid transport write")
		}
		data = data[count:]
	}
	return nil
}
