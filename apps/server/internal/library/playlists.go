package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maximumPlaylistTracks = 10_000

func (service *Service) Playlists(ctx context.Context) ([]Playlist, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT id,name,revision,updated_at FROM library_playlists ORDER BY lower(name),id`)
	if err != nil {
		return nil, fmt.Errorf("list playlists: %w", err)
	}
	items := []Playlist{}
	for rows.Next() {
		var item Playlist
		if err := rows.Scan(&item.ID, &item.Name, &item.Revision, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read playlist: %w", err)
		}
		item.TrackIDs = []string{}
		item.Tracks = []Track{}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list playlists: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close playlists: %w", err)
	}
	for index := range items {
		ids, err := service.playlistTrackIDs(ctx, service.db, items[index].ID)
		if err != nil {
			return nil, err
		}
		items[index].TrackIDs = ids
	}
	return items, nil
}

func (service *Service) Playlist(ctx context.Context, id string) (Playlist, error) {
	var item Playlist
	err := service.db.QueryRowContext(ctx, `SELECT id,name,revision,updated_at FROM library_playlists WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.Revision, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, notFound("playlist was not found")
	}
	if err != nil {
		return Playlist{}, fmt.Errorf("load playlist: %w", err)
	}
	ids, err := service.playlistTrackIDs(ctx, service.db, id)
	if err != nil {
		return Playlist{}, err
	}
	item.TrackIDs = ids
	tracks, err := service.playlistTracks(ctx, service.db, ids)
	if err != nil {
		return Playlist{}, err
	}
	item.Tracks = tracks
	return item, nil
}

func (service *Service) playlistTracks(ctx context.Context, query playlistQuery, ids []string) ([]Track, error) {
	byID := make(map[string]Track, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := byID[id]; !exists {
			byID[id] = Track{}
			unique = append(unique, id)
		}
	}
	const batchSize = 500
	for start := 0; start < len(unique); start += batchSize {
		end := start + batchSize
		if end > len(unique) {
			end = len(unique)
		}
		batch := unique[start:end]
		arguments := make([]any, len(batch))
		for index := range batch {
			arguments[index] = batch[index]
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := query.QueryContext(ctx, `SELECT `+trackColumns+` FROM library_tracks WHERE id IN (`+placeholders+`)`, arguments...)
		if err != nil {
			return nil, fmt.Errorf("hydrate playlist tracks: %w", err)
		}
		for rows.Next() {
			record, scanErr := scanTrack(rows)
			if scanErr != nil {
				rows.Close()
				return nil, fmt.Errorf("read playlist track: %w", scanErr)
			}
			byID[record.track.ID] = record.track
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("hydrate playlist tracks: %w", err)
		}
		rows.Close()
	}
	result := make([]Track, 0, len(ids))
	for _, id := range ids {
		track := byID[id]
		if track.ID == "" {
			track.ID = id
			track.Genres = []string{}
			track.Available = false
		}
		result = append(result, track)
	}
	return result, nil
}

type playlistQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (service *Service) playlistTrackIDs(ctx context.Context, query playlistQuery, id string) ([]string, error) {
	rows, err := query.QueryContext(ctx, `SELECT track_id FROM library_playlist_items WHERE playlist_id=? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("load playlist items: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var trackID string
		if err := rows.Scan(&trackID); err != nil {
			return nil, fmt.Errorf("read playlist item: %w", err)
		}
		ids = append(ids, trackID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load playlist items: %w", err)
	}
	return ids, nil
}

func (service *Service) SavePlaylist(ctx context.Context, id, name string, trackIDs []string, revision int64) (Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Playlist{}, invalid("playlist name must be between 1 and 200 characters")
	}
	if len(trackIDs) > maximumPlaylistTracks {
		return Playlist{}, invalid("playlist has too many tracks")
	}
	validatedIDs := make([]string, len(trackIDs))
	for index, trackID := range trackIDs {
		trackID = strings.TrimSpace(trackID)
		if trackID == "" || len(trackID) > 128 {
			return Playlist{}, invalid("playlist contains an invalid track id")
		}
		validatedIDs[index] = trackID
	}
	creating := id == ""
	if creating {
		if revision != 0 {
			return Playlist{}, invalid("new playlist revision must be zero")
		}
		var err error
		id, err = randomID()
		if err != nil {
			return Playlist{}, fmt.Errorf("create playlist id: %w", err)
		}
	} else if revision < 1 {
		return Playlist{}, invalid("playlist revision is required")
	}
	now := timestamp(time.Now())
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Playlist{}, fmt.Errorf("begin playlist save: %w", err)
	}
	defer tx.Rollback()
	if creating {
		_, err = tx.ExecContext(ctx, `INSERT INTO library_playlists(id,name,revision,updated_at) VALUES(?,?,1,?)`, id, name, now)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE library_playlists SET name=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, name, now, id, revision)
		if err == nil {
			affected, _ := result.RowsAffected()
			if affected == 0 {
				var exists int
				lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM library_playlists WHERE id=?`, id).Scan(&exists)
				if errors.Is(lookupErr, sql.ErrNoRows) {
					return Playlist{}, notFound("playlist was not found")
				}
				if lookupErr != nil {
					return Playlist{}, fmt.Errorf("check playlist revision: %w", lookupErr)
				}
				return Playlist{}, conflict("REVISION_CONFLICT", "playlist changed; refresh and try again")
			}
		}
	}
	if err != nil {
		return Playlist{}, fmt.Errorf("save playlist: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM library_playlist_items WHERE playlist_id=?`, id); err != nil {
		return Playlist{}, fmt.Errorf("replace playlist items: %w", err)
	}
	for position, trackID := range validatedIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO library_playlist_items(playlist_id,position,track_id) VALUES(?,?,?)`, id, position, trackID); err != nil {
			return Playlist{}, fmt.Errorf("save playlist item: %w", err)
		}
	}
	tracks, err := service.playlistTracks(ctx, tx, validatedIDs)
	if err != nil {
		return Playlist{}, err
	}
	if err = tx.Commit(); err != nil {
		return Playlist{}, fmt.Errorf("commit playlist save: %w", err)
	}
	service.notify("playlists")
	newRevision := int64(1)
	if !creating {
		newRevision = revision + 1
	}
	return Playlist{ID: id, Name: name, Revision: newRevision, TrackIDs: validatedIDs, Tracks: tracks, UpdatedAt: now}, nil
}

func (service *Service) DeletePlaylist(ctx context.Context, id string, revision int64) error {
	if id == "" || revision < 1 {
		return invalid("playlist id and revision are required")
	}
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin playlist delete: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM library_playlists WHERE id=? AND revision=?`, id, revision)
	if err != nil {
		return fmt.Errorf("delete playlist: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM library_playlists WHERE id=?`, id).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return notFound("playlist was not found")
		}
		if lookupErr != nil {
			return fmt.Errorf("check playlist revision: %w", lookupErr)
		}
		return conflict("REVISION_CONFLICT", "playlist changed; refresh and try again")
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit playlist delete: %w", err)
	}
	service.notify("playlists")
	return nil
}
