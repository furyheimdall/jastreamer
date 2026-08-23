package playback

func loadAutomaticPreview(
	db *sqliteDB,
	zoneID ZoneID,
	boundaryID BoundaryID,
) (bool, automaticPreview, error) {
	stmt, err := db.prepare(`
		SELECT previous_play_id,session_id,track_id,state,created_revision,
			terminal_revision,play_id,COALESCE(source,''),COALESCE(reason,'')
		FROM playback_automatic_previews WHERE zone_id=? AND boundary_id=?`)
	if err != nil {
		return false, automaticPreview{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return false, automaticPreview{}, err
	}
	if err := stmt.bindText(2, string(boundaryID)); err != nil {
		return false, automaticPreview{}, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return row, automaticPreview{}, err
	}
	preview := automaticPreview{
		zoneID: zoneID, boundaryID: boundaryID,
		previousPlay: PlayID(stmt.text(0)), sessionID: SessionID(stmt.text(1)),
		trackID: TrackID(stmt.text(2)), state: stmt.text(3),
		created: Revision(stmt.int64(4)), source: stmt.text(7), reason: stmt.text(8),
	}
	if !stmt.isNull(5) {
		preview.terminal = Revision(stmt.int64(5))
	}
	if !stmt.isNull(6) {
		preview.playID = PlayID(stmt.text(6))
	}
	return true, preview, nil
}

func cancelAutomaticPreviews(db *sqliteDB, zoneID ZoneID, revision Revision) error {
	return execBound(db, `
		UPDATE playback_automatic_previews
		SET state='cancelled',terminal_revision=?
		WHERE zone_id=? AND state='preview'`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	})
}

func updateZoneRevision(db *sqliteDB, zoneID ZoneID, revision Revision) error {
	return execBound(db, "UPDATE playback_zones SET revision=? WHERE zone_id=?", func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	})
}
