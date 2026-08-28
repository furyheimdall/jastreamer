package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (store *Store) Load(ctx context.Context) (snapshot Snapshot, err error) {
	snapshot = EmptySnapshot()
	if err := store.db.QueryRowContext(ctx, `
		SELECT generation,catalog_revision FROM catalog_scans
		WHERE root_id=? ORDER BY generation DESC LIMIT 1`, store.rootID,
	).Scan(&snapshot.Generation, &snapshot.Revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return snapshot, nil
		}
		return Snapshot{}, fmt.Errorf("load catalog generation: %w", err)
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT f.file_id,t.track_id,t.recording_id,t.album_id,f.relative_path,f.media_format,
			f.content_fingerprint,r.fallback_fingerprint,f.byte_size,f.modified_ns,
			t.title,t.artist,t.album_title,t.album_artist,r.embedded_recording_id,a.embedded_release_id,
			t.disc_number,t.track_number,t.natural_path_key,t.order_track_id,
			t.available,t.last_generation,n.status,n.content_fingerprint,n.feature_schema_version,
			n.analyzer_id,n.analyzer_version,n.normalizer_id,n.normalizer_version,n.failure_reason,n.feature_vector
		FROM catalog_tracks t
		JOIN catalog_files f ON f.file_id=t.file_id
		JOIN catalog_recordings r ON r.recording_id=t.recording_id
		JOIN catalog_albums a ON a.album_id=t.album_id
		JOIN catalog_analysis n ON n.track_id=t.track_id
		WHERE f.root_id=?`, store.rootID,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("query catalog tracks: %w", err)
	}
	for rows.Next() {
		track, scanErr := scanTrack(rows)
		if scanErr != nil {
			return Snapshot{}, errors.Join(scanErr, rows.Close())
		}
		track.RootID = RootID(store.rootID)
		snapshot.Tracks[track.TrackID] = track
	}
	if readErr := errors.Join(rows.Err(), rows.Close()); readErr != nil {
		return Snapshot{}, fmt.Errorf("read catalog tracks: %w", readErr)
	}
	if err := store.loadTrackTags(ctx, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (store *Store) loadTrackTags(ctx context.Context, snapshot *Snapshot) (err error) {
	rows, err := store.db.QueryContext(ctx, `SELECT track_id,tag_type,tag_value FROM catalog_track_tags ORDER BY track_id,tag_type,tag_value`)
	if err != nil {
		return fmt.Errorf("query catalog tags: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id TrackID
		var kind, value string
		if err := rows.Scan(&id, &kind, &value); err != nil {
			return fmt.Errorf("scan catalog tag: %w", err)
		}
		track, exists := snapshot.Tracks[id]
		if !exists {
			return fmt.Errorf("catalog tag references unknown track %q", id)
		}
		switch kind {
		case "genre":
			track.Metadata.Genres = append(track.Metadata.Genres, value)
		case "style":
			track.Metadata.Styles = append(track.Metadata.Styles, value)
		case "mood":
			track.Metadata.Moods = append(track.Metadata.Moods, value)
		case "local":
			track.Metadata.LocalTags = append(track.Metadata.LocalTags, value)
		default:
			return fmt.Errorf("unknown catalog tag type %q", kind)
		}
		snapshot.Tracks[id] = track
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate catalog tags: %w", err)
	}
	return nil
}

func scanTrack(rows *sql.Rows) (Track, error) {
	var (
		track                        Track
		available                    int
		modifiedNanoseconds          int64
		disc, number                 sql.NullInt64
		embeddedRecording, releaseID sql.NullString
	)
	if err := rows.Scan(
		&track.FileID, &track.TrackID, &track.RecordingID, &track.AlbumID,
		&track.RelativePath, &track.Format, &track.Fingerprint, &track.AudioFingerprint,
		&track.FileVersion.Size, &modifiedNanoseconds, &track.Metadata.Title, &track.Metadata.Artist,
		&track.Metadata.Album, &track.Metadata.AlbumArtist, &embeddedRecording, &releaseID,
		&disc, &number, &track.Order.NaturalPath, &track.Order.TrackID,
		&available, &track.Generation, &track.AnalysisStatus, &track.AnalysisFingerprint,
		&track.AnalysisProvenance.SchemaVersion, &track.AnalysisProvenance.AnalyzerID,
		&track.AnalysisProvenance.AnalyzerVersion, &track.AnalysisProvenance.NormalizerID,
		&track.AnalysisProvenance.NormalizerVersion, &track.AnalysisFailure, &track.AnalysisVector,
	); err != nil {
		return Track{}, fmt.Errorf("scan catalog track: %w", err)
	}
	track.Metadata.RecordingID = embeddedRecording.String
	track.Metadata.ReleaseID = releaseID.String
	track.Metadata.Disc = int(disc.Int64)
	track.Metadata.Track = int(number.Int64)
	track.Order.Disc = OrderedNumber{Known: disc.Valid, Value: int(disc.Int64)}
	track.Order.Track = OrderedNumber{Known: number.Valid, Value: int(number.Int64)}
	track.Available = available == 1
	track.FileVersion.Modified = time.Unix(0, modifiedNanoseconds).UTC()
	return track, nil
}
