package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (service *Service) SetLiked(ctx context.Context, id string, liked bool) (Track, error) {
	if id == "" {
		return Track{}, notFound("track was not found")
	}
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Track{}, fmt.Errorf("begin track like update: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM library_tracks WHERE id=?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Track{}, notFound("track was not found")
	} else if err != nil {
		return Track{}, fmt.Errorf("load track for like update: %w", err)
	}
	if liked {
		_, err = tx.ExecContext(ctx, `INSERT INTO library_track_likes(track_id,updated_at) VALUES(?,?) ON CONFLICT(track_id) DO UPDATE SET updated_at=excluded.updated_at`, id, timestamp(time.Now()))
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM library_track_likes WHERE track_id=?`, id)
	}
	if err != nil {
		return Track{}, fmt.Errorf("update track like: %w", err)
	}
	row := tx.QueryRowContext(ctx, `SELECT `+trackColumns+` FROM library_tracks WHERE id=?`, id)
	record, err := scanTrack(row)
	if err != nil {
		return Track{}, fmt.Errorf("load updated track: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Track{}, fmt.Errorf("commit track like update: %w", err)
	}
	service.notify("library")
	return record.track, nil
}
