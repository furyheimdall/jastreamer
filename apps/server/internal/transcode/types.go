package transcode

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxDiagnosticBytes = 4096

var (
	ErrUnavailable       = errors.New("transcode: FFmpeg fallback unavailable")
	ErrExecutableChanged = errors.New("transcode: configured FFmpeg executable changed after probe")
	ErrStartTimeout      = errors.New("transcode: FFmpeg produced no output before startup deadline")
	ErrUnsupported       = errors.New("transcode: source format unsupported")
	ErrCapacity          = errors.New("transcode: FFmpeg process capacity exhausted")
	ErrInvalidOffset     = errors.New("transcode: invalid output offset")
)

type Status string

const (
	StatusUnconfigured Status = "unconfigured"
	StatusAvailable    Status = "available"
	StatusUnavailable  Status = "unavailable"
	StatusIncompatible Status = "incompatible"
)

type Diagnostic struct {
	Status    Status
	SHA256    string
	Version   string
	Decoders  []string
	ErrorCode string
	Detail    string
}

func (value Diagnostic) Supports(codec string) bool {
	for _, supported := range value.Decoders {
		if supported == codec {
			return true
		}
	}
	return false
}

type Config struct {
	Approval     *Approval
	StartTimeout time.Duration
	environment  []string
}

type CapacityError struct{ Limit int }

func (value *CapacityError) Error() string {
	return fmt.Sprintf("transcode: capacity exhausted at %d processes", value.Limit)
}

func (value *CapacityError) Is(target error) bool { return target == ErrCapacity }

type OffsetError struct {
	Offset  time.Duration
	Maximum time.Duration
}

func (value *OffsetError) Error() string {
	return fmt.Sprintf("transcode: output offset %s outside 0..%s", value.Offset, value.Maximum)
}

func (value *OffsetError) Is(target error) bool { return target == ErrInvalidOffset }

type ProcessError struct {
	Operation string
	Detail    string
	Cause     error
}

func (value *ProcessError) Error() string {
	parts := []string{"transcode: " + value.Operation + " failed"}
	if value.Detail != "" {
		parts = append(parts, value.Detail)
	}
	return strings.Join(parts, ": ")
}

func (value *ProcessError) Unwrap() error { return value.Cause }

func diagnosticFailure(status Status, code, detail string) Diagnostic {
	if len(detail) > maxDiagnosticBytes {
		detail = detail[:maxDiagnosticBytes]
	}
	return Diagnostic{Status: status, ErrorCode: code, Detail: detail}
}

func processFailure(operation string, detail string, cause error) error {
	if len(detail) > maxDiagnosticBytes {
		detail = detail[:maxDiagnosticBytes]
	}
	return &ProcessError{Operation: operation, Detail: strings.TrimSpace(detail), Cause: cause}
}

func validateTimeout(value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("timeout must be positive: %w", ErrUnavailable)
	}
	return nil
}
