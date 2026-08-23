package playback

func loadAttemptByPlay(db *sqliteDB, zoneID ZoneID, playID PlayID) (storedDecisionAttempt, error) {
	stmt, err := db.prepare(`SELECT ` + decisionAttemptColumns + `,session_id,boundary_id
		FROM playback_decision_attempts WHERE zone_id=? AND play_id=?`)
	if err != nil {
		return storedDecisionAttempt{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return storedDecisionAttempt{}, err
	}
	if err := stmt.bindText(2, string(playID)); err != nil {
		return storedDecisionAttempt{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return storedDecisionAttempt{}, err
	}
	if !row {
		return storedDecisionAttempt{}, ErrStartFailure
	}
	value, err := decisionFromRow(stmt)
	if err != nil {
		return storedDecisionAttempt{}, err
	}
	return storedDecisionAttempt{
		value: value, zoneID: zoneID,
		sessionID: SessionID(stmt.text(12)), boundaryID: BoundaryID(stmt.text(13)),
		previousPlay: PlayID(stmt.text(11)),
	}, nil
}

func loadFailureReplay(db *sqliteDB, request StartFailureRequest) (bool, Decision, error) {
	stmt, err := db.prepare(`
		SELECT result_decision_id,zone_id,boundary_id,failed_play_id
		FROM playback_start_failures WHERE failed_play_id=?`)
	if err != nil {
		return false, Decision{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(request.PlayID)); err != nil {
		return false, Decision{}, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return row, Decision{}, err
	}
	if ZoneID(stmt.text(1)) != request.ZoneID || BoundaryID(stmt.text(2)) != request.BoundaryID ||
		PlayID(stmt.text(3)) != request.PlayID {
		return false, Decision{}, ErrStartFailure
	}
	found, replay, err := loadDecisionByID(db, stmt.text(0))
	if err != nil {
		return false, Decision{}, err
	}
	if !found {
		return false, Decision{}, ErrCorruptDatabase
	}
	return true, replay, nil
}

func terminalizeFailedStart(db *sqliteDB, stored storedDecisionAttempt, revision Revision) error {
	if err := execBound(db, `
		UPDATE renderer_outbox SET failed_revision=?
		WHERE zone_id=? AND play_id=? AND failed_revision IS NULL`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(stored.zoneID)); err != nil {
			return err
		}
		return stmt.bindText(3, string(stored.value.PlayID))
	}); err != nil {
		return err
	}
	return execBound(db, `
		UPDATE playback_plays SET state='stopped',terminal_revision=?
		WHERE play_id=? AND state='reserved'`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(stored.value.PlayID))
	})
}

func insertStartFailure(
	db *sqliteDB,
	failed storedDecisionAttempt,
	result Decision,
	failureIndex int,
) error {
	return execBound(db, `
		INSERT INTO playback_start_failures(
			failed_play_id,zone_id,session_id,boundary_id,track_id,source,
			failure_index,result_decision_id,failed_revision
		) VALUES (?,?,?,?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		values := []string{
			string(failed.value.PlayID), string(failed.zoneID), string(failed.sessionID),
			string(failed.boundaryID), string(failed.value.TrackID), failed.value.Source, result.ID,
		}
		indexes := []int{1, 2, 3, 4, 5, 6, 8}
		for index, value := range values {
			if err := stmt.bindText(indexes[index], value); err != nil {
				return err
			}
		}
		if err := stmt.bindInt64(7, int64(failureIndex)); err != nil {
			return err
		}
		return stmt.bindInt64(9, int64(result.Revision))
	})
}
