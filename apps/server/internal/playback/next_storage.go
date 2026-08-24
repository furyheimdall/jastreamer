package playback

import (
	"encoding/json"
	"fmt"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/decision"
)

type storedDecisionAttempt struct {
	value        Decision
	zoneID       ZoneID
	sessionID    SessionID
	boundaryID   BoundaryID
	previousPlay PlayID
	recordingID  catalog.RecordingID
	albumID      catalog.AlbumID
	order        catalog.OrderKey
}

type playWrite struct {
	zoneID     ZoneID
	sessionID  SessionID
	boundaryID BoundaryID
	playID     PlayID
	queueEntry QueueEntryID
	trackID    TrackID
	revision   Revision
}

func makeDecisionAttempt(
	key boundaryKey,
	previousPlay PlayID,
	sequence int64,
	attempt int,
	revision Revision,
	outcome decision.Outcome,
) (storedDecisionAttempt, error) {
	value := Decision{
		ID:       fmt.Sprintf("%s:d:%020d", key.sessionID, sequence),
		Revision: revision, Attempt: attempt,
	}
	stored := storedDecisionAttempt{
		value: value, zoneID: key.zoneID, sessionID: key.sessionID,
		boundaryID: key.boundaryID, previousPlay: previousPlay,
	}
	switch selected := outcome.(type) {
	case decision.Play:
		stored.value.Kind = DecisionPlay
		stored.value.Reason = string(selected.Reason)
		stored.value.Source = string(selected.Source)
		stored.value.PlayID = PlayID(fmt.Sprintf("%s:p:%020d", key.sessionID, sequence))
		stored.value.QueueEntryID = QueueEntryID(selected.QueueEntryID)
		stored.value.TrackID = TrackID(selected.TrackID)
		stored.value.RecordingKey = string(selected.RecordingKey)
		stored.value.Explanation = selected.Explanation
		stored.recordingID = selected.RecordingID
		stored.albumID = selected.AlbumID
		stored.order = selected.Order
	case decision.Stop:
		stored.value.Kind = DecisionStop
		stored.value.Reason = string(selected.Reason)
	case decision.Block:
		stored.value.Kind = DecisionBlock
		stored.value.Reason = string(selected.Reason)
		stored.value.Source = string(decision.SourceExplicit)
		stored.value.QueueEntryID = QueueEntryID(selected.QueueEntryID)
		stored.value.TrackID = TrackID(selected.TrackID)
	default:
		return storedDecisionAttempt{}, fmt.Errorf("decision outcome %T: %w", outcome, ErrInvalidObservation)
	}
	return stored, nil
}

func insertDecisionAttempt(db *sqliteDB, stored storedDecisionAttempt, sequence int64) error {
	explanation := []byte("{}")
	if stored.value.Source == string(decision.SourceSimilar) {
		encoded, err := json.Marshal(stored.value.Explanation)
		if err != nil {
			return fmt.Errorf("encode ranking explanation: %w", err)
		}
		explanation = encoded
	}
	query := `
		INSERT INTO playback_decision_attempts(
			decision_id,zone_id,session_id,boundary_id,previous_play_id,attempt,sequence,
			kind,reason,source,play_id,queue_entry_id,track_id,recording_key,explanation_json,
			recording_id,album_id,order_disc_known,order_disc_value,order_track_known,
			order_track_value,order_natural_path,order_track_id,committed_revision
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	return execBound(db, query, func(stmt *sqliteStmt) error {
		values := []string{
			stored.value.ID, string(stored.zoneID), string(stored.sessionID), string(stored.boundaryID),
			string(stored.previousPlay), string(stored.value.Kind), stored.value.Reason, stored.value.Source,
			string(stored.value.PlayID), string(stored.value.QueueEntryID), string(stored.value.TrackID),
			stored.value.RecordingKey, string(explanation), string(stored.recordingID), string(stored.albumID),
			stored.order.NaturalPath, string(stored.order.TrackID),
		}
		indexes := []int{1, 2, 3, 4, 5, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 22, 23}
		for index, value := range values {
			column := indexes[index]
			if value == "" && (column == 11 || column == 12) {
				continue
			}
			if err := stmt.bindText(column, value); err != nil {
				return err
			}
		}
		integers := []int64{
			int64(stored.value.Attempt), sequence, boolInteger(stored.order.Disc.Known),
			int64(stored.order.Disc.Value), boolInteger(stored.order.Track.Known),
			int64(stored.order.Track.Value), int64(stored.value.Revision),
		}
		integerIndexes := []int{6, 7, 18, 19, 20, 21, 24}
		for index, value := range integers {
			if err := stmt.bindInt64(integerIndexes[index], value); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertReservedPlay(db *sqliteDB, write playWrite) error {
	if write.queueEntry != "" {
		if err := setQueueState(db, queueTransition{
			entryID: write.queueEntry, state: QueueReserved, playID: write.playID, revision: write.revision,
		}); err != nil {
			return err
		}
	}
	return execBound(db, `
		INSERT INTO playback_plays(
			play_id,zone_id,session_id,queue_entry_id,track_id,state,boundary_id
		) VALUES (?,?,?,?,?,'reserved',?)`, func(stmt *sqliteStmt) error {
		values := []string{
			string(write.playID), string(write.zoneID), string(write.sessionID),
			string(write.queueEntry), string(write.trackID), string(write.boundaryID),
		}
		for index, value := range values {
			if write.queueEntry == "" && index == 3 {
				continue
			}
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertPlayOutbox(db *sqliteDB, stored storedDecisionAttempt) error {
	return execBound(db, `
		INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision)
		VALUES (?,?,?,'play',?)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, stored.value.ID); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(stored.zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(3, string(stored.value.PlayID)); err != nil {
			return err
		}
		return stmt.bindInt64(4, int64(stored.value.Revision))
	})
}

func boolInteger(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
