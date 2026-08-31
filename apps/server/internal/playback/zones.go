package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

var (
	ErrInvalidZone  = errors.New("playback: invalid zone")
	ErrZoneNotFound = errors.New("playback: zone not found")
)

type ZoneDefinition struct {
	ID          ZoneID
	DisplayName string
}

type Zone struct {
	ID          ZoneID
	DisplayName string
	Revision    Revision
	RendererID  RendererID
	Transport   Transport
}

type ZonesSnapshot struct {
	Zones     []Zone
	Renderers []RendererInventory
}

type DeleteZoneRequest struct {
	ZoneID           ZoneID
	IdempotencyKey   string
	ExpectedRevision Revision
}

type DeleteZoneResult struct {
	ZoneID   ZoneID
	Revision Revision
	Replayed bool
}

func (store *Store) CreateZone(ctx context.Context, input ZoneDefinition) (Zone, error) {
	if input.ID == "" || input.DisplayName == "" {
		return Zone{}, ErrInvalidZone
	}
	result := Zone{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := execBound(db, "DELETE FROM playback_idempotency WHERE zone_id=? AND operation='delete_zone'", func(stmt *sqliteStmt) error {
			return stmt.bindText(1, string(input.ID))
		}); err != nil {
			return err
		}
		if err := ensureZone(db, input.ID); err != nil {
			return err
		}
		createdAt := time.Time{}.UTC().Format(time.RFC3339Nano)
		if err := execBound(db, `INSERT OR IGNORE INTO server_zones(zone_id,display_name,created_at,updated_at) VALUES (?,?,?,?)`, func(stmt *sqliteStmt) error {
			for index, value := range []string{string(input.ID), input.DisplayName, createdAt, createdAt} {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		loaded, found, err := loadZoneInventory(db, input.ID)
		if err != nil {
			return err
		}
		if !found {
			return ErrZoneNotFound
		}
		if loaded.DisplayName != input.DisplayName {
			return ErrInvalidZone
		}
		result = loaded
		return nil
	})
	return result, err
}

// DeleteZone removes any zone, including the last remaining zone. Zones have no
// distinguished default identity; callers preserve baselines by deleting only IDs they own.
func (store *Store) DeleteZone(ctx context.Context, request DeleteZoneRequest) (DeleteZoneResult, error) {
	if request.ZoneID == "" || request.IdempotencyKey == "" {
		return DeleteZoneResult{}, ErrInvalidRequest
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(int64(request.ExpectedRevision), 10)))
	hash := hex.EncodeToString(digest[:])
	result := DeleteZoneResult{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		replayed, revision, err := loadMutationReplay(db, mutationReplayQuery{
			zoneID: request.ZoneID, key: request.IdempotencyKey, operation: QueueCommand("delete_zone"), hash: hash,
		})
		if err != nil {
			return err
		}
		if replayed {
			result = DeleteZoneResult{ZoneID: request.ZoneID, Revision: revision, Replayed: true}
			return nil
		}
		inventory, found, err := loadZoneInventory(db, request.ZoneID)
		if err != nil {
			return err
		}
		if !found {
			return ErrZoneNotFound
		}
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		if zone.revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		result = DeleteZoneResult{ZoneID: request.ZoneID, Revision: max(zone.revision, inventory.Revision) + 1}
		for _, table := range []string{"playback_album_state", "playback_session_recordings"} {
			if err := execBound(db, "DELETE FROM "+table+" WHERE session_id IN (SELECT session_id FROM playback_sessions WHERE zone_id=?)", func(stmt *sqliteStmt) error {
				return stmt.bindText(1, string(request.ZoneID))
			}); err != nil {
				return err
			}
		}
		for _, table := range []string{
			"playback_start_failures", "playback_previous_history", "playback_automatic_previews",
			"playback_decision_attempts", "playback_decisions", "renderer_outbox", "playback_plays",
			"playback_sessions", "playback_queue", "playback_continuation_policies", "playback_tombstones",
			"renderer_assignments", "server_zones",
		} {
			if err := execBound(db, "DELETE FROM "+table+" WHERE zone_id=?", func(stmt *sqliteStmt) error {
				return stmt.bindText(1, string(request.ZoneID))
			}); err != nil {
				return err
			}
		}
		if err := execBound(db, "DELETE FROM playback_zones WHERE zone_id=?", func(stmt *sqliteStmt) error {
			return stmt.bindText(1, string(request.ZoneID))
		}); err != nil {
			return err
		}
		if err := execBound(db, "DELETE FROM playback_idempotency WHERE zone_id=?", func(stmt *sqliteStmt) error {
			return stmt.bindText(1, string(request.ZoneID))
		}); err != nil {
			return err
		}
		return recordQueueMutation(db, queueMutationRecord{request: QueueMutationRequest{
			ZoneID: request.ZoneID, IdempotencyKey: request.IdempotencyKey, Command: QueueCommand("delete_zone"),
		}, hash: hash, revision: result.Revision})
	})
	return result, err
}

func (store *Store) Zones(ctx context.Context) (ZonesSnapshot, error) {
	result := ZonesSnapshot{Zones: []Zone{}, Renderers: []RendererInventory{}}
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT zone_id FROM server_zones ORDER BY zone_id")
		if err != nil {
			return err
		}
		ids := []ZoneID{}
		for {
			row, err := stmt.step()
			if err != nil {
				stmt.close()
				return err
			}
			if !row {
				break
			}
			ids = append(ids, ZoneID(stmt.text(0)))
		}
		stmt.close()
		for _, id := range ids {
			zone, found, err := loadZoneInventory(db, id)
			if err != nil {
				return err
			}
			if found {
				result.Zones = append(result.Zones, zone)
			}
		}
		renderers, err := loadAllRenderers(db)
		if err != nil {
			return err
		}
		result.Renderers = renderers
		return nil
	})
	return result, err
}

func loadAllRenderers(db *sqliteDB) ([]RendererInventory, error) {
	stmt, err := db.prepare("SELECT renderer_id FROM renderer_registry ORDER BY renderer_id")
	if err != nil {
		return nil, err
	}
	ids := []RendererID{}
	for {
		row, err := stmt.step()
		if err != nil {
			stmt.close()
			return nil, err
		}
		if !row {
			break
		}
		ids = append(ids, RendererID(stmt.text(0)))
	}
	stmt.close()
	result := make([]RendererInventory, 0, len(ids))
	for _, id := range ids {
		renderer, found, err := loadRenderer(db, id)
		if err != nil {
			return nil, err
		}
		if found {
			result = append(result, renderer)
		}
	}
	return result, nil
}

func loadZoneInventory(db *sqliteDB, id ZoneID) (Zone, bool, error) {
	stmt, err := db.prepare(`SELECT z.display_name,z.revision,p.transport,a.renderer_id
		FROM server_zones z JOIN playback_zones p ON p.zone_id=z.zone_id
		LEFT JOIN renderer_assignments a ON a.zone_id=z.zone_id AND a.unassigned_revision IS NULL
		WHERE z.zone_id=?`)
	if err != nil {
		return Zone{}, false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(id)); err != nil {
		return Zone{}, false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return Zone{}, false, err
	}
	return Zone{ID: id, DisplayName: stmt.text(0), Revision: Revision(stmt.int64(1)), Transport: Transport(stmt.text(2)), RendererID: RendererID(stmt.text(3))}, true, nil
}
