package library

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	maximumTagBytes        = 32 << 20
	maximumArtworkBytes    = 8 << 20
	maximumMetadataEntries = 4096
	maximumMetadataValues  = 256
)

type mediaInfo struct {
	metadata    storedMetadata
	format      string
	mime        string
	durationMS  int64
	artwork     []byte
	artworkMime string
}

func readMedia(file *os.File, path string, size int64) (mediaInfo, error) {
	format, mime, err := recognizeMedia(file, filepath.Ext(path))
	if err != nil {
		return mediaInfo{}, err
	}
	info := mediaInfo{format: format, mime: mime, metadata: storedMetadata{Genres: []string{}}}
	metadata, artwork, artworkMIME, _ := readEmbeddedMetadata(file, format, size)
	info.metadata = metadata
	info.artwork = artwork
	info.artworkMime = artworkMIME
	if info.metadata.Title == "" {
		info.metadata.Title = normalizeDisplay(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if format == "wav" {
		wavMetadata, duration, parseErr := parseWAV(file)
		if parseErr != nil {
			return mediaInfo{}, parseErr
		}
		mergeMetadata(&info.metadata, wavMetadata)
		info.durationMS = duration
	} else {
		info.durationMS = mediaDuration(file, format, size)
	}
	return info, nil
}

func recognizeMedia(file *os.File, extension string) (string, string, error) {
	var header [64 * 1024]byte
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	count, err := file.Read(header[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	data := header[:count]
	extension = strings.ToLower(extension)
	switch extension {
	case ".flac":
		if bytes.HasPrefix(data, []byte("fLaC")) {
			return "flac", "audio/flac", nil
		}
	case ".mp3":
		if bytes.HasPrefix(data, []byte("ID3")) || findMP3Frame(data, 0) >= 0 {
			return "mp3", "audio/mpeg", nil
		}
	case ".ogg", ".oga", ".opus":
		if bytes.HasPrefix(data, []byte("OggS")) {
			if bytes.Contains(data, []byte("OpusHead")) {
				return "opus", "audio/ogg", nil
			}
			if bytes.Contains(data, []byte("\x01vorbis")) {
				return "ogg", "audio/ogg", nil
			}
		}
	case ".wav", ".wave":
		if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
			return "wav", "audio/wav", nil
		}
	case ".m4a":
		if len(data) >= 12 && string(data[4:8]) == "ftyp" {
			return "m4a", "audio/mp4", nil
		}
	}
	return "", "", errors.New("unsupported or malformed audio file")
}

func isSupportedPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac", ".mp3", ".ogg", ".oga", ".opus", ".wav", ".wave", ".m4a":
		return true
	default:
		return false
	}
}

func readEmbeddedMetadata(file *os.File, format string, size int64) (storedMetadata, []byte, string, error) {
	switch format {
	case "flac":
		return readFLACMetadata(file, size)
	case "mp3":
		return readID3Metadata(file, size)
	case "ogg", "opus":
		metadata, picture, mime := readOggMetadata(file, size)
		return metadata, picture, mime, nil
	case "m4a":
		return readMP4Metadata(file, size)
	default:
		return storedMetadata{Genres: []string{}}, nil, "", nil
	}
}

func normalizedValues(values []string) []string {
	unique := make(map[string]string)
	for _, value := range values {
		display := normalizeDisplay(value)
		key := normalizeSearch(display)
		if key != "" {
			if previous, ok := unique[key]; !ok || display < previous {
				unique[key] = display
				if len(unique) >= maximumMetadataValues {
					break
				}
			}
		}
	}
	result := make([]string, 0, len(unique))
	for _, value := range unique {
		result = append(result, value)
	}
	slices.SortFunc(result, func(a, b string) int { return strings.Compare(normalizeSearch(a), normalizeSearch(b)) })
	return result
}

func splitValues(value string) []string {
	result := make([]string, 0, 4)
	start := 0
	for index, character := range value {
		if character != 0 && character != ';' && character != ',' {
			continue
		}
		if index > start {
			result = append(result, value[start:index])
			if len(result) >= maximumMetadataValues {
				return result
			}
		}
		start = index + 1
	}
	if start < len(value) && len(result) < maximumMetadataValues {
		result = append(result, value[start:])
	}
	return result
}
func appendSplitValues(target []string, value string) []string {
	if len(target) >= maximumMetadataValues {
		return target
	}
	values := splitValues(value)
	remaining := maximumMetadataValues - len(target)
	if len(values) > remaining {
		values = values[:remaining]
	}
	return append(target, values...)
}

func normalizeDisplay(value string) string {
	return norm.NFC.String(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
func normalizeSearch(value string) string {
	return cases.Fold().String(norm.NFC.String(strings.TrimSpace(value)))
}
func positiveNumber(value string) int {
	first, _, _ := strings.Cut(strings.TrimSpace(value), "/")
	number, err := strconv.Atoi(first)
	if err != nil || number <= 0 {
		return 0
	}
	return number
}
func mergeMetadata(target *storedMetadata, source storedMetadata) {
	if target.Title == "" {
		target.Title = source.Title
	}
	if target.Artist == "" {
		target.Artist = source.Artist
	}
	if target.Album == "" {
		target.Album = source.Album
	}
	if target.AlbumArtist == "" {
		target.AlbumArtist = source.AlbumArtist
	}
	if target.Disc == 0 {
		target.Disc = source.Disc
	}
	if target.Track == 0 {
		target.Track = source.Track
	}
	if len(target.Genres) == 0 {
		target.Genres = source.Genres
	}
}

func parseWAV(file *os.File) (storedMetadata, int64, error) {
	result := storedMetadata{Genres: []string{}}
	stat, err := file.Stat()
	if err != nil {
		return result, 0, err
	}
	fileSize := stat.Size()
	if fileSize < 12 {
		return result, 0, errors.New("truncated WAV file")
	}
	var byteRate, dataSize int64
	if _, err := file.Seek(12, io.SeekStart); err != nil {
		return result, 0, err
	}
	offset := int64(12)
	for entries := 0; offset <= fileSize-8 && offset < 16<<20; entries++ {
		if entries >= maximumMetadataEntries {
			return result, 0, errors.New("too many WAV chunks")
		}
		var header [8]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return result, 0, err
		}
		size := int64(binary.LittleEndian.Uint32(header[4:]))
		padding := size & 1
		payloadOffset := offset + 8
		if size+padding > fileSize-payloadOffset {
			return result, 0, errors.New("invalid WAV chunk bounds")
		}
		switch string(header[:4]) {
		case "fmt ":
			if size < 16 || size > 1<<20 {
				return result, 0, errors.New("invalid WAV format chunk")
			}
			payload := make([]byte, int(size))
			if _, err := io.ReadFull(file, payload); err != nil {
				return result, 0, err
			}
			byteRate = int64(binary.LittleEndian.Uint32(payload[8:12]))
		case "data":
			dataSize = size
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return result, 0, err
			}
		case "LIST":
			if size > 4<<20 {
				if _, err := file.Seek(size, io.SeekCurrent); err != nil {
					return result, 0, err
				}
				break
			}
			payload := make([]byte, int(size))
			if _, err := io.ReadFull(file, payload); err != nil {
				return result, 0, err
			}
			result = parseWAVInfo(payload)
		default:
			if _, err := file.Seek(size, io.SeekCurrent); err != nil {
				return result, 0, err
			}
		}
		if padding != 0 {
			if _, err := file.Seek(1, io.SeekCurrent); err != nil {
				return result, 0, err
			}
		}
		offset = payloadOffset + size + padding
	}
	if byteRate <= 0 || dataSize <= 0 {
		return result, 0, nil
	}
	return result, dataSize * 1000 / byteRate, nil
}
func parseWAVInfo(payload []byte) storedMetadata {
	result := storedMetadata{Genres: []string{}}
	if len(payload) < 4 || string(payload[:4]) != "INFO" {
		return result
	}
	values := make(map[string]string)
	for offset, entries := 4, 0; offset+8 <= len(payload) && entries < maximumMetadataEntries; entries++ {
		key := string(payload[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(payload[offset+4 : offset+8]))
		offset += 8
		if size < 0 || size > len(payload)-offset {
			break
		}
		switch key {
		case "INAM", "IART", "IPRD", "IGNR", "ITRK":
			values[key] = strings.Trim(string(payload[offset:offset+size]), "\x00 \t\r\n")
		}
		offset += size + size%2
	}
	result.Title = normalizeDisplay(values["INAM"])
	result.Artist = normalizeDisplay(values["IART"])
	result.Album = normalizeDisplay(values["IPRD"])
	result.Genres = normalizedValues([]string{values["IGNR"]})
	result.Track = positiveNumber(values["ITRK"])
	return result
}

func normalizeImageMIME(value string, data []byte) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "image/jpg" {
		value = "image/jpeg"
	}
	if value == "image/jpeg" || value == "image/png" || value == "image/gif" {
		return value
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	if bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")) {
		return "image/gif"
	}
	return ""
}

func mediaParseError(path string, err error) error {
	return fmt.Errorf("read metadata for %s: %w", filepath.Base(path), err)
}
