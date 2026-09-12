package library

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"strings"
)

const maximumEncodedArtworkBytes = (maximumArtworkBytes+maximumTagStringBytes+514)/3*4 + 255

func readOggMetadata(file *os.File, size int64) (storedMetadata, []byte, string) {
	result := storedMetadata{Genres: []string{}}
	packets := oggPackets(file, size, 3)
	for _, packet := range packets {
		var payload []byte
		switch {
		case bytes.HasPrefix(packet, []byte("OpusTags")):
			payload = packet[8:]
		case bytes.HasPrefix(packet, []byte("\x03vorbis")):
			payload = packet[7:]
		default:
			continue
		}
		comments, err := parseVorbisComments(payload)
		if err != nil {
			return result, nil, ""
		}
		result = metadataFromVorbisComments(comments)
		picture, mime := vorbisPicture(comments)
		return result, picture, mime
	}
	return result, nil, ""
}

func oggPackets(file *os.File, size int64, maximum int) [][]byte {
	packets := make([][]byte, 0, maximum)
	current := make([]byte, 0)
	offset := int64(0)
	totalRead := int64(0)
	for pages := 0; pages < maximumMetadataEntries && offset <= size-27 && len(packets) < maximum; pages++ {
		var header [27]byte
		if _, err := file.ReadAt(header[:], offset); err != nil || string(header[:4]) != "OggS" || header[4] != 0 {
			break
		}
		if header[5]&1 != 0 && len(current) == 0 || header[5]&1 == 0 && len(current) > 0 {
			break
		}
		segmentCount := int(header[26])
		segmentTable := make([]byte, segmentCount)
		if _, err := file.ReadAt(segmentTable, offset+27); err != nil {
			break
		}
		payloadLength := 0
		for _, length := range segmentTable {
			payloadLength += int(length)
		}
		payloadOffset := offset + 27 + int64(segmentCount)
		pageLength := int64(27 + segmentCount + payloadLength)
		if payloadOffset > size || int64(payloadLength) > size-payloadOffset || totalRead > maximumTagBytes-pageLength {
			break
		}
		pageData := make([]byte, payloadLength)
		if _, err := file.ReadAt(pageData, payloadOffset); err != nil {
			break
		}
		totalRead += pageLength
		payloadPosition := 0
		for _, lengthByte := range segmentTable {
			length := int(lengthByte)
			if len(current) > maximumTagBytes-length {
				return packets
			}
			current = append(current, pageData[payloadPosition:payloadPosition+length]...)
			payloadPosition += length
			if length < 255 {
				packets = append(packets, current)
				current = nil
				if len(packets) >= maximum {
					break
				}
			}
		}
		offset += pageLength
	}
	return packets
}

func parseVorbisComments(payload []byte) (map[string][]string, error) {
	result := make(map[string][]string)
	if len(payload) < 8 {
		return result, errors.New("truncated Vorbis comments")
	}
	vendorLength := int64(binary.LittleEndian.Uint32(payload[:4]))
	if vendorLength > maximumTagStringBytes || vendorLength > int64(len(payload)-8) {
		return result, errors.New("invalid Vorbis vendor length")
	}
	offset := 4 + int(vendorLength)
	count := int64(binary.LittleEndian.Uint32(payload[offset : offset+4]))
	offset += 4
	if count > maximumTagCommentCount || count > int64((len(payload)-offset)/4) {
		return result, errors.New("invalid Vorbis comment count")
	}
	for range count {
		length := int64(binary.LittleEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		if length > int64(len(payload)-offset) {
			return result, errors.New("invalid Vorbis comment length")
		}
		commentBytes := payload[offset : offset+int(length)]
		offset += int(length)
		keyEnd := bytes.IndexByte(commentBytes, '=')
		if keyEnd <= 0 || keyEnd > 255 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(string(commentBytes[:keyEnd])))
		maximumLength := int64(maximumTagStringBytes)
		if key == "METADATA_BLOCK_PICTURE" || key == "COVERART" {
			maximumLength = maximumEncodedArtworkBytes
		}
		if length > maximumLength {
			return result, errors.New("Vorbis comment is too large")
		}
		result[key] = append(result[key], string(commentBytes[keyEnd+1:]))
	}
	return result, nil
}

func metadataFromVorbisComments(comments map[string][]string) storedMetadata {
	result := storedMetadata{
		Title:       normalizeDisplay(firstComment(comments, "TITLE")),
		Artist:      normalizeDisplay(firstComment(comments, "ARTIST")),
		Album:       normalizeDisplay(firstComment(comments, "ALBUM")),
		AlbumArtist: normalizeDisplay(firstComment(comments, "ALBUMARTIST", "ALBUM ARTIST")),
		Track:       positiveNumber(firstComment(comments, "TRACKNUMBER")),
		Disc:        positiveNumber(firstComment(comments, "DISCNUMBER")),
		Genres:      []string{},
	}
	for _, value := range comments["GENRE"] {
		result.Genres = appendSplitValues(result.Genres, value)
	}
	result.Genres = normalizedValues(result.Genres)
	return result
}
func firstComment(comments map[string][]string, keys ...string) string {
	for _, key := range keys {
		if values := comments[key]; len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func vorbisPicture(comments map[string][]string) ([]byte, string) {
	if values := comments["METADATA_BLOCK_PICTURE"]; len(values) > 0 {
		encoded := strings.TrimSpace(values[0])
		if base64.StdEncoding.DecodedLen(len(encoded)) <= maximumArtworkBytes+maximumTagStringBytes+512 {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil {
				if picture, mime := parseFLACPicture(decoded); len(picture) > 0 {
					return picture, mime
				}
			}
		}
	}
	if values := comments["COVERART"]; len(values) > 0 {
		encoded := strings.TrimSpace(values[0])
		if base64.StdEncoding.DecodedLen(len(encoded)) <= maximumArtworkBytes {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil {
				mime := firstComment(comments, "COVERARTMIME")
				mime = normalizeImageMIME(mime, decoded)
				if mime != "" {
					return decoded, mime
				}
			}
		}
	}
	return nil, ""
}
func parseFLACPicture(data []byte) ([]byte, string) {
	if len(data) < 32 {
		return nil, ""
	}
	offset := 4
	mimeLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if mimeLength < 0 || offset+mimeLength+4 > len(data) {
		return nil, ""
	}
	mime := string(data[offset : offset+mimeLength])
	offset += mimeLength
	descriptionLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if descriptionLength < 0 || offset+descriptionLength+20 > len(data) {
		return nil, ""
	}
	offset += descriptionLength + 16
	pictureLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if pictureLength <= 0 || pictureLength > maximumArtworkBytes || offset+pictureLength > len(data) {
		return nil, ""
	}
	picture := data[offset : offset+pictureLength]
	mime = normalizeImageMIME(mime, picture)
	if mime == "" {
		return nil, ""
	}
	return picture, mime
}
