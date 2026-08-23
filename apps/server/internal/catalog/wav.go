package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const maximumMetadataChunk = 4 << 20

func parsePCMWAV(reader io.ReadSeeker) (Metadata, string, error) {
	if _, err := reader.Seek(12, io.SeekStart); err != nil {
		return Metadata{}, "", fmt.Errorf("seek WAV chunks: %w", err)
	}
	var (
		metadata   Metadata
		foundPCM   bool
		foundAudio bool
		audioHash  = sha256.New()
	)
	for {
		var header [8]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return Metadata{}, "", fmt.Errorf("read WAV chunk header: %w", err)
		}
		size := int64(binary.LittleEndian.Uint32(header[4:]))
		switch string(header[:4]) {
		case "fmt ":
			if size < 16 || size > maximumMetadataChunk {
				return Metadata{}, "", fmt.Errorf("invalid WAV fmt size %d: %w", size, ErrMalformedMedia)
			}
			payload := make([]byte, size)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return Metadata{}, "", fmt.Errorf("read WAV fmt: %w", err)
			}
			foundPCM = binary.LittleEndian.Uint16(payload) == 1
		case "LIST":
			if size < 4 || size > maximumMetadataChunk {
				return Metadata{}, "", fmt.Errorf("invalid WAV LIST size %d: %w", size, ErrMalformedMedia)
			}
			payload := make([]byte, size)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return Metadata{}, "", fmt.Errorf("read WAV LIST: %w", err)
			}
			metadata = parseWAVInfo(payload)
		case "data":
			foundAudio = true
			if _, err := io.CopyN(audioHash, reader, size); err != nil {
				return Metadata{}, "", fmt.Errorf("read WAV PCM: %w", err)
			}
		default:
			if _, err := reader.Seek(size, io.SeekCurrent); err != nil {
				return Metadata{}, "", fmt.Errorf("skip WAV chunk: %w", err)
			}
		}
		if size%2 != 0 {
			if _, err := reader.Seek(1, io.SeekCurrent); err != nil {
				return Metadata{}, "", fmt.Errorf("skip WAV padding: %w", err)
			}
		}
	}
	if !foundPCM || !foundAudio {
		return Metadata{}, "", fmt.Errorf("WAV is not PCM: %w", ErrMalformedMedia)
	}
	return metadata, hex.EncodeToString(audioHash.Sum(nil)), nil
}

func parseWAVInfo(payload []byte) Metadata {
	if len(payload) < 4 || string(payload[:4]) != "INFO" {
		return Metadata{}
	}
	values := make(map[string]string)
	for offset := 4; offset+8 <= len(payload); {
		key := string(payload[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(payload[offset+4 : offset+8]))
		start, end := offset+8, offset+8+size
		if size < 0 || end > len(payload) {
			break
		}
		values[key] = strings.TrimRight(string(payload[start:end]), "\x00 ")
		offset = end + size%2
	}
	return Metadata{
		Title:  normalizeDisplay(values["INAM"]),
		Album:  normalizeDisplay(values["IPRD"]),
		Artist: normalizeDisplay(values["IART"]),
		Track:  positiveNumber(values["IPRT"]),
	}
}
