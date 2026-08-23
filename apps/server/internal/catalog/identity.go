package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var identifierFold = cases.Fold()

type identitySet struct {
	FileID      FileID
	TrackID     TrackID
	RecordingID RecordingID
	AlbumID     AlbumID
}

func identities(path, contentFingerprint, audioFingerprint string, metadata Metadata) identitySet {
	recording := RecordingID(metadata.RecordingID)
	if recording == "" {
		recording = RecordingID(hashID("recording", audioFingerprint))
	}
	albumBasis := metadata.ReleaseID
	if albumBasis == "" {
		albumBasis = normalize(metadata.Album) + "|" + normalize(metadata.AlbumArtist) + "|" + normalize(parentPath(path))
	}
	return identitySet{
		FileID:      FileID(hashID("file", contentFingerprint, normalize(path))),
		TrackID:     TrackID(hashID("track", contentFingerprint, normalize(path))),
		RecordingID: recording,
		AlbumID:     AlbumID(hashID("album", albumBasis)),
	}
}

func hashID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func normalize(value string) string {
	return identifierFold.String(norm.NFC.String(strings.Join(strings.Fields(strings.TrimSpace(value)), " ")))
}

func normalizePath(value string) string {
	return identifierFold.String(norm.NFC.String(filepathSlash(strings.TrimSpace(value))))
}

func parentPath(path string) string {
	path = filepathSlash(path)
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[:index]
	}
	return "."
}
