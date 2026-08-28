//go:build !linux && !windows

package transcode

import (
	"os/exec"
	"sync"
)

type executableBinding struct {
	fingerprint string
	close       sync.Once
}

func bindExecutable(string) (*executableBinding, error) {
	return nil, bindingError(executableUnsafeLocation, nil)
}

func (*executableBinding) command([]string) *exec.Cmd { return &exec.Cmd{} }

func (*executableBinding) Close() error { return nil }
