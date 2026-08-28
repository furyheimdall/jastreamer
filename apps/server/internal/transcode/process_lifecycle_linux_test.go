//go:build linux

package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"golang.org/x/sys/unix"
)

var errMalformedLinuxProcessStat = errors.New("malformed Linux process stat")

type linuxProcessDisposition uint8

const (
	linuxProcessGone linuxProcessDisposition = iota
	linuxProcessZombie
	linuxProcessDead
	linuxProcessLive
)

func TestProvider_cancellation_terminates_process_tree(t *testing.T) {
	// Given
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	path := fakeExecutable(t, fakeProgramTree)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second, environment: append(os.Environ(), "PIDFILE="+pidFile)})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	output, err := provider.Open(ctx, bytes.NewReader(nil), catalog.FormatOpus)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := output.Close(); closeErr != nil && !errors.Is(closeErr, context.Canceled) {
			t.Error(closeErr)
		}
	})
	processOutput, ok := output.(*stream)
	if !ok {
		t.Fatalf("stream type = %T", output)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(pid, unix.PIDFD_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	pidfdFile := os.NewFile(uintptr(pidfd), "FFmpeg descendant pidfd")
	pidfdOpen := true
	t.Cleanup(func() {
		if pidfdOpen {
			if closeErr := pidfdFile.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
	})
	pidfdConnection, err := pidfdFile.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	subscribed := make(chan struct{})
	descendantExited := make(chan error, 1)
	go func() {
		firstInvocation := true
		// RawConn.Read invokes the callback before parking in the runtime poller.
		// Its second invocation therefore follows pidfd exit readiness.
		waitErr := pidfdConnection.Read(func(uintptr) bool {
			if firstInvocation {
				firstInvocation = false
				close(subscribed)
				return false
			}
			return true
		})
		descendantExited <- waitErr
	}()
	select {
	case <-subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("pidfd exit subscription was not established")
	}

	// When
	cancel()

	// Then
	select {
	case waitErr := <-descendantExited:
		if waitErr != nil {
			t.Fatal(waitErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FFmpeg descendant did not exit after cancellation")
	}
	select {
	case <-processOutput.state.done:
	case <-time.After(2 * time.Second):
		t.Fatal("FFmpeg process did not exit after cancellation")
	}
	if closeErr := output.Close(); closeErr != nil && !errors.Is(closeErr, context.Canceled) {
		t.Fatal(closeErr)
	}
	stat, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	disposition, err := linuxProcessDispositionFromStat(stat, readErr)
	if err != nil {
		t.Fatal(err)
	}
	if !disposition.terminal() {
		cleanupErr := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		if cleanupErr != nil && !errors.Is(cleanupErr, unix.ESRCH) {
			t.Errorf("terminate live descendant after failed assertion: %v", cleanupErr)
		}
		t.Fatalf("descendant remained live after process exit: %s", stat)
	}
	if closeErr := pidfdFile.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	pidfdOpen = false
}

func TestLinuxProcessDisposition_classifies_terminal_and_live_states(t *testing.T) {
	tests := []struct {
		name         string
		stat         string
		readErr      error
		want         linuxProcessDisposition
		wantTerminal bool
	}{
		{name: "gone", readErr: os.ErrNotExist, want: linuxProcessGone, wantTerminal: true},
		{name: "gone ESRCH", readErr: unix.ESRCH, want: linuxProcessGone, wantTerminal: true},
		{name: "zombie", stat: "41 (child) Z 1 2 3", want: linuxProcessZombie, wantTerminal: true},
		{name: "dead uppercase", stat: "42 (child) X 1 2 3", want: linuxProcessDead, wantTerminal: true},
		{name: "dead lowercase", stat: "43 (child) x 1 2 3", want: linuxProcessDead, wantTerminal: true},
		{name: "live", stat: "44 (child) R 1 2 3", want: linuxProcessLive},
		{name: "unknown is live", stat: "45 (child) ? 1 2 3", want: linuxProcessLive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			stat := []byte(test.stat)

			// When
			got, err := linuxProcessDispositionFromStat(stat, test.readErr)
			// Then
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want || got.terminal() != test.wantTerminal {
				t.Fatalf("disposition = %d terminal=%t, want %d terminal=%t", got, got.terminal(), test.want, test.wantTerminal)
			}
		})
	}
}

func linuxProcessDispositionFromStat(stat []byte, readErr error) (linuxProcessDisposition, error) {
	if errors.Is(readErr, os.ErrNotExist) || errors.Is(readErr, unix.ESRCH) {
		return linuxProcessGone, nil
	}
	if readErr != nil {
		return linuxProcessLive, fmt.Errorf("read Linux process stat: %w", readErr)
	}
	closingParenthesis := bytes.LastIndex(stat, []byte(") "))
	stateIndex := closingParenthesis + 2
	if closingParenthesis < 0 || stateIndex >= len(stat) {
		return linuxProcessLive, errMalformedLinuxProcessStat
	}
	switch stat[stateIndex] {
	case 'Z':
		return linuxProcessZombie, nil
	case 'X', 'x':
		return linuxProcessDead, nil
	default:
		return linuxProcessLive, nil
	}
}

func (disposition linuxProcessDisposition) terminal() bool {
	switch disposition {
	case linuxProcessGone, linuxProcessZombie, linuxProcessDead:
		return true
	case linuxProcessLive:
		return false
	default:
		return false
	}
}
