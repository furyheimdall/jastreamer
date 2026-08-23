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
			t.available,t.last_generation,n.status
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
	defer func() {
		err = errors.Join(err, rows.Close())
	}()
	for rows.Next() {
		track, err := scanTrack(rows)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Tracks[track.TrackID] = track
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("iterate catalog tracks: %w", err)
	}
	return snapshot, nil
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
		&available, &track.Generation, &track.AnalysisStatus,
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
