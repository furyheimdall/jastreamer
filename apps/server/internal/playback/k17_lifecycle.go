package playback

import (
	"context"
	"time"
)

type K17LifecycleTarget struct {
	RendererID RendererID
	ZoneID     ZoneID
	LastSeenAt time.Time
}

type K17Observation struct {
	RendererID RendererID
	ZoneID     ZoneID
	Transport  string
	Position   time.Duration
	CurrentURI string
	Owned      bool
	ObservedAt time.Time
}

func (store *Store) K17LifecycleTargets(ctx context.Context) ([]K17LifecycleTarget, error) {
	result := []K17LifecycleTarget{}
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT r.renderer_id,a.zone_id,r.updated_at FROM renderer_registry r
			JOIN renderer_assignments a ON a.renderer_id=r.renderer_id AND a.unassigned_revision IS NULL
			WHERE r.kind='k17' AND r.state='available' ORDER BY r.renderer_id`)
		if err != nil {
			return err
		}
		defer stmt.close()
		for {
			row, err := stmt.step()
			if err != nil {
				return err
			}
			if !row {
				return nil
			}
			lastSeenAt, err := time.Parse(time.RFC3339Nano, stmt.text(2))
			if err != nil {
				return err
			}
			result = append(result, K17LifecycleTarget{
				RendererID: RendererID(stmt.text(0)), ZoneID: ZoneID(stmt.text(1)), LastSeenAt: lastSeenAt,
			})
		}
	})
	return result, err
}

func (store *Store) RecordK17Observation(ctx context.Context, observation K17Observation) error {
	_, err := store.ApplyK17Observation(ctx, observation)
	return err
}

func (store *Store) MarkK17Unavailable(ctx context.Context, rendererID RendererID) error {
	if rendererID == "" {
		return ErrInvalidRenderer
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		renderer, found, err := loadRenderer(db, rendererID)
		if err != nil {
			return err
		}
		if !found || renderer.Kind != RendererKindK17 {
			return ErrRendererNotFound
		}
		now := store.clock.Now().UTC().Format(time.RFC3339Nano)
		if err := setRendererConnectionState(db, rendererConnectionUpdate{rendererID: rendererID, state: RendererUnavailable, observedAt: now}); err != nil {
			return err
		}
		if err := execBound(db, `UPDATE renderer_session_state SET connection_state='disconnected',disconnected_at=? WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, now); err != nil {
				return err
			}
			return stmt.bindText(2, string(rendererID))
		}); err != nil {
			return err
		}
		return suspendAssignedRenderer(db, rendererID)
	})
}

func ensureK17Truth(db *sqliteDB, rendererID RendererID, observedAt string) error {
	return execBound(db, `INSERT INTO renderer_session_state(renderer_id,connection_state,connected_at)
		VALUES (?,'connected',?) ON CONFLICT(renderer_id) DO NOTHING`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(rendererID)); err != nil {
			return err
		}
		return stmt.bindText(2, observedAt)
	})
}

func suspendK17Zone(db *sqliteDB, zoneID ZoneID, zone zoneRecord) error {
	if zone.transport == TransportIdle || zone.transport == TransportSuspended {
		return nil
	}
	return execBound(db, `UPDATE playback_zones SET revision=?,transport='suspended' WHERE zone_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(zone.revision+1)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	})
}
