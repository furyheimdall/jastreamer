package library

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func readID3Metadata(file *os.File, size int64) (storedMetadata, []byte, string, error) {
	result := storedMetadata{Genres: []string{}}
	var artwork []byte
	var artworkMIME string

	if size >= 10 {
		var header [10]byte
		if _, err := file.ReadAt(header[:], 0); err == nil && string(header[:3]) == "ID3" {
			if header[3] < 2 || header[3] > 4 {
				return result, nil, "", errors.New("unsupported ID3 version")
			}
			tagSize, ok := decodeSynchsafe32(header[6:10])
			if !ok || tagSize > maximumTagBytes || int64(tagSize) > size-10 {
				return result, nil, "", errors.New("invalid ID3 tag size")
			}
			data := make([]byte, tagSize)
			if _, err := file.ReadAt(data, 10); err != nil {
				return result, nil, "", err
			}
			if header[3] == 4 && header[5]&0x10 != 0 {
				if len(data) < 10 || string(data[len(data)-10:len(data)-7]) != "3DI" {
					return result, nil, "", errors.New("invalid ID3 footer")
				}
				data = data[:len(data)-10]
			}
			var err error
			result, artwork, artworkMIME, err = parseID3v2(data, int(header[3]), header[5])
			if err != nil {
				return storedMetadata{Genres: []string{}}, nil, "", err
			}
		}
	}

	id3v1 := readID3v1(file, size)
	mergeMetadata(&result, id3v1)
	return result, artwork, artworkMIME, nil
}

func parseID3v2(data []byte, version int, tagFlags byte) (storedMetadata, []byte, string, error) {
	result := storedMetadata{Genres: []string{}}
	offset := 0
	if tagFlags&0x40 != 0 {
		if len(data) < 4 {
			return result, nil, "", errors.New("truncated ID3 extended header")
		}
		var skip int
		if version == 3 {
			extended := int(binary.BigEndian.Uint32(data[:4]))
			if extended < 0 || extended > len(data)-4 {
				return result, nil, "", errors.New("invalid ID3 extended header")
			}
			skip = 4 + extended
		} else if version == 4 {
			extended, ok := decodeSynchsafe32(data[:4])
			if !ok || extended < 4 || extended > len(data) {
				return result, nil, "", errors.New("invalid ID3 extended header")
			}
			skip = extended
		}
		offset = skip
	}

	var artwork []byte
	var artworkMIME string
	for entries := 0; offset < len(data) && entries < maximumMetadataEntries; entries++ {
		name, payload, frameFlags, next, ok := nextID3Frame(data, offset, version)
		if !ok {
			if allZero(data[offset:]) {
				break
			}
			return result, nil, "", errors.New("invalid ID3 frame bounds")
		}
		offset = next
		if name == "" {
			break
		}
		if id3FrameUnsupported(version, frameFlags) {
			continue
		}
		var valid bool
		payload, valid = prepareID3Payload(payload, version, frameFlags)
		if !valid {
			return result, artwork, artworkMIME, errors.New("invalid ID3 frame prefix")
		}
		if tagFlags&0x80 != 0 || version == 4 && frameFlags&0x0002 != 0 {
			payload = removeID3Unsynchronisation(payload)
		}
		switch canonicalID3Frame(name) {
		case "title":
			result.Title = firstNonempty(result.Title, normalizeDisplay(decodeID3TextFrame(payload)))
		case "artist":
			result.Artist = firstNonempty(result.Artist, normalizeDisplay(decodeID3TextFrame(payload)))
		case "album":
			result.Album = firstNonempty(result.Album, normalizeDisplay(decodeID3TextFrame(payload)))
		case "albumartist":
			result.AlbumArtist = firstNonempty(result.AlbumArtist, normalizeDisplay(decodeID3TextFrame(payload)))
		case "track":
			if result.Track == 0 {
				result.Track = positiveNumber(decodeID3TextFrame(payload))
			}
		case "disc":
			if result.Disc == 0 {
				result.Disc = positiveNumber(decodeID3TextFrame(payload))
			}
		case "genre":
			result.Genres = appendSplitValues(result.Genres, decodeID3TextFrame(payload))
		case "picture":
			if len(artwork) == 0 {
				picture, mime := parseID3Picture(payload, version)
				if len(picture) > 0 {
					artwork = picture
					artworkMIME = mime
				}
			}
		}
	}
	if offset < len(data) && !allZero(data[offset:]) {
		return result, artwork, artworkMIME, errors.New("too many ID3 frames")
	}
	result.Genres = normalizedValues(result.Genres)
	return result, artwork, artworkMIME, nil
}

func nextID3Frame(data []byte, offset, version int) (string, []byte, uint16, int, bool) {
	headerSize := 10
	nameSize := 4
	if version == 2 {
		headerSize = 6
		nameSize = 3
	}
	if offset < 0 || offset > len(data)-headerSize {
		return "", nil, 0, offset, false
	}
	header := data[offset : offset+headerSize]
	if allZero(header[:nameSize]) {
		return "", nil, 0, len(data), true
	}
	for _, character := range header[:nameSize] {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return "", nil, 0, offset, false
		}
	}
	var length int
	var flags uint16
	if version == 2 {
		length = int(header[3])<<16 | int(header[4])<<8 | int(header[5])
	} else if version == 3 {
		length = int(binary.BigEndian.Uint32(header[4:8]))
		flags = binary.BigEndian.Uint16(header[8:10])
	} else {
		var ok bool
		length, ok = decodeSynchsafe32(header[4:8])
		if !ok {
			return "", nil, 0, offset, false
		}
		flags = binary.BigEndian.Uint16(header[8:10])
	}
	payloadOffset := offset + headerSize
	if length < 0 || length > maximumTagBytes || length > len(data)-payloadOffset {
		return "", nil, 0, offset, false
	}
	return string(header[:nameSize]), data[payloadOffset : payloadOffset+length], flags, payloadOffset + length, true
}

