package downloads

import (
	"encoding/json"
	"errors"
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
	ID      string `json:"id,omitempty"`
	RootID  string `json:"root_id,omitempty"`
	Path    string `json:"path,omitempty"`
	Quality string `json:"quality"`
}

func (request *Request) UnmarshalJSON(data []byte) error {
	var fields map[string]*string
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	kind := fields["kind"]
	if kind == nil {
		return errors.New("download kind is required")
	}
	required := []string{"kind", "quality", "id"}
	switch *kind {
	case "track", "album", "playlist":
	case "folder":
		required = []string{"kind", "quality", "root_id", "path"}
	default:
		return errors.New("download kind is unsupported")
	}
	if len(fields) != len(required) {
		return errors.New("download target fields are invalid")
	}
	for _, name := range required {
		if fields[name] == nil {
			return errors.New("download target fields must be strings")
		}
	}
	decoded := Request{Kind: *kind, Quality: *fields["quality"]}
	if *kind == "folder" {
		decoded.RootID = *fields["root_id"]
		decoded.Path = *fields["path"]
	} else {
		decoded.ID = *fields["id"]
	}
	*request = decoded
	return nil
}

type Artifact struct {
	File     *os.File
	FileName string
	Mime     string
	Size     int64
	SHA256   string
	ModTime  time.Time
}
