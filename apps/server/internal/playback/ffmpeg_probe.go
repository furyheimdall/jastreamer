package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxFFmpegErrorDetailBytes = 4096

func (store *Store) SaveFFmpegProbe(ctx context.Context, probe FFmpegProbe) error {
	if !validFFmpegStatus(probe.Status) {
		return fmt.Errorf("invalid FFmpeg probe status %q", probe.Status)
	}
	codecsValue := probe.Codecs
	if codecsValue == nil {
		codecsValue = []string{}
	}
	codecs, err := json.Marshal(codecsValue)
	if err != nil {
		return fmt.Errorf("encode FFmpeg codecs: %w", err)
	}
	detail := probe.ErrorDetail
	if probe.ConfiguredPath != "" {
		detail = strings.ReplaceAll(detail, probe.ConfiguredPath, "[redacted]")
	}
	if len(detail) > maxFFmpegErrorDetailBytes {
		detail = detail[:maxFFmpegErrorDetailBytes]
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		return execBound(db, `UPDATE ffmpeg_probe_status SET configured_path=?,executable_fingerprint=?,status=?,version=?,codecs_json=?,error_code=?,error_detail=?,revision=revision+1,probed_at=? WHERE singleton=1`, func(stmt *sqliteStmt) error {
			values := []string{probe.ConfiguredPath, probe.ExecutableFingerprint, string(probe.Status), probe.Version, string(codecs), probe.ErrorCode, detail, probe.ProbedAt.UTC().Format(time.RFC3339Nano)}
			for index, value := range values {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func (store *Store) FFmpegProbe(ctx context.Context) (FFmpegProbe, error) {
	var probe FFmpegProbe
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT configured_path,executable_fingerprint,status,version,codecs_json,error_code,error_detail,revision,probed_at FROM ffmpeg_probe_status WHERE singleton=1`)
		if err != nil {
			return err
		}
		defer stmt.close()
		row, err := stmt.step()
		if err != nil || !row {
			return errors.Join(err, ErrCorruptDatabase)
		}
		probe = FFmpegProbe{
			ConfiguredPath: stmt.text(0), ExecutableFingerprint: stmt.text(1), Status: FFmpegStatus(stmt.text(2)),
			Version: stmt.text(3), ErrorCode: stmt.text(5), ErrorDetail: stmt.text(6), Revision: Revision(stmt.int64(7)),
		}
		if err := json.Unmarshal([]byte(stmt.text(4)), &probe.Codecs); err != nil {
			return fmt.Errorf("decode FFmpeg codecs: %w", err)
		}
		if stmt.text(8) != "" {
			probe.ProbedAt, err = time.Parse(time.RFC3339Nano, stmt.text(8))
			if err != nil {
				return fmt.Errorf("decode FFmpeg probe time: %w", err)
			}
		}
		return nil
	})
	return probe, err
}

func validFFmpegStatus(status FFmpegStatus) bool {
	switch status {
	case FFmpegUnconfigured, FFmpegAvailable, FFmpegUnavailable, FFmpegIncompatible:
		return true
	default:
		return false
	}
}
