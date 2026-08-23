package playback

import (
	"encoding/json"
	"fmt"
)

const decisionAttemptColumns = `
	decision_id,kind,reason,source,play_id,queue_entry_id,track_id,
	recording_key,explanation_json,committed_revision,attempt,previous_play_id`

func loadLatestBoundaryDecision(
	db *sqliteDB,
	key boundaryKey,
	previousPlay PlayID,
) (bool, Decision, error) {
	stmt, err := db.prepare(`SELECT ` + decisionAttemptColumns + `
		FROM playback_decision_attempts
		WHERE zone_id=? AND session_id=? AND boundary_id=?
		ORDER BY attempt DESC LIMIT 1`)
	if err != nil {
		return false, Decision{}, err
	}
	defer stmt.close()
	values := []string{string(key.zoneID), string(key.sessionID), string(key.boundaryID)}
	for index, value := range values {
		if err := stmt.bindText(index+1, value); err != nil {
			return false, Decision{}, err
		}
	}
	row, err := stmt.step()
	if err != nil || !row {
		return row, Decision{}, err
	}
	if PlayID(stmt.text(11)) != previousPlay {
		return false, Decision{}, ErrBoundaryConflict
	}
	value, err := decisionFromRow(stmt)
	return true, value, err
}

func loadDecisionByID(db *sqliteDB, decisionID string) (bool, Decision, error) {
	stmt, err := db.prepare(`SELECT ` + decisionAttemptColumns + `
		FROM playback_decision_attempts WHERE decision_id=?`)
	if err != nil {
		return false, Decision{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, decisionID); err != nil {
		return false, Decision{}, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return row, Decision{}, err
	}
	value, err := decisionFromRow(stmt)
	return true, value, err
}

func decisionFromRow(stmt *sqliteStmt) (Decision, error) {
	value := Decision{
		ID: stmt.text(0), Kind: DecisionKind(stmt.text(1)), Reason: stmt.text(2), Source: stmt.text(3),
		PlayID: PlayID(stmt.text(4)), QueueEntryID: QueueEntryID(stmt.text(5)), TrackID: TrackID(stmt.text(6)),
		RecordingKey: stmt.text(7), Revision: Revision(stmt.int64(9)), Attempt: int(stmt.int64(10)),
	}
	if raw := stmt.text(8); raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &value.Explanation); err != nil {
			return Decision{}, fmt.Errorf("decode ranking explanation: %w", err)
		}
	}
	return value, nil
}

func countBoundaryAttempts(db *sqliteDB, key boundaryKey) (int, error) {
	stmt, err := db.prepare(`
		SELECT count(*) FROM playback_decision_attempts
		WHERE zone_id=? AND session_id=? AND boundary_id=?`)
	if err != nil {
		return 0, err
	}
	defer stmt.close()
	values := []string{string(key.zoneID), string(key.sessionID), string(key.boundaryID)}
	for index, value := range values {
		if err := stmt.bindText(index+1, value); err != nil {
			return 0, err
		}
	}
	row, err := stmt.step()
	if err != nil {
		return 0, err
	}
	if !row {
		return 0, ErrCorruptDatabase
	}
	return int(stmt.int64(0)), nil
}