func decodeSynchsafe32(data []byte) (int, bool) {
	if len(data) != 4 || (data[0]|data[1]|data[2]|data[3])&0x80 != 0 {
		return 0, false
	}
	return int(data[0])<<21 | int(data[1])<<14 | int(data[2])<<7 | int(data[3]), true
}
func id3FrameUnsupported(version int, flags uint16) bool {
	if version == 3 {
		return flags&0x00c0 != 0
	}
	return version == 4 && flags&0x000c != 0
}

func prepareID3Payload(payload []byte, version int, flags uint16) ([]byte, bool) {
	if version == 3 && flags&0x0020 != 0 || version == 4 && flags&0x0040 != 0 {
		if len(payload) < 1 {
			return nil, false
		}
		payload = payload[1:]
	}
	if version == 4 && flags&0x0001 != 0 {
		if len(payload) < 4 {
			return nil, false
		}
		if _, ok := decodeSynchsafe32(payload[:4]); !ok {
			return nil, false
		}
		payload = payload[4:]
	}
	return payload, true
}

func canonicalID3Frame(name string) string {
	switch name {
	case "TIT2", "TT2":
		return "title"
	case "TPE1", "TP1":
		return "artist"
	case "TALB", "TAL":
		return "album"
	case "TPE2", "TP2":
		return "albumartist"
	case "TRCK", "TRK":
		return "track"
	case "TPOS", "TPA":
		return "disc"
	case "TCON", "TCO":
		return "genre"
	case "APIC", "PIC":
		return "picture"
	default:
		return ""
	}
}

func decodeID3TextFrame(data []byte) string {
	if len(data) < 1 {
		return ""
	}
	return strings.TrimRight(decodeID3String(data[0], data[1:]), "\x00")
}

func decodeID3String(encoding byte, data []byte) string {
	switch encoding {
	case 0:
		runes := make([]rune, len(data))
		for index, value := range data {
			runes[index] = rune(value)
		}
		return string(runes)
	case 1, 2:
		var order binary.ByteOrder = binary.BigEndian
		if encoding == 1 && len(data) >= 2 {
			if data[0] == 0xff && data[1] == 0xfe {
				order = binary.LittleEndian
				data = data[2:]
			} else if data[0] == 0xfe && data[1] == 0xff {
				data = data[2:]
			}
		}
		data = data[:len(data)-len(data)%2]
		values := make([]uint16, len(data)/2)
		for index := range values {
			values[index] = order.Uint16(data[index*2 : index*2+2])
		}
		return string(utf16.Decode(values))
	case 3:
		if !utf8.Valid(data) {
			return strings.ToValidUTF8(string(data), "")
		}
		return string(data)
	default:
		return ""
	}
}

func parseID3Picture(data []byte, version int) ([]byte, string) {
	if len(data) < 5 {
		return nil, ""
	}
	encoding := data[0]
	offset := 1
	var declaredMIME string
	if version == 2 {
		if len(data) < offset+3 {
			return nil, ""
		}
		declaredMIME = "image/" + strings.ToLower(string(data[offset:offset+3]))
		offset += 3
	} else {
		end := bytes.IndexByte(data[offset:], 0)
		if end < 0 || end > 255 {
			return nil, ""
		}
		declaredMIME = string(data[offset : offset+end])
		offset += end + 1
	}
	if offset >= len(data) {
		return nil, ""
	}
	offset++ // picture type
	descriptionEnd := id3Terminator(data[offset:], encoding)
	if descriptionEnd < 0 || descriptionEnd > 1<<20 {
		return nil, ""
	}
	offset += descriptionEnd
	if encoding == 1 || encoding == 2 {
		offset += 2
	} else {
		offset++
	}
	if offset > len(data) || len(data)-offset == 0 || len(data)-offset > maximumArtworkBytes {
		return nil, ""
	}
	mime := normalizeImageMIME(declaredMIME, data[offset:])
	if mime == "" {
		return nil, ""
	}
	return append([]byte(nil), data[offset:]...), mime
}

func id3Terminator(data []byte, encoding byte) int {
	if encoding == 1 || encoding == 2 {
		for offset := 0; offset+1 < len(data); offset += 2 {
			if data[offset] == 0 && data[offset+1] == 0 {
				return offset
			}
		}
		return -1
	}
	return bytes.IndexByte(data, 0)
}

func removeID3Unsynchronisation(data []byte) []byte {
	if !bytes.Contains(data, []byte{0xff, 0x00}) {
		return data
	}
	result := make([]byte, 0, len(data))
	for index := 0; index < len(data); index++ {
		result = append(result, data[index])
		if data[index] == 0xff && index+1 < len(data) && data[index+1] == 0x00 {
			index++
		}
	}
	return result
}

func readID3v1(file *os.File, size int64) storedMetadata {
	result := storedMetadata{Genres: []string{}}
	if size < 128 {
		return result
	}
	var data [128]byte
	if _, err := file.ReadAt(data[:], size-128); err != nil || string(data[:3]) != "TAG" {
		return result
	}
	clean := func(value []byte) string { return strings.Trim(string(value), "\x00 ") }
	result.Title = normalizeDisplay(clean(data[3:33]))
	result.Artist = normalizeDisplay(clean(data[33:63]))
	result.Album = normalizeDisplay(clean(data[63:93]))
	if data[125] == 0 {
		result.Track = int(data[126])
	}
	return result
}

func firstNonempty(current, candidate string) string {
	if current != "" {
		return current
	}
	return candidate
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
