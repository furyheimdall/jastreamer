package transcode

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

type processState struct {
	command *exec.Cmd
	done    chan struct{}
	mu      sync.Mutex
	err     error
	kill    sync.Once
}

func newProcessState(command *exec.Cmd) *processState {
	return &processState{command: command, done: make(chan struct{})}
}

func (state *processState) finish(err error) {
	state.mu.Lock()
	state.err = err
	state.mu.Unlock()
	close(state.done)
}

func (state *processState) result() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.err
}

func (state *processState) terminate() {
	state.kill.Do(func() { terminateProcessTree(state.command) })
}

type stream struct {
	reader io.Reader
	stdout io.ReadCloser
	stdin  io.WriteCloser
	state  *processState
	close  sync.Once
	err    error
}

func (value *stream) Read(destination []byte) (int, error) {
	return value.reader.Read(destination)
}

func (value *stream) Close() error {
	value.close.Do(func() {
		value.state.terminate()
		stdinErr, stdoutErr := value.stdin.Close(), value.stdout.Close()
		if errors.Is(stdinErr, os.ErrClosed) {
			stdinErr = nil
		}
		if errors.Is(stdoutErr, os.ErrClosed) {
			stdoutErr = nil
		}
		value.err = errors.Join(stdinErr, stdoutErr)
		<-value.state.done
		processErr := value.state.result()
		if processErr != nil && !isTerminated(processErr) {
			value.err = errors.Join(value.err, processErr)
		}
	})
	return value.err
}
