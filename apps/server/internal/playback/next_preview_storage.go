package playback

func commitMatchingPreview(db *sqliteDB, stored storedDecisionAttempt) error {
	found, preview, err := loadAutomaticPreview(db, stored.zoneID, stored.boundaryID)
	if err != nil || !found {
		return err
	}
	if preview.state == "cancelled" {
		if stored.value.Source == "explicit" || stored.value.Kind != DecisionPlay {
			return nil
		}
		return ErrAutomaticPreempted
	}
	if preview.state == "committed" {
		if preview.playID != stored.value.PlayID {
			return ErrAutomaticConflict
		}
		return nil
	}
	if preview.trackID != stored.value.TrackID || preview.source != stored.value.Source ||
		preview.reason != stored.value.Reason {
		return ErrAutomaticConflict
	}
	return execBound(db, `
		UPDATE playback_automatic_previews
		SET state='committed',terminal_revision=?,play_id=?
		WHERE zone_id=? AND boundary_id=? AND state='preview'`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(stored.value.Revision)); err != nil {
			return err
		}
		values := []string{
			string(stored.value.PlayID), string(stored.zoneID), string(stored.boundaryID),
		}
		for index, value := range values {
			if err := stmt.bindText(index+2, value); err != nil {
				return err
			}
		}
		return nil
	})
}
