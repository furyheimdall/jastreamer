package transcode

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

type executableBindingErrorCode string

const (
	executableNotFound       executableBindingErrorCode = "not_found"
	executableNotExecutable  executableBindingErrorCode = "not_executable"
	executableUnsafeLocation executableBindingErrorCode = "unsafe_location"
	executableOpenFailed     executableBindingErrorCode = "open_failed"
	executableHashFailed     executableBindingErrorCode = "hash_failed"
	executableBindingFailed  executableBindingErrorCode = "identity_binding_failed"
)

type executableBindingError struct {
	Code  executableBindingErrorCode
	Cause error
}

func (value *executableBindingError) Error() string {
	return fmt.Sprintf("transcode: executable identity binding failed: %s", value.Code)
}

func (value *executableBindingError) Unwrap() error { return value.Cause }

func bindingError(code executableBindingErrorCode, cause error) error {
	return &executableBindingError{Code: code, Cause: cause}
}

func bindingDiagnostic(err error) Diagnostic {
	var bindingErr *executableBindingError
	if !errors.As(err, &bindingErr) {
		return diagnosticFailure(StatusUnavailable, string(executableBindingFailed), "configured executable identity could not be bound")
	}
	detail := "configured executable could not be opened"
	switch bindingErr.Code {
	case executableNotFound:
		detail = "configured executable was not found"
	case executableNotExecutable:
		detail = "configured path is not an executable regular file"
	case executableUnsafeLocation:
		detail = "configured executable or parent location is not identity-safe"
	case executableHashFailed:
		detail = "configured executable could not be fingerprinted"
	case executableBindingFailed:
		detail = "configured executable identity could not be bound"
	case executableOpenFailed:
	default:
		return diagnosticFailure(StatusUnavailable, string(executableBindingFailed), "configured executable identity could not be bound")
	}
	return diagnosticFailure(StatusUnavailable, string(bindingErr.Code), detail)
}

func fingerprintFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
