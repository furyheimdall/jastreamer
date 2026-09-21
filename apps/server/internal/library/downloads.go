package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const MaximumDownloadTracks = 10_000

// DownloadSnapshot is a point-in-time catalog or playlist view used only while
// preparing a client-owned import. Track order and duplicate playlist entries
// are retained exactly.
type DownloadSnapshot struct {
	Kind              string
	ID                string
	Title             string
	Tracks            []Track
	IntegrityFailures map[string]bool
}

func (service *Service) DownloadSnapshot(ctx context.Context, kind, id string) (DownloadSnapshot, error) {
	if id == "" {
		return DownloadSnapshot{}, invalid("download source id is required")
	}
	tx, err := service.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DownloadSnapshot{}, fmt.Errorf("begin download snapshot: %w", err)
	}
	defer tx.Rollback()

	result := DownloadSnapshot{Kind: kind, ID: id, Tracks: []Track{}, IntegrityFailures: make(map[string]bool)}
	failedRows, err := tx.QueryContext(ctx, `SELECT items.track_id FROM library_verification_items AS items
		JOIN library_verification_state AS state ON state.run_id=items.run_id
		JOIN library_tracks AS tracks ON tracks.id=items.track_id
		WHERE items.status='failed' AND items.byte_size=tracks.byte_size AND items.modified_ns=tracks.modified_ns`)
	if err != nil {
		return DownloadSnapshot{}, fmt.Errorf("load known download integrity failures: %w", err)
	}
	for failedRows.Next() {
		var trackID string
		if err := failedRows.Scan(&trackID); err != nil {
			failedRows.Close()
			return DownloadSnapshot{}, fmt.Errorf("read known download integrity failure: %w", err)
		}
		result.IntegrityFailures[trackID] = true
	}
	if err := failedRows.Err(); err != nil {
		failedRows.Close()
		return DownloadSnapshot{}, fmt.Errorf("read known download integrity failures: %w", err)
	}
	if err := failedRows.Close(); err != nil {
		return DownloadSnapshot{}, fmt.Errorf("close known download integrity failures: %w", err)
	}
	switch kind {
	case "track":
		record, loadErr := scanTrack(tx.QueryRowContext(ctx, `SELECT `+trackColumns+` FROM library_tracks WHERE id=? AND available=1`, id))
		if errors.Is(loadErr, sql.ErrNoRows) {
			return DownloadSnapshot{}, notFound("track was not found")
		}
		if loadErr != nil {
			return DownloadSnapshot{}, fmt.Errorf("snapshot download track: %w", loadErr)
		}
		result.Title = record.track.Title
		result.Tracks = append(result.Tracks, record.track)
	case "album":
		rows, queryErr := tx.QueryContext(ctx, `SELECT `+trackColumns+` FROM library_tracks WHERE album_id=? AND available=1 ORDER BY disc,track_number,lower(title),id`, id)
		if queryErr != nil {
			return DownloadSnapshot{}, fmt.Errorf("snapshot download album: %w", queryErr)
		}
		for rows.Next() {
			record, scanErr := scanTrack(rows)
			if scanErr != nil {
				rows.Close()
				return DownloadSnapshot{}, fmt.Errorf("read download album: %w", scanErr)
			}
			if result.Title == "" {
				result.Title = record.track.Album
			}
			result.Tracks = append(result.Tracks, record.track)
			if len(result.Tracks) > MaximumDownloadTracks {
				rows.Close()
				return DownloadSnapshot{}, invalid("download collection has too many tracks")
			}
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return DownloadSnapshot{}, fmt.Errorf("read download album: %w", queryErr)
		}
		if queryErr = rows.Close(); queryErr != nil {
			return DownloadSnapshot{}, fmt.Errorf("close download album: %w", queryErr)
		}
		if len(result.Tracks) == 0 {
			return DownloadSnapshot{}, notFound("album was not found")
		}
	case "playlist":
		var revision int64
		if queryErr := tx.QueryRowContext(ctx, `SELECT name,revision FROM library_playlists WHERE id=?`, id).Scan(&result.Title, &revision); errors.Is(queryErr, sql.ErrNoRows) {
			return DownloadSnapshot{}, notFound("playlist was not found")
		} else if queryErr != nil {
			return DownloadSnapshot{}, fmt.Errorf("snapshot download playlist: %w", queryErr)
		}
		trackIDs, loadErr := service.playlistTrackIDs(ctx, tx, id)
		if loadErr != nil {
			return DownloadSnapshot{}, loadErr
		}
		if len(trackIDs) > MaximumDownloadTracks {
			return DownloadSnapshot{}, invalid("download collection has too many tracks")
		}
		result.Tracks, loadErr = service.playlistTracks(ctx, tx, trackIDs)
		if loadErr != nil {
			return DownloadSnapshot{}, loadErr
		}
	default:
		return DownloadSnapshot{}, invalid("download kind must be track, album, or playlist")
	}
	if err := tx.Commit(); err != nil {
		return DownloadSnapshot{}, fmt.Errorf("commit download snapshot: %w", err)
	}
	return result, nil
}
