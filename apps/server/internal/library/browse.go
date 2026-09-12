package library

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func (service *Service) Browse(ctx context.Context, query Query) (Page, error) {
	if query.Offset < 0 {
		return Page{}, invalid("offset must not be negative")
	}
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 1 || query.Limit > 500 {
		return Page{}, invalid("limit must be between 1 and 500")
	}
	if query.Kind == "" {
		query.Kind = "tracks"
	}
	if query.Path != "" {
		query.Path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(query.Path)))
		if query.Path == "." {
			query.Path = ""
		}
		if strings.HasPrefix(query.Path, "/") || query.Path == ".." || strings.HasPrefix(query.Path, "../") {
			return Page{}, invalid("folder path is invalid")
		}
	}
	tracks, err := service.browseTracks(ctx, query.RootID, query.AlbumID)
	if err != nil {
		return Page{}, err
	}
	filtered := filterTracks(tracks, query, query.Kind == "tracks")
	switch query.Kind {
	case "tracks":
		if err := sortTracks(filtered, query.Sort, query.AlbumID != ""); err != nil {
			return Page{}, err
		}
		items, total := pageTracks(filtered, query.Offset, query.Limit)
		return Page{Items: items, Total: total, Offset: query.Offset, Limit: query.Limit}, nil
	case "albums":
		items := albumItems(tracks, filtered, query.Search)
		if query.Sort != "" && query.Sort != "album" && query.Sort != "artist" {
			return Page{}, invalid("unsupported album sort")
		}
		if query.Sort == "artist" {
			slices.SortFunc(items, compareAlbumArtist)
		} else {
			slices.SortFunc(items, compareAlbumTitle)
		}
		page, total := pageSlice(items, query.Offset, query.Limit)
		return Page{Items: page, Total: total, Offset: query.Offset, Limit: query.Limit}, nil
	case "artists":
		items := artistItems(tracks, filtered, query.Search)
		if query.Sort != "" && query.Sort != "artist" {
			return Page{}, invalid("unsupported artist sort")
		}
		slices.SortFunc(items, func(a, b Artist) int {
			if order := strings.Compare(normalizeSearch(a.Name), normalizeSearch(b.Name)); order != 0 {
				return order
			}
			return strings.Compare(a.ID, b.ID)
		})
		page, total := pageSlice(items, query.Offset, query.Limit)
		return Page{Items: page, Total: total, Offset: query.Offset, Limit: query.Limit}, nil
	case "genres":
		items := genreItems(tracks, filtered, query.Search)
		if query.Sort != "" && query.Sort != "genre" && query.Sort != "name" {
			return Page{}, invalid("unsupported genre sort")
		}
		slices.SortFunc(items, func(a, b Genre) int {
			if order := strings.Compare(normalizeSearch(a.Name), normalizeSearch(b.Name)); order != 0 {
				return order
			}
			return strings.Compare(a.ID, b.ID)
		})
		page, total := pageSlice(items, query.Offset, query.Limit)
		return Page{Items: page, Total: total, Offset: query.Offset, Limit: query.Limit}, nil
	case "folders":
		items, err := service.folderItems(query, tracks, filtered)
		if err != nil {
			return Page{}, err
		}
		if query.Sort != "" && query.Sort != "path" && query.Sort != "name" {
			return Page{}, invalid("unsupported folder sort")
		}
		slices.SortFunc(items, func(a, b Folder) int {
			if order := strings.Compare(normalizeSearch(a.Name), normalizeSearch(b.Name)); order != 0 {
				return order
			}
			if order := strings.Compare(a.RootID, b.RootID); order != 0 {
				return order
			}
			return strings.Compare(a.Path, b.Path)
		})
		page, total := pageSlice(items, query.Offset, query.Limit)
		return Page{Items: page, Total: total, Offset: query.Offset, Limit: query.Limit}, nil
	default:
		return Page{}, invalid("library kind is invalid")
	}
}

func (service *Service) browseTracks(ctx context.Context, rootID, albumID string) ([]Track, error) {
	statement := `SELECT ` + trackColumns + ` FROM library_tracks WHERE available=1`
	args := []any{}
	if rootID != "" {
		statement += ` AND root_id=?`
		args = append(args, rootID)
	}
	if albumID != "" {
		statement += ` AND album_id=?`
		args = append(args, albumID)
	}
	rows, err := service.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("browse library tracks: %w", err)
	}
	defer rows.Close()
	tracks := []Track{}
	for rows.Next() {
		record, scanErr := scanTrack(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("read library track: %w", scanErr)
		}
		tracks = append(tracks, record.track)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("browse library tracks: %w", err)
	}
	return tracks, nil
}

