package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
	"golang.org/x/text/unicode/norm"
)

var ErrMalformedMedia = errors.New("catalog: malformed media")

func parseMedia(path string, reader io.ReadSeeker) (MediaSnapshot, error) {
	format, err := recognizeFormat(filepath.Ext(path), reader)
	if err != nil {
		return MediaSnapshot{}, err
	}
	contentFingerprint, err := hashReader(reader)
	if err != nil {
		return MediaSnapshot{}, fmt.Errorf("content fingerprint: %w", err)
	}
	switch format {
	case FormatPCMWAV:
		metadata, audioFingerprint, parseErr := parsePCMWAV(reader)
		if parseErr != nil {
			return MediaSnapshot{}, parseErr
		}
		return MediaSnapshot{Format: format, Metadata: metadata, ContentFingerprint: contentFingerprint, AudioFingerprint: audioFingerprint}, nil
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return MediaSnapshot{}, fmt.Errorf("seek metadata: %w", err)
	}
	parsed, err := tag.ReadFrom(reader)
	if err != nil {
		if !errors.Is(err, tag.ErrNoTagsFound) {
			return MediaSnapshot{}, fmt.Errorf("parse tags: %w: %w", err, ErrMalformedMedia)
		}
		return MediaSnapshot{Format: format, ContentFingerprint: contentFingerprint, AudioFingerprint: contentFingerprint}, nil
	}
	audioFingerprint := contentFingerprint
	switch format {
	case FormatFLAC, FormatMP3:
		if _, err := reader.Seek(0, io.SeekStart); err != nil {
			return MediaSnapshot{}, fmt.Errorf("seek audio fingerprint: %w", err)
		}
		audioFingerprint, err = tag.Sum(reader)
		if err != nil {
			return MediaSnapshot{}, fmt.Errorf("audio fingerprint: %w", err)
		}
	case FormatOggVorbis, FormatOpus:
		audioFingerprint, err = oggAudioFingerprint(reader)
		if err != nil {
			return MediaSnapshot{}, fmt.Errorf("audio fingerprint: %w", err)
		}
	}
	return MediaSnapshot{
		Format:             format,
		Metadata:           metadataFromTag(parsed),
		ContentFingerprint: contentFingerprint,
		AudioFingerprint:   audioFingerprint,
	}, nil
}

func recognizeFormat(extension string, reader io.ReadSeeker) (Format, error) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek signature: %w", err)
	}
	header := make([]byte, 64*1024)
	count, err := reader.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read signature: %w", err)
	}
	header = header[:count]
	extension = strings.ToLower(extension)
	switch extension {
	case ".flac":
		if bytes.HasPrefix(header, []byte("fLaC")) {
			return FormatFLAC, nil
		}
	case ".mp3":
		if bytes.HasPrefix(header, []byte("ID3")) || (len(header) >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0) {
			return FormatMP3, nil
		}
	case ".ogg", ".oga":
		if bytes.HasPrefix(header, []byte("OggS")) && bytes.Contains(header, []byte("vorbis")) {
			return FormatOggVorbis, nil
		}
	case ".opus":
		if bytes.HasPrefix(header, []byte("OggS")) && bytes.Contains(header, []byte("OpusHead")) {
			return FormatOpus, nil
		}
	case ".wav", ".wave":
		if len(header) >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE")) {
			return FormatPCMWAV, nil
		}
	default:
		return "", fmt.Errorf("extension %q: %w", extension, ErrMalformedMedia)
	}
	return "", fmt.Errorf("signature for %q: %w", extension, ErrMalformedMedia)
}

func metadataFromTag(parsed tag.Metadata) Metadata {
	track, _ := parsed.Track()
	disc, _ := parsed.Disc()
	raw := extractRawTags(parsed)
	if track <= 0 {
		track = positiveNumber(raw.first("TRACKNUMBER"))
	}
	if disc <= 0 {
		disc = positiveNumber(raw.first("DISCNUMBER"))
	}
	genres := append([]string{parsed.Genre()}, raw.all("GENRE", "GENRES")...)
	return Metadata{
		Title:       normalizeDisplay(parsed.Title()),
		Album:       normalizeDisplay(parsed.Album()),
		AlbumArtist: normalizeDisplay(parsed.AlbumArtist()),
		Artist:      normalizeDisplay(parsed.Artist()),
		RecordingID: normalizeEmbeddedID(raw.first("MUSICBRAINZTRACKID", "MUSICBRAINZRECORDINGID")),
		ReleaseID:   normalizeEmbeddedID(raw.first("MUSICBRAINZRELEASEID")),
		Disc:        disc,
		Track:       track,
		Genres:      normalizedTagValues(genres),
		Styles:      raw.all("STYLE", "STYLES", "SUBGENRE"),
		Moods:       raw.all("MOOD", "MOODS"),
		LocalTags:   raw.all("TAG", "TAGS", "LOCALTAG", "LOCALTAGS", "GROUPING"),
	}
}

func hashReader(reader io.ReadSeeker) (string, error) {
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isSupportedPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac", ".mp3", ".ogg", ".oga", ".opus", ".wav", ".wave":
		return true
	default:
		return false
	}
}

func positiveNumber(value string) int {
	first, _, _ := strings.Cut(strings.TrimSpace(value), "/")
	number, err := strconv.Atoi(first)
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

func normalizeDisplay(value string) string {
	return norm.NFC.String(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func normalizeEmbeddedID(value string) string {
	return strings.ToLower(strings.Trim(value, "\x00 \t\r\n"))
}
