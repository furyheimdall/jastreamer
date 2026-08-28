package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

const (
	defaultStartTimeout = 3 * time.Second
	maximumOffset       = 24 * time.Hour
	maximumTranscodes   = 2
	offsetPrecision     = time.Microsecond
)

type Provider struct {
	executable   *executableBinding
	startTimeout time.Duration
	environment  []string
	slots        chan struct{}
}

type Input struct {
	Source io.Reader
	Format catalog.Format
	Offset time.Duration
}

type processLease struct {
	slots <-chan struct{}
	once  sync.Once
}

func (lease *processLease) release() {
	lease.once.Do(func() { <-lease.slots })
}

func NewProvider(config Config) (*Provider, error) {
	if config.Approval == nil || config.Approval.Diagnostic.Status != StatusAvailable || len(config.Approval.Diagnostic.SHA256) != 64 {
		return nil, ErrUnavailable
	}
	startTimeout := config.StartTimeout
	if startTimeout == 0 {
		startTimeout = defaultStartTimeout
	}
	if err := validateTimeout(startTimeout); err != nil {
		return nil, err
	}
	environment := config.environment
	if environment == nil {
		environment = os.Environ()
	}
	executable, err := config.Approval.consume()
	if err != nil {
		return nil, err
	}
	return &Provider{executable: executable, startTimeout: startTimeout, environment: environment, slots: make(chan struct{}, maximumTranscodes)}, nil
}

func (provider *Provider) Close() error { return provider.executable.Close() }

func (provider *Provider) Open(ctx context.Context, source io.Reader, format catalog.Format) (io.ReadCloser, error) {
	return provider.OpenAt(ctx, Input{Source: source, Format: format})
}

func (provider *Provider) OpenAt(ctx context.Context, input Input) (io.ReadCloser, error) {
	offset := input.Offset
	if !supportedFormat(input.Format) {
		return nil, ErrUnsupported
	}
	if offset < 0 || offset > maximumOffset || offset%offsetPrecision != 0 {
		return nil, &OffsetError{Offset: offset, Maximum: maximumOffset}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	lease := &processLease{slots: provider.slots}
	select {
	case provider.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	default:
		return nil, &CapacityError{Limit: maximumTranscodes}
	}
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-i", "pipe:0"}
	if offset > 0 {
		seconds, fraction := offset/time.Second, offset%time.Second/time.Microsecond
		arguments = append(arguments, "-ss", fmt.Sprintf("%d.%06d", seconds, fraction))
	}
	arguments = append(arguments, "-vn", "-sn", "-dn", "-ac", "2", "-ar", "44100", "-acodec", "pcm_s16be", "-f", "s16be", "pipe:1")
	command := provider.executable.command(arguments)
	command.Env = provider.environment
	configureProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		lease.release()
		return nil, processFailure("create input pipe", "", err)
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		lease.release()
		return nil, processFailure("create output pipe", "", err)
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		lease.release()
		return nil, processFailure("create diagnostic pipe", "", err)
	}
	command.Stdout, command.Stderr = stdoutWriter, stderrWriter
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		_ = stderr.Close()
		_ = stderrWriter.Close()
		lease.release()
		return nil, processFailure("start", "", err)
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	if err := processStarted(command); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		lease.release()
		return nil, processFailure("isolate process", "", err)
	}
	state := newProcessState(command)
	diagnostics := &boundedBuffer{limit: maxDiagnosticBytes}
	go func() {
		_, _ = io.Copy(stdin, input.Source)
		_ = stdin.Close()
	}()
	go func() {
		_, _ = io.Copy(diagnostics, stderr)
		_ = stderr.Close()
	}()
	go func() {
		waitErr := command.Wait()
		processFinished(command)
		lease.release()
		state.finish(waitErr)
	}()
	go func() {
		select {
		case <-ctx.Done():
			state.terminate()
		case <-state.done:
		}
	}()
	first := make(chan firstRead, 1)
	go func() {
		var one [1]byte
		count, readErr := stdout.Read(one[:])
		first <- firstRead{value: one[0], count: count, err: readErr}
	}()
	timer := time.NewTimer(provider.startTimeout)
	defer timer.Stop()
	select {
	case result := <-first:
		if result.count == 1 {
			return &stream{reader: io.MultiReader(bytes.NewReader([]byte{result.value}), stdout), stdout: stdout, stdin: stdin, state: state}, nil
		}
		if result.err != nil && !errors.Is(result.err, io.EOF) {
			state.terminate()
			<-state.done
			return nil, processFailure("read startup output", diagnostics.String(), result.err)
		}
		<-state.done
		return nil, processFailure("startup", diagnostics.String(), state.result())
	case <-timer.C:
		state.terminate()
		<-state.done
		return nil, errors.Join(ErrStartTimeout, processFailure("startup", diagnostics.String(), state.result()))
	case <-ctx.Done():
		state.terminate()
		<-state.done
		return nil, context.Cause(ctx)
	}
}

func supportedFormat(format catalog.Format) bool {
	switch format {
	case catalog.FormatFLAC, catalog.FormatMP3, catalog.FormatOggVorbis, catalog.FormatOpus, catalog.FormatPCMWAV:
		return true
	default:
		return false
	}
}

type firstRead struct {
	value byte
	count int
	err   error
}

type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	value []byte
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := buffer.limit - len(buffer.value)
	if remaining > 0 {
		buffer.value = append(buffer.value, value[:min(remaining, len(value))]...)
	}
	return len(value), nil
}

func (buffer *boundedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.value)
}
