//go:build !windows

package transcode

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processStarted(*exec.Cmd) error { return nil }

func processFinished(*exec.Cmd) {}

func terminateProcessTree(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = command.Process.Kill()
	}
}

func isTerminated(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ProcessState != nil && !exit.ProcessState.Success()
}
