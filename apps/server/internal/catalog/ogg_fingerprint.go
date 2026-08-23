package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

func oggAudioFingerprint(reader io.ReadSeeker) (string, error) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek Ogg: %w", err)
	}
	hash := sha256.New()
	packets := make(map[uint32][]byte)
	hasAudioIdentity := false
	for {
		var header [27]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("read Ogg page header: %w", err)
		}
		if !bytes.Equal(header[:4], []byte("OggS")) {
			return "", fmt.Errorf("invalid Ogg page: %w", ErrMalformedMedia)
		}
		serial := binary.LittleEndian.Uint32(header[14:18])
		segments := make([]byte, int(header[26]))
		if _, err := io.ReadFull(reader, segments); err != nil {
			return "", fmt.Errorf("read Ogg lacing: %w", err)
		}
		size := 0
		for _, segment := range segments {
			size += int(segment)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return "", fmt.Errorf("read Ogg payload: %w", err)
		}
		offset := 0
		packet := packets[serial]
		for _, segment := range segments {
			end := offset + int(segment)
			packet = append(packet, payload[offset:end]...)
			offset = end
			if segment < 255 {
				if !bytes.HasPrefix(packet, []byte("\x03vorbis")) && !bytes.HasPrefix(packet, []byte("OpusTags")) {
					var length [8]byte
					binary.LittleEndian.PutUint64(length[:], uint64(len(packet)))
					if _, err := hash.Write(length[:]); err != nil {
						return "", fmt.Errorf("hash Ogg packet length: %w", err)
					}
					if _, err := hash.Write(packet); err != nil {
						return "", fmt.Errorf("hash Ogg packet: %w", err)
					}
					hasAudioIdentity = true
				}
				packet = nil
			}
		}
		packets[serial] = packet
	}
	if !hasAudioIdentity {
		return "", fmt.Errorf("Ogg has no codec packets: %w", ErrMalformedMedia)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
