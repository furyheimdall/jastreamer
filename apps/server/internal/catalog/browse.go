package catalog

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultBrowseLimit  = 100
	MaximumBrowseLimit  = 500
	MaximumSearchLength = 200
)

var (
	ErrCatalogRevisionChanged = errors.New("catalog: revision changed")
	ErrInvalidCursor          = errors.New("catalog: invalid cursor")
	ErrInvalidLimit           = errors.New("catalog: invalid browse limit")
	ErrInvalidQuery           = errors.New("catalog: invalid search query")
	searchFold                = cases.Fold()
)

type BrowseRequest struct {
	Query  string
	Limit  int
	Cursor string
}

type BrowseTrack struct {
	TrackID     TrackID     `json:"track_id"`
	RecordingID RecordingID `json:"recording_id"`
	AlbumID     AlbumID     `json:"album_id"`
	Format      Format      `json:"format"`
	Title       string      `json:"title"`
	Artist      string      `json:"artist"`
	Album       string      `json:"album"`
	AlbumArtist string      `json:"album_artist"`
	DiscNumber  int         `json:"disc_number,omitempty"`
	TrackNumber int         `json:"track_number,omitempty"`
}

type BrowsePage struct {
	Items           []BrowseTrack `json:"tracks"`
	NextCursor      string        `json:"next_cursor,omitempty"`
	CatalogRevision uint64        `json:"catalog_revision"`
}

type indexedBrowseTrack struct {
	view         BrowseTrack
	searchFields [4]string
}

type Browser struct {
	revision uint64
	tracks   []indexedBrowseTrack
}

func NewBrowser(snapshot Snapshot) *Browser {
	tracks := make([]indexedBrowseTrack, 0, len(snapshot.Tracks))
	for _, track := range snapshot.Tracks {
		if !track.Available {
			continue
		}
		view := BrowseTrack{
			TrackID: track.TrackID, RecordingID: track.RecordingID, AlbumID: track.AlbumID,
			Format: track.Format, Title: track.Metadata.Title, Artist: track.Metadata.Artist,
			Album: track.Metadata.Album, AlbumArtist: track.Metadata.AlbumArtist,
			DiscNumber: track.Metadata.Disc, TrackNumber: track.Metadata.Track,
		}
		fields := [4]string{view.Title, view.Artist, view.Album, view.AlbumArtist}
		for index := range fields {
			fields[index] = normalizedSearch(fields[index])
		}
		tracks = append(tracks, indexedBrowseTrack{view: view, searchFields: fields})
	}
	sort.Slice(tracks, func(left, right int) bool { return tracks[left].view.TrackID < tracks[right].view.TrackID })
	return &Browser{revision: snapshot.Revision, tracks: tracks}
}

func (browser *Browser) Browse(request BrowseRequest) (BrowsePage, error) {
	limit := request.Limit
	if limit == 0 {
		limit = DefaultBrowseLimit
	}
	if limit < 1 || limit > MaximumBrowseLimit {
		return BrowsePage{}, ErrInvalidLimit
	}
	query := normalizedSearch(request.Query)
	if utf8.RuneCountInString(query) > MaximumSearchLength {
		return BrowsePage{}, ErrInvalidQuery
	}
	offset, err := decodeBrowseCursor(request.Cursor, browser.revision, query)
	if err != nil {
		return BrowsePage{}, err
	}
	matches := browser.matching(query)
	if offset > len(matches) {
		return BrowsePage{}, ErrInvalidCursor
	}
	end := min(offset+limit, len(matches))
	items := make([]BrowseTrack, end-offset)
	for index, match := range matches[offset:end] {
		items[index] = browser.tracks[match].view
	}
	page := BrowsePage{Items: items, CatalogRevision: browser.revision}
	if end < len(matches) {
		page.NextCursor = encodeBrowseCursor(browser.revision, query, end)
	}
	return page, nil
}

func (browser *Browser) matching(query string) []int {
	matches := make([]int, 0, len(browser.tracks))
	for index, track := range browser.tracks {
		if query == "" {
			matches = append(matches, index)
			continue
		}
		for _, field := range track.searchFields {
			if strings.Contains(field, query) {
				matches = append(matches, index)
				break
			}
		}
	}
	return matches
}

func normalizedSearch(value string) string {
	return norm.NFC.String(searchFold.String(norm.NFC.String(strings.TrimSpace(value))))
}
