package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/settings"
	"github.com/jastreamer/jastreamer-server/internal/transcode"
)

type ffmpegRuntimeConfig struct {
	path     string
	probes   *playback.Store
	settings *settings.Store
}

type ffmpegProvider interface {
	media.TransformProvider
	Close() error
}

func configureFFmpeg(ctx context.Context, config ffmpegRuntimeConfig) (provider ffmpegProvider, err error) {
	approval := transcode.Probe(ctx, config.path, 5*time.Second)
	defer func() { err = errors.Join(err, approval.Close()) }()
	diagnostic := approval.Diagnostic
	status := playback.FFmpegUnavailable
	switch diagnostic.Status {
	case transcode.StatusUnconfigured:
		status = playback.FFmpegUnconfigured
	case transcode.StatusAvailable:
		status = playback.FFmpegAvailable
	case transcode.StatusUnavailable:
		status = playback.FFmpegUnavailable
	case transcode.StatusIncompatible:
		status = playback.FFmpegIncompatible
	default:
		return nil, fmt.Errorf("unsupported FFmpeg probe status %q", diagnostic.Status)
	}
	if err := config.probes.SaveFFmpegProbe(ctx, playback.FFmpegProbe{
		ConfiguredPath: config.path, ExecutableFingerprint: diagnostic.SHA256,
		Status: status, Version: diagnostic.Version, Codecs: diagnostic.Decoders,
		ErrorCode: diagnostic.ErrorCode, ErrorDetail: diagnostic.Detail, ProbedAt: time.Now(),
	}); err != nil {
		return nil, fmt.Errorf("persist FFmpeg probe: %w", err)
	}
	warning := ""
	if diagnostic.Status != transcode.StatusAvailable {
		warning = "PCM fallback is disabled; original compatible media remains available."
	}
	config.settings.SetFFmpegDiagnostic(settings.FFmpegDiagnostic{
		ConfiguredPath: config.path, Status: string(diagnostic.Status), ExecutableSHA: diagnostic.SHA256,
		Version: diagnostic.Version, ErrorCode: diagnostic.ErrorCode, Warning: warning,
	})
	if diagnostic.Status != transcode.StatusAvailable {
		return nil, nil
	}
	provider, err = transcode.NewProvider(transcode.Config{Approval: approval})
	if err != nil {
		return nil, fmt.Errorf("configure FFmpeg fallback: %w", err)
	}
	return provider, nil
}
