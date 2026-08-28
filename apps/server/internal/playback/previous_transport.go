package playback

type transportMutationPlan struct {
	physicalCommand TransportCommand
	history         *previousHistoryEntry
}

func planTransportMutation(db *sqliteDB, request TransportMutationRequest, zone zoneRecord) (transportMutationPlan, error) {
	plan := transportMutationPlan{physicalCommand: request.Command}
	if request.Command != TransportPrevious {
		return plan, nil
	}
	if zone.transport != TransportPlaying && zone.transport != TransportPaused {
		return transportMutationPlan{}, ErrInvalidTransition
	}
	if request.PositionMS > 5_000 {
		plan.physicalCommand = TransportSeek
		return plan, nil
	}
	history, err := latestPreviousHistory(db, request.ZoneID)
	if err != nil {
		return transportMutationPlan{}, err
	}
	plan.physicalCommand = TransportPlay
	plan.history = &history
	return plan, nil
}

func retireCurrentForPrevious(db *sqliteDB, zoneID ZoneID, playID PlayID, revision Revision) error {
	if err := execBound(db, `UPDATE playback_plays SET state='completed',terminal_revision=?
		WHERE zone_id=? AND play_id=? AND state IN ('reserved','playing')`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(zoneID)); err != nil {
			return err
		}
		return stmt.bindText(3, string(playID))
	}); err != nil {
		return err
	}
	return execBound(db, `UPDATE playback_queue SET state='completed',reserved_play_id=NULL,terminal_revision=?
		WHERE zone_id=? AND reserved_play_id=? AND state IN ('reserved','playing')`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(zoneID)); err != nil {
			return err
		}
		return stmt.bindText(3, string(playID))
	})
}

func updatePreviousTransportIntent(db *sqliteDB, zoneID ZoneID, revision Revision, playID PlayID) error {
	return execBound(db, `UPDATE playback_zones SET revision=?,transport='starting',current_play_id=?
		WHERE zone_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(playID)); err != nil {
			return err
		}
		return stmt.bindText(3, string(zoneID))
	})
}

func loadTransportReplayResult(db *sqliteDB, request TransportMutationRequest, result *TransportMutationResult) error {
	if request.Command != TransportPrevious {
		var err error
		result.CommandID, result.PlayID, result.TrackID, err = transportResultAtRevision(db, transportResultQuery{
			zoneID: request.ZoneID, revision: result.Revision, command: request.Command,
		})
		return err
	}
	stmt, err := db.prepare(`SELECT o.command_id,o.play_id,COALESCE(p.track_id,''),
		COALESCE(NULLIF(o.transport_kind,''),o.command_type)
		FROM renderer_outbox o LEFT JOIN playback_plays p ON p.play_id=o.play_id
		WHERE o.zone_id=? AND o.created_revision=? ORDER BY o.command_id LIMIT 1`)
	if err != nil {
		return err
	}
	if err := stmt.bindText(1, string(request.ZoneID)); err != nil {
		stmt.close()
		return err
	}
	if err := stmt.bindInt64(2, int64(result.Revision)); err != nil {
		stmt.close()
		return err
	}
	row, err := stmt.step()
	if err != nil || !row {
		stmt.close()
		if err != nil {
			return err
		}
		return ErrCorruptDatabase
	}
	result.CommandID, result.PlayID = stmt.text(0), PlayID(stmt.text(1))
	result.TrackID, result.PhysicalCommand = TrackID(stmt.text(2)), TransportCommand(stmt.text(3))
	stmt.close()
	if result.PhysicalCommand != TransportPlay {
		return nil
	}
	history, err := previousHistoryAtRevision(db, request.ZoneID, result.Revision)
	if err != nil {
		return err
	}
	result.QueueEntryID, result.SourcePlayID = history.sourceQueue, history.sourcePlay
	return nil
}

func previousHistoryAtRevision(db *sqliteDB, zoneID ZoneID, revision Revision) (previousHistoryEntry, error) {
	stmt, err := db.prepare(`SELECT history_id,source_play_id,COALESCE(source_queue_entry_id,''),track_id
		FROM playback_previous_history WHERE zone_id=? AND consumed_revision=? LIMIT 1`)
	if err != nil {
		return previousHistoryEntry{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return previousHistoryEntry{}, err
	}
	if err := stmt.bindInt64(2, int64(revision)); err != nil {
		return previousHistoryEntry{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return previousHistoryEntry{}, err
	}
	if !row {
		return previousHistoryEntry{}, ErrCorruptDatabase
	}
	return previousHistoryEntry{
		historyID: stmt.text(0), sourcePlay: PlayID(stmt.text(1)),
		sourceQueue: QueueEntryID(stmt.text(2)), trackID: TrackID(stmt.text(3)),
	}, nil
}
