package playback

import "context"

func (store *Store) RevokeRenderer(ctx context.Context, id RendererID) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		rendererRevision, _, found, err := rendererRevision(db, id)
		if err != nil {
			return err
		}
		if !found {
			return ErrRendererNotFound
		}
		inventory, loaded, err := loadRenderer(db, id)
		if err != nil {
			return err
		}
		if loaded && inventory.State == RendererRevoked {
			return nil
		}
		if err := execBound(db, `UPDATE renderer_registry SET state='revoked',revision=? WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(rendererRevision+1)); err != nil {
				return err
			}
			return stmt.bindText(2, string(id))
		}); err != nil {
			return err
		}
		if err := execBound(db, `UPDATE renderer_session_state SET connection_state='revoked'
			WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
			return stmt.bindText(1, string(id))
		}); err != nil {
			return err
		}
		zoneID, assigned, err := assignedZone(db, id)
		if err != nil || !assigned {
			return err
		}
		zone, found, err := loadZoneInventory(db, zoneID)
		if err != nil {
			return err
		}
		if !found {
			return ErrZoneNotFound
		}
		revision := zone.Revision + 1
		if err := closeAssignment(db, zoneID, revision); err != nil {
			return err
		}
		if err := updateServerZoneRevision(db, zoneID, revision); err != nil {
			return err
		}
		return execBound(db, `UPDATE playback_zones SET revision=revision+1,transport='suspended' WHERE zone_id=?`, func(stmt *sqliteStmt) error {
			return stmt.bindText(1, string(zoneID))
		})
	})
}

func assignedZone(db *sqliteDB, id RendererID) (ZoneID, bool, error) {
	stmt, err := db.prepare("SELECT zone_id FROM renderer_assignments WHERE renderer_id=? AND unassigned_revision IS NULL")
	if err != nil {
		return "", false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(id)); err != nil {
		return "", false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return "", false, err
	}
	return ZoneID(stmt.text(0)), true, nil
}
