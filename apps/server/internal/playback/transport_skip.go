package playback

import "context"

func (store *Store) CompleteAcknowledgedSkip(ctx context.Context, commandID, boundaryID string) (Decision, error) {
	result := Decision{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT zone_id,play_id,state FROM renderer_outbox
			WHERE command_id=? AND COALESCE(NULLIF(transport_kind,''),command_type)='skip'`)
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
			return ErrInvalidObservation
		}
		zoneID, playID, state := ZoneID(stmt.text(0)), PlayID(stmt.text(1)), stmt.text(2)
		stmt.close()
		if state == "confirmed" {
			loaded, found, err := loadLatestDecisionForBoundary(db, zoneID, BoundaryID(boundaryID))
			if err != nil || !found {
				return err
			}
			result = loaded
			return nil
		}
		if err := execBound(db, `UPDATE renderer_outbox SET state='confirmed',receipt_state='terminal'
			WHERE command_id=?`, func(update *sqliteStmt) error { return update.bindText(1, commandID) }); err != nil {
			return err
		}
		commit := acknowledgedSkip{zoneID: zoneID, playID: playID, boundaryID: BoundaryID(boundaryID)}
		result, err = store.commitAcknowledgedSkip(db, commit)
		return err
	})
	return result, err
}

type acknowledgedSkip struct {
	zoneID     ZoneID
	playID     PlayID
	boundaryID BoundaryID
}

func (store *Store) commitAcknowledgedSkip(db *sqliteDB, commit acknowledgedSkip) (Decision, error) {
	zoneID, playID, boundaryID := commit.zoneID, commit.playID, commit.boundaryID
	zone, err := loadZone(db, zoneID)
	if err != nil {
		return Decision{}, err
	}
	revision := zone.revision + 1
	if err := completePlay(db, playCompletion{zoneID: zoneID, playID: playID, revision: revision}); err != nil {
		return Decision{}, err
	}
	if err := execBound(db, `UPDATE playback_zones SET transport='selecting',current_play_id=NULL
		WHERE zone_id=?`, func(stmt *sqliteStmt) error { return stmt.bindText(1, string(zoneID)) }); err != nil {
		return Decision{}, err
	}
	return store.commitNext(db, NextRequest{ZoneID: zoneID, Boundary: Boundary{ID: boundaryID}})
}

func loadLatestDecisionForBoundary(db *sqliteDB, zoneID ZoneID, boundaryID BoundaryID) (Decision, bool, error) {
	stmt, err := db.prepare(`SELECT decision_id FROM playback_decision_attempts WHERE zone_id=? AND boundary_id=?
		ORDER BY committed_revision DESC,attempt DESC LIMIT 1`)
	if err != nil {
		return Decision{}, false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return Decision{}, false, err
	}
	if err := stmt.bindText(2, string(boundaryID)); err != nil {
		return Decision{}, false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return Decision{}, false, err
	}
	found, decision, err := loadDecisionByID(db, stmt.text(0))
	return decision, found, err
}
