package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
)

func (store *Store) ScheduleAnalysis(ctx context.Context, p analysis.Provenance) (jobs []AnalysisJob, err error) {
	now := store.now().UTC().Format(time.RFC3339Nano)
	_, err = store.db.ExecContext(ctx, `UPDATE catalog_analysis SET status='queued',failure_reason='',feature_vector=X'',updated_at=?
		WHERE status='running' OR feature_schema_version<>? OR analyzer_id<>? OR analyzer_version<>? OR normalizer_id<>? OR normalizer_version<>?`, now, p.SchemaVersion, p.AnalyzerID, p.AnalyzerVersion, p.NormalizerID, p.NormalizerVersion)
	if err != nil {
		return nil, fmt.Errorf("schedule analysis: %w", err)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT a.track_id,a.content_fingerprint,f.relative_path,a.status FROM catalog_analysis a JOIN catalog_tracks t ON t.track_id=a.track_id JOIN catalog_files f ON f.file_id=t.file_id WHERE a.status='queued' AND t.available=1 ORDER BY a.track_id`)
	if err != nil {
		return nil, fmt.Errorf("query analysis jobs: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var j AnalysisJob
		if err := rows.Scan(&j.TrackID, &j.Fingerprint, &j.RelativePath, &j.Status); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (store *Store) ClaimAnalysis(ctx context.Context, limit int, p analysis.Provenance) (jobs []AnalysisJob, err error) {
	if limit < 1 {
		return nil, nil
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rollback := tx.Rollback(); rollback != nil && !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	rows, err := tx.QueryContext(ctx, `SELECT a.track_id,a.content_fingerprint,f.relative_path FROM catalog_analysis a JOIN catalog_tracks t ON t.track_id=a.track_id JOIN catalog_files f ON f.file_id=t.file_id WHERE a.status='queued' ORDER BY a.track_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var j AnalysisJob
		j.Status = AnalysisRunning
		if err = rows.Scan(&j.TrackID, &j.Fingerprint, &j.RelativePath); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		jobs = append(jobs, j)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for _, j := range jobs {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_analysis SET status='running',feature_schema_version=?,analyzer_id=?,analyzer_version=?,normalizer_id=?,normalizer_version=?,updated_at=? WHERE track_id=? AND status='queued'`, p.SchemaVersion, p.AnalyzerID, p.AnalyzerVersion, p.NormalizerID, p.NormalizerVersion, store.now().UTC().Format(time.RFC3339Nano), j.TrackID)
		if err != nil {
			return nil, err
		}
	}
	return jobs, tx.Commit()
}

func (store *Store) FinishAnalysis(ctx context.Context, id TrackID, p analysis.Provenance, fingerprint string, vector []byte, reason string) error {
	status := AnalysisComplete
	if reason != "" {
		status = AnalysisFailed
		vector = []byte{}
	}
	result, err := store.db.ExecContext(ctx, `UPDATE catalog_analysis SET status=?,failure_reason=?,feature_vector=?,updated_at=? WHERE track_id=? AND content_fingerprint=? AND feature_schema_version=? AND analyzer_id=? AND analyzer_version=? AND normalizer_id=? AND normalizer_version=?`, status, reason, vector, store.now().UTC().Format(time.RFC3339Nano), id, fingerprint, p.SchemaVersion, p.AnalyzerID, p.AnalyzerVersion, p.NormalizerID, p.NormalizerVersion)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read finished analysis rows: %w", err)
	}
	if n != 1 {
		return errors.New("catalog: stale analysis result")
	}
	return nil
}

// FeatureVector never decodes; selection only sees complete persisted vectors.
func (store *Store) FeatureVector(ctx context.Context, id TrackID) ([]byte, bool, error) {
	var vector []byte
	err := store.db.QueryRowContext(ctx, `SELECT feature_vector FROM catalog_analysis WHERE track_id=? AND status='complete'`, id).Scan(&vector)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return vector, err == nil, err
}
func (store *Store) analysisPath(relative string) string {
	return filepath.Join(store.root, filepath.FromSlash(relative))
}
