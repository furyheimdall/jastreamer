package library

import "encoding/json"

type Root struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Track struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Artist      string   `json:"artist"`
	Album       string   `json:"album"`
	AlbumArtist string   `json:"album_artist"`
	AlbumID     string   `json:"album_id"`
	Disc        int      `json:"disc"`
	Track       int      `json:"track"`
	Genres      []string `json:"genres"`
	DurationMS  int64    `json:"duration_ms"`
	Format      string   `json:"format"`
	Mime        string   `json:"mime"`
	ArtworkID   string   `json:"artwork_id"`
	RootID      string   `json:"root_id"`
	Path        string   `json:"path"`
	Available   bool     `json:"available"`
	Size        int64    `json:"size"`
	ModifiedAt  string   `json:"modified_at"`
}

type TrackInfo struct {
	Track Track               `json:"track"`
	Audio AudioProperties     `json:"audio"`
	Tags  map[string][]string `json:"tags"`
}

type AudioProperties struct {
	Codec         string `json:"codec"`
	SampleRate    *int64 `json:"sample_rate"`
	Channels      *int   `json:"channels"`
	BitsPerSample *int   `json:"bits_per_sample"`
	BitRate       *int64 `json:"bit_rate"`
}

type Album struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	ArtworkID  string `json:"artwork_id"`
	TrackCount int    `json:"track_count"`
}

type Artist struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

type Genre struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

type Folder struct {
	RootID     string `json:"root_id"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

type Query struct {
	Kind    string
	Search  string
	AlbumID string
	Artist  string
	Genre   string
	RootID  string
	Path    string
	Sort    string
	Offset  int
	Limit   int
}

type Page struct {
	Items  any `json:"items"`
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type Playlist struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Revision  int64    `json:"revision"`
	TrackIDs  []string `json:"track_ids"`
	Tracks    []Track  `json:"tracks"`
	UpdatedAt string   `json:"updated_at"`
}

type ScanJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Discovered  int64  `json:"discovered"`
	Processed   int64  `json:"processed"`
	Added       int64  `json:"added"`
	Updated     int64  `json:"updated"`
	Unavailable int64  `json:"unavailable"`
	Errors      int64  `json:"errors"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
	Error       string `json:"error"`
}

type storedMetadata struct {
	Title       string   `json:"title"`
	Artist      string   `json:"artist"`
	Album       string   `json:"album"`
	AlbumArtist string   `json:"album_artist"`
	Disc        int      `json:"disc"`
	Track       int      `json:"track"`
	Genres      []string `json:"genres"`
}

func encodeGenres(values []string) string {
	if values == nil {
		values = []string{}
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
