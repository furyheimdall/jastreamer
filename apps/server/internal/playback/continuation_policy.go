package playback

import (
	"context"

	"github.com/jastreamer/jastreamer-server/internal/decision"
)

func (store *Store) ContinuationPolicy(ctx context.Context, zoneID ZoneID) (ContinuationPolicy, error) {
	policy := ContinuationPolicy{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := ensureZone(db, zoneID); err != nil {
			return err
		}
		loaded, err := loadContinuationPolicy(db, zoneID)
		policy = loaded
		return err
	})
	return policy, err
}

func (store *Store) UpdateContinuationPolicy(ctx context.Context, request PolicyUpdate) (ContinuationPolicy, error) {
	if request.ZoneID == "" || !request.Mode.Valid() ||
		(request.SessionOverride != "" && !request.SessionOverride.Valid()) ||
		request.ArtistGap < 0 || request.ArtistGap > 100 || request.AlbumGap < 0 || request.AlbumGap > 100 {
		return ContinuationPolicy{}, ErrInvalidPolicy
	}
	policy := ContinuationPolicy{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := ensureZone(db, request.ZoneID); err != nil {
			return err
		}
		current, err := loadContinuationPolicy(db, request.ZoneID)
		if err != nil {
			return err
		}
		if current.Revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		policy = ContinuationPolicy{
			Mode: request.Mode, SessionOverride: request.SessionOverride,
			ArtistGap: request.ArtistGap, AlbumGap: request.AlbumGap, Revision: current.Revision + 1,
		}
		return execBound(db, `
			UPDATE playback_continuation_policies
			SET mode=?,artist_gap=?,album_gap=?,session_override=?,revision=? WHERE zone_id=?`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, string(policy.Mode)); err != nil {
				return err
			}
			if err := stmt.bindInt64(2, int64(policy.ArtistGap)); err != nil {
				return err
			}
			if err := stmt.bindInt64(3, int64(policy.AlbumGap)); err != nil {
				return err
			}
			if err := stmt.bindText(4, string(policy.SessionOverride)); err != nil {
				return err
			}
			if err := stmt.bindInt64(5, policy.Revision); err != nil {
				return err
			}
			return stmt.bindText(6, string(request.ZoneID))
		})
	})
	return policy, err
}

func loadContinuationPolicy(db *sqliteDB, zoneID ZoneID) (ContinuationPolicy, error) {
	stmt, err := db.prepare(`
		SELECT mode,artist_gap,album_gap,session_override,revision
		FROM playback_continuation_policies WHERE zone_id=?`)
	if err != nil {
		return ContinuationPolicy{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return ContinuationPolicy{}, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return ContinuationPolicy{}, err
	}
	return ContinuationPolicy{
		Mode: decision.Policy(stmt.text(0)), ArtistGap: int(stmt.int64(1)), AlbumGap: int(stmt.int64(2)),
		SessionOverride: decision.Policy(stmt.text(3)), Revision: stmt.int64(4),
	}, nil
}
