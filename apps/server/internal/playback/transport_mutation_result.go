package playback

type transportResultQuery struct {
	zoneID   ZoneID
	revision Revision
	command  TransportCommand
}

func transportResultAtRevision(db *sqliteDB, query transportResultQuery) (string, PlayID, TrackID, error) {
	kind := string(query.command)
	if query.command == TransportStart {
		kind = "play"
	}
	stmt, err := db.prepare(`SELECT o.command_id,o.play_id,COALESCE(p.track_id,'') FROM renderer_outbox o
		LEFT JOIN playback_plays p ON p.play_id=o.play_id WHERE o.zone_id=?
		AND o.created_revision=? AND COALESCE(NULLIF(o.transport_kind,''),o.command_type)=? ORDER BY o.command_id LIMIT 1`)
	if err != nil {
		return "", "", "", err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(query.zoneID)); err != nil {
		return "", "", "", err
	}
	if err := stmt.bindInt64(2, int64(query.revision)); err != nil {
		return "", "", "", err
	}
	if err := stmt.bindText(3, kind); err != nil {
		return "", "", "", err
	}
	row, err := stmt.step()
	if err != nil {
		return "", "", "", err
	}
	if !row {
		return "", "", "", ErrCorruptDatabase
	}
	return stmt.text(0), PlayID(stmt.text(1)), TrackID(stmt.text(2)), nil
}