func filterTracks(tracks []Track, query Query, includeSearch bool) []Track {
	result := make([]Track, 0, len(tracks))
	artist := normalizeSearch(query.Artist)
	genre := normalizeSearch(query.Genre)
	for _, track := range tracks {
		if artist != "" && normalizeSearch(track.Artist) != artist && normalizeSearch(track.AlbumArtist) != artist {
			continue
		}
		if genre != "" && !containsNormalized(track.Genres, genre) {
			continue
		}
		if query.Path != "" || query.RootID != "" && query.Kind == "tracks" {
			folder := filepath.ToSlash(filepath.Dir(filepath.FromSlash(track.Path)))
			if folder == "." {
				folder = ""
			}
			if folder != query.Path {
				continue
			}
		}
		if includeSearch && !trackMatchesSearch(track, query.Search) {
			continue
		}
		result = append(result, track)
	}
	return result
}

func trackMatchesSearch(track Track, search string) bool {
	search = normalizeSearch(search)
	if search == "" {
		return true
	}
	fields := []string{track.Title, track.Artist, track.Album, track.AlbumArtist, track.Path}
	for _, field := range fields {
		if strings.Contains(normalizeSearch(field), search) {
			return true
		}
	}
	for _, genre := range track.Genres {
		if strings.Contains(normalizeSearch(genre), search) {
			return true
		}
	}
	return false
}
func containsNormalized(values []string, wanted string) bool {
	for _, value := range values {
		if normalizeSearch(value) == wanted {
			return true
		}
	}
	return false
}

