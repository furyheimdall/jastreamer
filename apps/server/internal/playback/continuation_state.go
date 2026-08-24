package playback

import (
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/decision"
)

type continuationPlay struct {
	sessionID    SessionID
	source       string
	recordingKey string
	recordingID  catalog.RecordingID
	albumID      catalog.AlbumID
	order        catalog.OrderKey
}

func loadContinuationPlay(db *sqliteDB, playID PlayID) (continuationPlay, error) {
	stmt, err := db.prepare(`
		SELECT session_id,source,recording_key,recording_id,album_id,order_disc_known,order_disc_value,
			order_track_known,order_track_value,order_natural_path,order_track_id
		FROM playback_decision_attempts WHERE play_id=?`)
	if err != nil {
		return continuationPlay{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(playID)); err != nil {
		return continuationPlay{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return continuationPlay{}, err
	}
	if !row {
		return continuationPlay{}, ErrCorruptDatabase
	}
	return continuationPlay{
		sessionID: SessionID(stmt.text(0)), source: stmt.text(1), recordingKey: stmt.text(2),
		recordingID: catalog.RecordingID(stmt.text(3)), albumID: catalog.AlbumID(stmt.text(4)),
		order: catalog.OrderKey{
			Disc:        catalog.OrderedNumber{Known: stmt.int64(5) == 1, Value: int(stmt.int64(6))},
			Track:       catalog.OrderedNumber{Known: stmt.int64(7) == 1, Value: int(stmt.int64(8))},
			NaturalPath: stmt.text(9), TrackID: catalog.TrackID(stmt.text(10)),
		},
	}, nil
}

func recordStartedPlay(db *sqliteDB, playID PlayID) error {
	play, err := loadContinuationPlay(db, playID)
	if err != nil {
		return err
	}
	if play.recordingKey == "" {
		return ErrCorruptDatabase
	}
	return execBound(db, `
		INSERT OR IGNORE INTO playback_session_recordings(session_id,recording_key,recording_id)
		VALUES (?,?,?)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(play.sessionID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, play.recordingKey); err != nil {
			return err
		}
		return stmt.bindText(3, string(play.recordingID))
	})
}

func advanceAlbumState(db *sqliteDB, playID PlayID) error {
	play, err := loadContinuationPlay(db, playID)
	if err != nil {
		return err
	}
	if play.albumID == "" || play.order.TrackID == "" {
		return nil
	}
	switch play.source {
	case string(decision.SourceExplicit), string(decision.SourceAlbum), string(decision.SourceSimilar):
		return upsertAlbumState(db, play)
	default:
		return ErrCorruptDatabase
	}
}

func upsertAlbumState(db *sqliteDB, play continuationPlay) error {
	query := `
		INSERT INTO playback_album_state(
			session_id,album_id,order_disc_known,order_disc_value,order_track_known,
			order_track_value,order_natural_path,order_track_id
		) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET
			album_id=excluded.album_id,order_disc_known=excluded.order_disc_known,
			order_disc_value=excluded.order_disc_value,order_track_known=excluded.order_track_known,
			order_track_value=excluded.order_track_value,order_natural_path=excluded.order_natural_path,
			order_track_id=excluded.order_track_id`
	return execBound(db, query, func(stmt *sqliteStmt) error {
		texts := []string{
			string(play.sessionID), string(play.albumID), play.order.NaturalPath, string(play.order.TrackID),
		}
		textIndexes := []int{1, 2, 7, 8}
		for index, value := range texts {
			if err := stmt.bindText(textIndexes[index], value); err != nil {
				return err
			}
		}
		integers := []int64{
			boolInteger(play.order.Disc.Known), int64(play.order.Disc.Value),
			boolInteger(play.order.Track.Known), int64(play.order.Track.Value),
		}
		for index, value := range integers {
			if err := stmt.bindInt64(index+3, value); err != nil {
				return err
			}
		}
		return nil
	})
}
