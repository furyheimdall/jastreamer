package playback

func loadAutomaticPreview(
	db *sqliteDB,
	zoneID ZoneID,
	boundaryID BoundaryID,
) (bool, automaticPreview, error) {
	stmt, err := db.prepare(`
		SELECT previous_play_id,session_id,track_id,state,created_revision,
			terminal_revision,play_id
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
		created: Revision(stmt.int64(4)),
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

func reserveAutomaticPlay(db *sqliteDB, preview automaticPreview, decision Decision) error {
	if err := execBound(db, `
		INSERT INTO playback_plays(
			play_id,zone_id,session_id,queue_entry_id,track_id,state,boundary_id
		) VALUES (?,?,?,NULL,?,'reserved',?)`, func(stmt *sqliteStmt) error {
		values := []string{
			string(decision.PlayID), string(preview.zoneID), string(preview.sessionID),
			string(preview.trackID), string(preview.boundaryID),
		}
		for index, value := range values {
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return execBound(db, `
		INSERT INTO renderer_outbox(
			command_id,zone_id,play_id,command_type,created_revision
		) VALUES (?,?,?,'play',?)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, decision.ID); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(preview.zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(3, string(decision.PlayID)); err != nil {
			return err
		}
		return stmt.bindInt64(4, int64(decision.Revision))
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
