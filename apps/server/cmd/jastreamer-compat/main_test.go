package main

import (
	"errors"
	"io"
	"testing"
)

var errOutput = errors.New("output failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errOutput
}

func TestRunCLI_returns_checked_output_failure(t *testing.T) {
	// Given
	args := []string{"--invalid"}

	// When
	code, err := runCLI(args, io.Discard, failingWriter{})

	// Then
	if code != 74 || !errors.Is(err, errOutput) {
		t.Fatalf("code=%d error=%v", code, err)
	}
}
