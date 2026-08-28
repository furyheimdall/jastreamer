package playback

import "context"

func (store *Store) FailTransportDispatch(ctx context.Context, commandID string) (Revision, error) {
	var revision Revision
	err := store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT zone_id FROM renderer_outbox WHERE command_id=? AND failed_revision IS NULL")
		if err != nil {
			return err
		}
		if err := stmt.bindText(1, commandID); err != nil {
			stmt.close()
			return err
		}
		row, err := stmt.step()
		if err != nil || !row {
			stmt.close()
			if err != nil {
				return err
			}
			return nil
		}
		zoneID := ZoneID(stmt.text(0))
		stmt.close()
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		revision = zone.revision + 1
		if err := execBound(db, `UPDATE renderer_outbox SET state='confirmed',receipt_state='terminal',
			failed_revision=?,last_error_code='ADAPTER_FAILURE' WHERE command_id=?`, func(update *sqliteStmt) error {
			if err := update.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			return update.bindText(2, commandID)
		}); err != nil {
			return err
		}
		return updateTransportIntent(db, transportIntent{zoneID: zoneID, revision: revision, transport: TransportSuspended})
	})
	return revision, err
}
