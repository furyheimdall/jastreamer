package downloads

import (
	"os"
	"time"
)

const (
	QualityOriginal = "original"
	QualityAAC256   = "aac_256"
)

type Capabilities struct {
	Version   int      `json:"version"`
	Qualities []string `json:"qualities"`
	MaxTracks int      `json:"max_tracks"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Track struct {
	Index         int    `json:"index"`
	TrackID       string `json:"track_id"`
	Status        string `json:"status"`
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	Album         string `json:"album"`
	AlbumArtist   string `json:"album_artist"`
	Disc          int    `json:"disc"`
	Track         int    `json:"track"`
	DurationMS    int64  `json:"duration_ms"`
	SourceVersion string `json:"source_version"`
	Quality       string `json:"quality"`
	Mime          string `json:"mime"`
	Codec         string `json:"codec"`
	ByteSize      int64  `json:"byte_size"`
	SHA256        string `json:"sha256"`
	MediaPath     string `json:"media_path"`
	ArtworkPath   string `json:"artwork_path"`
	Error         *Error `json:"error,omitempty"`
	artifactName  string
}

type Manifest struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Quality   string  `json:"quality"`
	Status    string  `json:"status"`
	ExpiresAt string  `json:"expires_at"`
	Tracks    []Track `json:"tracks"`
	Error     *Error  `json:"error,omitempty"`
}

type Request struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Quality string `json:"quality"`
}

type Artifact struct {
	File     *os.File
	FileName string
	Mime     string
	Size     int64
	SHA256   string
	ModTime  time.Time
}
