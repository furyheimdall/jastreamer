package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type persistSession struct {
	tx     *sql.Tx
	rootID string
	now    string
}

func (store *Store) Save(ctx context.Context, result ScanResult) (err error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin catalog save: %w", err)
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback catalog save: %w", rollbackErr))
		}
	}()
	now := store.now().UTC().Format(time.RFC3339Nano)
	session := persistSession{tx: tx, rootID: store.rootID, now: now}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO catalog_roots(root_id, canonical_path, created_at) VALUES(?,?,?)
		ON CONFLICT(root_id) DO UPDATE SET canonical_path=excluded.canonical_path`,
		store.rootID, store.root, now,
	); err != nil {
		return fmt.Errorf("save catalog root: %w", err)
	}
	completed := sql.NullString{String: now, Valid: result.Complete}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO catalog_scans(root_id,generation,started_at,completed_at,catalog_revision) VALUES(?,?,?,?,?)
		ON CONFLICT(root_id,generation) DO UPDATE SET completed_at=excluded.completed_at,catalog_revision=excluded.catalog_revision`,
		store.rootID, result.Snapshot.Generation, now, completed, result.Snapshot.Revision,
	); err != nil {
		return fmt.Errorf("save catalog scan: %w", err)
	}
	for _, track := range result.Snapshot.Tracks {
		if err := session.saveTrack(ctx, track); err != nil {
			return err
		}
	}
	for _, issue := range result.Issues {
		detail := ""
		if issue.Err != nil {
			detail = issue.Err.Error()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO catalog_scan_issues(root_id,generation,relative_path,issue_code,detail) VALUES(?,?,?,?,?)
			ON CONFLICT(root_id,generation,relative_path,issue_code) DO UPDATE SET detail=excluded.detail`,
			store.rootID, result.Snapshot.Generation, issue.Path, issue.Code, detail,
		); err != nil {
			return fmt.Errorf("save catalog issue %q: %w", issue.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog save: %w", err)
	}
	return nil
}

func (session persistSession) saveTrack(ctx context.Context, track Track) error {
	tombstone := sql.NullInt64{Int64: int64(track.Generation), Valid: !track.Available}
	if _, err := session.tx.ExecContext(ctx, `
		INSERT INTO catalog_files(
			file_id,root_id,relative_path,media_format,content_fingerprint,byte_size,modified_ns,
			available,first_generation,last_generation,tombstoned_generation
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(file_id) DO UPDATE SET
			relative_path=excluded.relative_path,media_format=excluded.media_format,
			content_fingerprint=excluded.content_fingerprint,byte_size=excluded.byte_size,
			modified_ns=excluded.modified_ns,available=excluded.available,
			last_generation=excluded.last_generation,tombstoned_generation=excluded.tombstoned_generation`,
		track.FileID, session.rootID, track.RelativePath, track.Format, track.Fingerprint,
		track.FileVersion.Size, track.FileVersion.Modified.UnixNano(), boolInteger(track.Available),
		track.Generation, track.Generation, tombstone,
	); err != nil {
		return fmt.Errorf("save catalog file %q: %w", track.RelativePath, err)
	}
	if _, err := session.tx.ExecContext(ctx, `
		INSERT INTO catalog_recordings(recording_id,embedded_recording_id,fallback_fingerprint,normalized_title,normalized_primary_artist)
		VALUES(?,?,?,?,?)
		ON CONFLICT(recording_id) DO UPDATE SET embedded_recording_id=excluded.embedded_recording_id,
			fallback_fingerprint=excluded.fallback_fingerprint,normalized_title=excluded.normalized_title,
			normalized_primary_artist=excluded.normalized_primary_artist`,
		track.RecordingID, nullableText(track.Metadata.RecordingID), track.AudioFingerprint,
		normalize(track.Metadata.Title), normalize(track.Metadata.Artist),
	); err != nil {
		return fmt.Errorf("save catalog recording %q: %w", track.RecordingID, err)
	}
	if _, err := session.tx.ExecContext(ctx, `
		INSERT INTO catalog_albums(album_id,embedded_release_id,normalized_title,normalized_album_artist,directory_boundary)
		VALUES(?,?,?,?,?)
		ON CONFLICT(album_id) DO UPDATE SET embedded_release_id=excluded.embedded_release_id,
			normalized_title=excluded.normalized_title,normalized_album_artist=excluded.normalized_album_artist,
			directory_boundary=excluded.directory_boundary`,
		track.AlbumID, nullableText(track.Metadata.ReleaseID), normalize(track.Metadata.Album),
		normalize(track.Metadata.AlbumArtist), parentPath(track.RelativePath),
	); err != nil {
		return fmt.Errorf("save catalog album %q: %w", track.AlbumID, err)
	}
	return session.saveTrackAndAnalysis(ctx, track)
}

func (session persistSession) saveTrackAndAnalysis(ctx context.Context, track Track) error {
	if _, err := session.tx.ExecContext(ctx, `
		INSERT INTO catalog_tracks(
			track_id,file_id,recording_id,album_id,title,artist,album_title,album_artist,
			disc_number,track_number,natural_path_key,order_track_id,available,last_generation
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(track_id) DO UPDATE SET recording_id=excluded.recording_id,album_id=excluded.album_id,
			title=excluded.title,artist=excluded.artist,album_title=excluded.album_title,
			album_artist=excluded.album_artist,disc_number=excluded.disc_number,
			track_number=excluded.track_number,natural_path_key=excluded.natural_path_key,
			order_track_id=excluded.order_track_id,available=excluded.available,last_generation=excluded.last_generation`,
		track.TrackID, track.FileID, track.RecordingID, track.AlbumID, track.Metadata.Title,
		track.Metadata.Artist, track.Metadata.Album, track.Metadata.AlbumArtist,
		nullablePositive(track.Metadata.Disc), nullablePositive(track.Metadata.Track),
		track.Order.NaturalPath, track.Order.TrackID, boolInteger(track.Available), track.Generation,
	); err != nil {
		return fmt.Errorf("save catalog track %q: %w", track.TrackID, err)
	}
	if _, err := session.tx.ExecContext(ctx, `
		INSERT INTO catalog_analysis(track_id,content_fingerprint,status,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(track_id) DO UPDATE SET content_fingerprint=excluded.content_fingerprint,
			status=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN 'queued' ELSE catalog_analysis.status END,
			failure_reason=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN '' ELSE catalog_analysis.failure_reason END,
			feature_vector=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN X'' ELSE catalog_analysis.feature_vector END,
			feature_schema_version=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN 0 ELSE catalog_analysis.feature_schema_version END,
			analyzer_id=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN '' ELSE catalog_analysis.analyzer_id END,
			analyzer_version=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN '' ELSE catalog_analysis.analyzer_version END,
			normalizer_id=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN '' ELSE catalog_analysis.normalizer_id END,
			normalizer_version=CASE WHEN catalog_analysis.content_fingerprint<>excluded.content_fingerprint THEN '' ELSE catalog_analysis.normalizer_version END,
			updated_at=excluded.updated_at`,
		track.TrackID, track.AudioFingerprint, AnalysisQueued, session.now,
	); err != nil {
		return fmt.Errorf("save catalog analysis %q: %w", track.TrackID, err)
	}
	return nil
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullablePositive(value int) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(value), Valid: value > 0}
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
