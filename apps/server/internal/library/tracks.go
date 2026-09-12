package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const trackColumns = `id,title,artist,album,album_artist,album_id,disc,track_number,genres_json,duration_ms,format,mime,artwork_id,root_id,relative_path,available,byte_size,modified_at,modified_ns`

type trackRecord struct {
	track      Track
	modifiedNS int64
}

func (service *Service) Track(ctx context.Context, id string) (Track, error) {
	record, err := service.trackRecord(ctx, id)
	if err != nil {
		return Track{}, err
	}
	return record.track, nil
}

func (service *Service) trackRecord(ctx context.Context, id string) (trackRecord, error) {
	if strings.TrimSpace(id) == "" {
		return trackRecord{}, notFound("track was not found")
	}
	row := service.db.QueryRowContext(ctx, `SELECT `+trackColumns+` FROM library_tracks WHERE id=?`, id)
	record, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return trackRecord{}, notFound("track was not found")
	}
	if err != nil {
		return trackRecord{}, fmt.Errorf("load library track: %w", err)
	}
	return record, nil
}

func scanTrack(scanner interface{ Scan(...any) error }) (trackRecord, error) {
	var record trackRecord
	var genres string
	var available int
	err := scanner.Scan(
		&record.track.ID, &record.track.Title, &record.track.Artist, &record.track.Album,
		&record.track.AlbumArtist, &record.track.AlbumID, &record.track.Disc, &record.track.Track,
		&genres, &record.track.DurationMS, &record.track.Format, &record.track.Mime,
		&record.track.ArtworkID, &record.track.RootID, &record.track.Path, &available,
		&record.track.Size, &record.track.ModifiedAt, &record.modifiedNS,
	)
	if err != nil {
		return trackRecord{}, err
	}
	record.track.Available = available == 1
	record.track.Genres = []string{}
	if err := json.Unmarshal([]byte(genres), &record.track.Genres); err != nil {
		return trackRecord{}, fmt.Errorf("decode track genres: %w", err)
	}
	if record.track.Genres == nil {
		record.track.Genres = []string{}
	}
	return record, nil
}

func (service *Service) Open(ctx context.Context, id string) (*os.File, Track, error) {
	record, err := service.trackRecord(ctx, id)
	if err != nil {
		return nil, Track{}, err
	}
	if !record.track.Available {
		return nil, Track{}, notFound("track is unavailable")
	}
	root, ok := service.root(record.track.RootID)
	if !ok {
		return nil, Track{}, notFound("track is unavailable")
	}
	file, err := openRootFile(ctx, root.Path, record.track.Path)
	if err != nil {
		return nil, Track{}, notFound("track is unavailable")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.track.Size || info.ModTime().UnixNano() != record.modifiedNS {
		file.Close()
		return nil, Track{}, conflict("MEDIA_STALE", "track changed since the last library scan")
	}
	return file, record.track, nil
}

func openRootFile(ctx context.Context, rootPath, relative string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(rootPath) || relative == "" || strings.ContainsRune(relative, '\x00') {
		return nil, errors.New("unsafe library path")
	}
	relative = filepath.FromSlash(relative)
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return nil, errors.New("unsafe library path")
	}
	for component := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return nil, errors.New("unsafe library path")
		}
	}
	rootPath = filepath.Clean(rootPath)
	pathRootInfo, err := os.Lstat(rootPath)
	if err != nil || !pathRootInfo.IsDir() || pathRootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("library root is unavailable or unsafe")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	openedRootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(pathRootInfo, openedRootInfo) {
		return nil, errors.New("library root changed while opening")
	}
	before, err := root.Lstat(relative)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("library path is not a regular file")
	}
	file, err := openRootReadOnly(root, relative)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	current, currentErr := root.Lstat(relative)
	if err != nil || currentErr != nil || !after.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) || !os.SameFile(after, current) {
		file.Close()
		return nil, errors.New("library path changed while opening")
	}
	currentRoot, err := os.Lstat(rootPath)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !os.SameFile(pathRootInfo, currentRoot) {
		file.Close()
		return nil, errors.New("library root changed while opening")
	}
	if err := ctx.Err(); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
