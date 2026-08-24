//go:build windows

// jastreamer Windows Service host. The MSI registers this executable with SCM;
// it supervises the cross-platform Server core in the same installation directory.
package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
)

type service struct{}

func (service) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	directory, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(directory, "jastreamer-server-core.exe"), "--config", filepath.Join(directory, "server.json"))
	command.Dir = directory
	command.Env = append(os.Environ(), "JASTREAMER_DATA_DIR="+filepath.Join(os.Getenv("ProgramData"), "jastreamer", "Server"))
	stdout, err := command.StdoutPipe()
	if err != nil {
		return false, 1
	}
	if err = command.Start(); err != nil {
		return false, 1
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(ready, "ready https://") {
		cancel()
		return false, 1
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-finished:
				case <-time.After(20 * time.Second):
					_ = command.Process.Kill()
				}
				return false, 0
			}
		case err = <-finished:
			if err == nil || errors.Is(err, context.Canceled) {
				return false, 0
			}
			return false, 1
		}
	}
}

func main() {
	if err := svc.Run("jastreamer-server", service{}); err != nil {
		os.Exit(1)
	}
}
