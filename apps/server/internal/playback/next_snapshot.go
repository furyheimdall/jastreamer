package playback

import (
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
	"github.com/jastreamer/jastreamer-server/internal/decision"
)

func authoritativeSnapshot(
	db *sqliteDB,
	key boundaryKey,
	frozen decision.Snapshot,
) (decision.Snapshot, error) {
	mode, err := sessionPolicy(db, key.sessionID)
	if err != nil {
		return decision.Snapshot{}, err
	}
	policy, err := loadContinuationPolicy(db, key.zoneID)
	if err != nil {
		return decision.Snapshot{}, err
	}
	explicit, err := pendingExplicit(db, key.zoneID)
	if err != nil {
		return decision.Snapshot{}, err
	}
	failed, err := failedGeneratedTracks(db, key)
	if err != nil {
		return decision.Snapshot{}, err
	}
	frozen.Policy = mode
	frozen.Explicit = explicit
	frozen.FailedGenerated = failed
	frozen.Similar.RankingPolicy.ArtistGap = policy.ArtistGap
	frozen.Similar.RankingPolicy.AlbumGap = policy.AlbumGap
	if err := overlayAlbumState(db, key.sessionID, &frozen); err != nil {
		return decision.Snapshot{}, err
	}
	seen, err := sessionRecordingKeys(db, key.sessionID)
	if err != nil {
		return decision.Snapshot{}, err
	}
	if frozen.Similar.Seen == nil {
		frozen.Similar.Seen = make(map[candidates.RecordingKey]struct{}, len(seen))
	}
	for recordingKey := range seen {
		frozen.Similar.Seen[recordingKey] = struct{}{}
	}
	return frozen, nil
}

func pendingExplicit(db *sqliteDB, zoneID ZoneID) ([]decision.ExplicitEntry, error) {
	stmt, err := db.prepare(`
		SELECT entry_id,track_id,available FROM playback_queue
		WHERE zone_id=? AND state IN ('pending','blocked','reserved') ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return nil, err
	}
	var entries []decision.ExplicitEntry
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			return entries, nil
		}
		entries = append(entries, decision.ExplicitEntry{
			ID: decision.QueueEntryID(stmt.text(0)), TrackID: catalog.TrackID(stmt.text(1)),
			Available: stmt.int64(2) == 1,
		})
	}
}

func failedGeneratedTracks(db *sqliteDB, key boundaryKey) ([]catalog.TrackID, error) {
	stmt, err := db.prepare(`
		SELECT track_id FROM playback_start_failures
		WHERE zone_id=? AND session_id=? AND boundary_id=? AND source IN ('album','similar')
		ORDER BY failure_index`)
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	values := []string{string(key.zoneID), string(key.sessionID), string(key.boundaryID)}
	for index, value := range values {
		if err := stmt.bindText(index+1, value); err != nil {
			return nil, err
		}
	}
	var tracks []catalog.TrackID
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			return tracks, nil
		}
		tracks = append(tracks, catalog.TrackID(stmt.text(0)))
	}
}

func overlayAlbumState(db *sqliteDB, sessionID SessionID, snapshot *decision.Snapshot) error {
	stmt, err := db.prepare(`
		SELECT album_id,order_disc_known,order_disc_value,order_track_known,
			order_track_value,order_natural_path,order_track_id
		FROM playback_album_state WHERE session_id=?`)
	if err != nil {
		return err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(sessionID)); err != nil {
		return err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return err
	}
	started, err := sessionRecordings(db, sessionID)
	if err != nil {
		return err
	}
	snapshot.Album = decision.AlbumSnapshot{
		AlbumID: catalog.AlbumID(stmt.text(0)),
		Anchor: catalog.OrderKey{
			Disc:        catalog.OrderedNumber{Known: stmt.int64(1) == 1, Value: int(stmt.int64(2))},
			Track:       catalog.OrderedNumber{Known: stmt.int64(3) == 1, Value: int(stmt.int64(4))},
			NaturalPath: stmt.text(5), TrackID: catalog.TrackID(stmt.text(6)),
		},
		Started: started,
	}
	return nil
}

func sessionRecordings(db *sqliteDB, sessionID SessionID) (map[catalog.RecordingID]bool, error) {
	stmt, err := db.prepare(`
		SELECT recording_id FROM playback_session_recordings
		WHERE session_id=? AND recording_id!=''`)
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(sessionID)); err != nil {
		return nil, err
	}
	started := make(map[catalog.RecordingID]bool)
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			return started, nil
		}
		started[catalog.RecordingID(stmt.text(0))] = true
	}
}

func sessionRecordingKeys(
	db *sqliteDB,
	sessionID SessionID,
) (map[candidates.RecordingKey]struct{}, error) {
	stmt, err := db.prepare("SELECT recording_key FROM playback_session_recordings WHERE session_id=?")
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(sessionID)); err != nil {
		return nil, err
	}
	seen := make(map[candidates.RecordingKey]struct{})
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			return seen, nil
		}
		seen[candidates.RecordingKey(stmt.text(0))] = struct{}{}
	}
}
