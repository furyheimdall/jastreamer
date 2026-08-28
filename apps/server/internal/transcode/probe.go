package transcode

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxProbeOutputBytes = 1 << 20

var requiredDecoders = map[string]string{
	"flac": "flac", "mp3": "mp3", "vorbis": "vorbis", "opus": "opus", "wav": "pcm_s16le",
}

func Probe(ctx context.Context, path string, timeout time.Duration) *Approval {
	if path == "" {
		return newApproval(diagnosticFailure(StatusUnconfigured, "unconfigured", "no executable configured"), nil)
	}
	if !filepath.IsAbs(path) {
		return newApproval(diagnosticFailure(StatusUnavailable, "path_not_absolute", "configured executable path is not absolute"), nil)
	}
	if validateTimeout(timeout) != nil {
		return newApproval(diagnosticFailure(StatusUnavailable, "invalid_timeout", "probe timeout is invalid"), nil)
	}
	executable, err := bindExecutable(filepath.Clean(path))
	if err != nil {
		return newApproval(bindingDiagnostic(err), nil)
	}
	diagnostic := probeExecutable(ctx, executable, timeout)
	if diagnostic.Status == StatusAvailable {
		return newApproval(diagnostic, executable)
	}
	if err := executable.Close(); err != nil {
		return newApproval(bindingDiagnostic(bindingError(executableBindingFailed, err)), nil)
	}
	return newApproval(diagnostic, nil)
}

func probeExecutable(ctx context.Context, executable *executableBinding, timeout time.Duration) Diagnostic {
	fingerprint := executable.fingerprint
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	versionOutput, err := probeCommand(probeCtx, executable, []string{"-version"})
	if err != nil {
		return failedProbe(probeCtx, "version_failed")
	}
	version, err := parseFFmpegVersion(versionOutput)
	if err != nil {
		var versionErr *VersionError
		if errors.As(err, &versionErr) && versionErr.Kind == VersionUnsupported {
			return Diagnostic{Status: StatusIncompatible, SHA256: fingerprint, Version: versionErr.Version.String(), ErrorCode: "unsupported_version", Detail: "configured FFmpeg version is outside the supported range"}
		}
		return Diagnostic{Status: StatusIncompatible, SHA256: fingerprint, ErrorCode: "malformed_version", Detail: "configured executable returned a malformed FFmpeg version"}
	}
	decoderOutput, err := probeCommand(probeCtx, executable, []string{"-hide_banner", "-decoders"})
	if err != nil {
		return failedProbe(probeCtx, "decoder_probe_failed")
	}
	encoderOutput, err := probeCommand(probeCtx, executable, []string{"-hide_banner", "-encoders"})
	if err != nil {
		return failedProbe(probeCtx, "encoder_probe_failed")
	}
	available := codecTokens(decoderOutput)
	decoders := make([]string, 0, len(requiredDecoders))
	for name, token := range requiredDecoders {
		if _, ok := available[token]; !ok {
			return Diagnostic{Status: StatusIncompatible, SHA256: fingerprint, Version: version.String(), ErrorCode: "missing_decoder", Detail: "required audio decoder is unavailable"}
		}
		decoders = append(decoders, name)
	}
	if _, ok := codecTokens(encoderOutput)["pcm_s16be"]; !ok {
		return Diagnostic{Status: StatusIncompatible, SHA256: fingerprint, Version: version.String(), ErrorCode: "missing_pcm_s16be", Detail: "required PCM encoder is unavailable"}
	}
	sort.Strings(decoders)
	return Diagnostic{Status: StatusAvailable, SHA256: fingerprint, Version: version.String(), Decoders: decoders}
}

func failedProbe(ctx context.Context, code string) Diagnostic {
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		return diagnosticFailure(StatusUnavailable, "probe_timeout", "configured executable probe timed out")
	}
	return diagnosticFailure(StatusUnavailable, code, "configured executable probe failed")
}

func probeCommand(ctx context.Context, executable *executableBinding, arguments []string) (string, error) {
	command := executable.command(arguments)
	configureProcess(command)
	output := &boundedBuffer{limit: maxProbeOutputBytes}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return "", err
	}
	if err := processStarted(command); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return "", err
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		processFinished(command)
	}()
	select {
	case err := <-done:
		if err != nil {
			return "", err
		}
		return output.String(), nil
	case <-ctx.Done():
		terminateProcessTree(command)
		<-done
		return "", context.Cause(ctx)
	}
}

func codecTokens(output string) map[string]struct{} {
	result := make(map[string]struct{})
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			result[fields[1]] = struct{}{}
		}
	}
	return result
}

func firstLine(output string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	if len(line) > 256 {
		return line[:256]
	}
	return line
}
