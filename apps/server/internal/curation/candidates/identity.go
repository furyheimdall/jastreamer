package candidates

import (
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var fold = cases.Fold()

func RecordingKeyFor(track catalog.Track) RecordingKey {
	return recordingKey(track)
}

func recordingKey(track catalog.Track) RecordingKey {
	if track.RecordingID != "" {
		return RecordingKey("recording:" + string(track.RecordingID))
	}
	if track.AudioFingerprint != "" {
		return RecordingKey("fingerprint:" + track.AudioFingerprint)
	}
	if track.Fingerprint != "" {
		return RecordingKey("fingerprint:" + track.Fingerprint)
	}
	return RecordingKey("track:" + string(track.TrackID))
}

func normalize(value string) string {
	return fold.String(norm.NFC.String(strings.Join(strings.Fields(strings.TrimSpace(value)), " ")))
}

func normalizedPath(value string) string {
	return normalize(strings.ReplaceAll(value, "\\", "/"))
}

func sharesPrimaryArtist(left, right string) bool {
	left, right = normalize(left), normalize(right)
	return left != "" && left == right && left != "various artists"
}

func supportedFormat(value catalog.Format) bool {
	switch value {
	case catalog.FormatFLAC, catalog.FormatMP3, catalog.FormatOggVorbis, catalog.FormatOpus, catalog.FormatPCMWAV:
		return true
	default:
		return false
	}
}
