package playback

func closeAssignment(db *sqliteDB, zoneID ZoneID, revision Revision) error {
	return execBound(db, `UPDATE renderer_assignments SET unassigned_revision=?,unassigned_at=?
		WHERE zone_id=? AND unassigned_revision IS NULL`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, ""); err != nil {
			return err
		}
		return stmt.bindText(3, string(zoneID))
	})
}

type assignmentWrite struct {
	id       AssignmentID
	request  AssignmentRequest
	revision Revision
}

func insertAssignment(db *sqliteDB, write assignmentWrite) error {
	return execBound(db, `INSERT INTO renderer_assignments(
		assignment_id,zone_id,renderer_id,assigned_revision,assigned_at
	) VALUES (?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		for index, value := range []string{string(write.id), string(write.request.ZoneID), string(write.request.RendererID)} {
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		if err := stmt.bindInt64(4, int64(write.revision)); err != nil {
			return err
		}
		return stmt.bindText(5, "")
	})
}

func updateServerZoneRevision(db *sqliteDB, id ZoneID, revision Revision) error {
	return execBound(db, "UPDATE server_zones SET revision=?,updated_at=? WHERE zone_id=?", func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, ""); err != nil {
			return err
		}
		return stmt.bindText(3, string(id))
	})
}