func sortTracks(tracks []Track, sortName string, albumDetail bool) error {
	if albumDetail && sortName == "" {
		sortName = "album"
	}
	if sortName == "" {
		sortName = "title"
	}
	var compare func(Track, Track) int
	switch sortName {
	case "album":
		compare = compareAlbumTrack
	case "title":
		compare = func(a, b Track) int { return compareTrackFields(a, b, a.Title, b.Title) }
	case "artist":
		compare = func(a, b Track) int {
			if order := strings.Compare(normalizeSearch(a.Artist), normalizeSearch(b.Artist)); order != 0 {
				return order
			}
			return compareTrackFields(a, b, a.Title, b.Title)
		}
	case "path":
		compare = func(a, b Track) int {
			if order := strings.Compare(normalizeSearch(a.Path), normalizeSearch(b.Path)); order != 0 {
				return order
			}
			return strings.Compare(a.ID, b.ID)
		}
	case "duration":
		compare = func(a, b Track) int {
			if a.DurationMS < b.DurationMS {
				return -1
			}
			if a.DurationMS > b.DurationMS {
				return 1
			}
			return compareTrackFields(a, b, a.Title, b.Title)
		}
	case "modified":
		compare = func(a, b Track) int {
			if a.ModifiedAt > b.ModifiedAt {
				return -1
			}
			if a.ModifiedAt < b.ModifiedAt {
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		}
	default:
		return invalid("unsupported track sort")
	}
	slices.SortFunc(tracks, compare)
	return nil
}
func compareAlbumTrack(a, b Track) int {
	if order := strings.Compare(normalizeSearch(a.AlbumArtist), normalizeSearch(b.AlbumArtist)); order != 0 {
		return order
	}
	if order := strings.Compare(normalizeSearch(a.Album), normalizeSearch(b.Album)); order != 0 {
		return order
	}
	ad, bd := a.Disc, b.Disc
	if ad == 0 {
		ad = 1 << 30
	}
	if bd == 0 {
		bd = 1 << 30
	}
	if ad < bd {
		return -1
	}
	if ad > bd {
		return 1
	}
	at, bt := a.Track, b.Track
	if at == 0 {
		at = 1 << 30
	}
	if bt == 0 {
		bt = 1 << 30
	}
	if at < bt {
		return -1
	}
	if at > bt {
		return 1
	}
	return compareTrackFields(a, b, a.Title, b.Title)
}
func compareTrackFields(a, b Track, af, bf string) int {
	if order := strings.Compare(normalizeSearch(af), normalizeSearch(bf)); order != 0 {
		return order
	}
	return strings.Compare(a.ID, b.ID)
}

func albumItems(all, matched []Track, search string) []Album {
	wanted := matchedAlbumIDs(matched, search)
	items := make(map[string]Album)
	for _, track := range all {
		if _, ok := wanted[track.AlbumID]; !ok {
			continue
		}
		item, exists := items[track.AlbumID]
		if !exists {
			item = Album{ID: track.AlbumID, Title: track.Album, Artist: track.AlbumArtist, ArtworkID: track.ArtworkID}
		}
		if exists && normalizeSearch(item.Artist) != normalizeSearch(track.AlbumArtist) {
			item.Artist = ""
		}
		item.TrackCount++
		if item.ArtworkID == "" && track.ArtworkID != "" {
			item.ArtworkID = track.ArtworkID
		}
		items[track.AlbumID] = item
	}
	result := make([]Album, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	return result
}
func matchedAlbumIDs(matched []Track, search string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, track := range matched {
		if trackMatchesSearch(track, search) {
			result[track.AlbumID] = struct{}{}
		}
	}
	return result
}
func compareAlbumTitle(a, b Album) int {
	if order := strings.Compare(normalizeSearch(a.Title), normalizeSearch(b.Title)); order != 0 {
		return order
	}
	if order := strings.Compare(normalizeSearch(a.Artist), normalizeSearch(b.Artist)); order != 0 {
		return order
	}
	return strings.Compare(a.ID, b.ID)
}
func compareAlbumArtist(a, b Album) int {
	if order := strings.Compare(normalizeSearch(a.Artist), normalizeSearch(b.Artist)); order != 0 {
		return order
	}
	return compareAlbumTitle(a, b)
}

func artistItems(all, matched []Track, search string) []Artist {
	wanted := make(map[string]struct{})
	needle := normalizeSearch(search)
	for _, track := range matched {
		if needle != "" && !trackMatchesSearch(track, search) {
			continue
		}
		for _, name := range []string{track.Artist, track.AlbumArtist} {
			if key := normalizeSearch(name); key != "" {
				wanted[key] = struct{}{}
			}
		}
	}
	counts := make(map[string]Artist)
	for _, track := range all {
		seen := make(map[string]struct{})
		for _, name := range []string{track.Artist, track.AlbumArtist} {
			key := normalizeSearch(name)
			if key == "" {
				continue
			}
			if _, ok := wanted[key]; !ok {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			item := counts[key]
			if item.ID == "" {
				item = Artist{ID: stableID("artist", key), Name: name}
			}
			item.TrackCount++
			counts[key] = item
		}
	}
	result := make([]Artist, 0, len(counts))
	for _, item := range counts {
		result = append(result, item)
	}
	return result
}
func genreItems(all, matched []Track, search string) []Genre {
	wanted := make(map[string]struct{})
	needle := normalizeSearch(search)
	for _, track := range matched {
		if needle != "" && !trackMatchesSearch(track, search) {
			continue
		}
		for _, name := range track.Genres {
			if key := normalizeSearch(name); key != "" {
				wanted[key] = struct{}{}
			}
		}
	}
	counts := make(map[string]Genre)
	for _, track := range all {
		for _, name := range track.Genres {
			key := normalizeSearch(name)
			if _, ok := wanted[key]; !ok {
				continue
			}
			item := counts[key]
			if item.ID == "" {
				item = Genre{ID: stableID("genre", key), Name: name}
			}
			item.TrackCount++
			counts[key] = item
		}
	}
	result := make([]Genre, 0, len(counts))
	for _, item := range counts {
		result = append(result, item)
	}
	return result
}

func (service *Service) folderItems(query Query, all, matched []Track) ([]Folder, error) {
	if query.RootID == "" {
		counts := make(map[string]int)
		for _, track := range all {
			if trackMatchesSearch(track, query.Search) {
				counts[track.RootID]++
			}
		}
		items := []Folder{}
		for _, root := range service.rootsSnapshot() {
			if query.Search != "" && !strings.Contains(normalizeSearch(root.Name), normalizeSearch(query.Search)) && counts[root.ID] == 0 {
				continue
			}
			items = append(items, Folder{RootID: root.ID, Path: "", Name: root.Name, TrackCount: counts[root.ID]})
		}
		return items, nil
	}
	if _, ok := service.root(query.RootID); !ok {
		return []Folder{}, nil
	}
	prefix := query.Path
	if prefix != "" {
		prefix += "/"
	}
	counts := make(map[string]int)
	for _, track := range all {
		if track.RootID != query.RootID || !strings.HasPrefix(track.Path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(track.Path, prefix)
		slash := strings.IndexByte(remainder, '/')
		if slash < 0 {
			continue
		}
		child := prefix + remainder[:slash]
		if query.Search != "" && !trackMatchesSearch(track, query.Search) && !strings.Contains(normalizeSearch(child), normalizeSearch(query.Search)) {
			continue
		}
		counts[child]++
	}
	items := make([]Folder, 0, len(counts))
	for path, count := range counts {
		items = append(items, Folder{RootID: query.RootID, Path: path, Name: filepath.Base(filepath.FromSlash(path)), TrackCount: count})
	}
	return items, nil
}

func pageTracks(items []Track, offset, limit int) ([]Track, int) {
	return pageSlice(items, offset, limit)
}
func pageSlice[T any](items []T, offset, limit int) ([]T, int) {
	total := len(items)
	if offset >= total {
		return []T{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total
}
