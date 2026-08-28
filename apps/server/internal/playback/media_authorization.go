package playback

import (
	"context"
	"errors"
)

var ErrMediaUnauthorized = errors.New("playback: media identity is not active")

type MediaAuthorization struct {
	RendererID RendererID
	ZoneID     ZoneID
	PlayID     PlayID
	TrackID    TrackID
}

func (store *Store) AuthorizeMedia(ctx context.Context, request MediaAuthorization) error {
	if request.RendererID == "" || request.ZoneID == "" || request.PlayID == "" || request.TrackID == "" {
		return ErrMediaUnauthorized
	}
	return store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT r.state,p.track_id,p.state,z.current_play_id
			FROM renderer_registry r
			JOIN renderer_assignments a ON a.renderer_id=r.renderer_id AND a.unassigned_revision IS NULL
			JOIN playback_zones z ON z.zone_id=a.zone_id
			JOIN playback_plays p ON p.zone_id=z.zone_id AND p.play_id=z.current_play_id
			WHERE r.renderer_id=? AND a.zone_id=?`)
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(request.RendererID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(request.ZoneID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil {
			return err
		}
		state := RendererState("")
		if row {
			state = RendererState(stmt.text(0))
		}
		if !row || (state != RendererAvailable && state != RendererConnected) || TrackID(stmt.text(1)) != request.TrackID ||
			PlayID(stmt.text(3)) != request.PlayID || (stmt.text(2) != "reserved" && stmt.text(2) != "playing") {
			return ErrMediaUnauthorized
		}
		return nil
	})
}
