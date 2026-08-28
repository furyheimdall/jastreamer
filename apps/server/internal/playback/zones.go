package playback

import (
	"context"
	"errors"
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

func (store *Store) CreateZone(ctx context.Context, input ZoneDefinition) (Zone, error) {
	if input.ID == "" || input.DisplayName == "" {
		return Zone{}, ErrInvalidZone
	}
	result := Zone{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
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
