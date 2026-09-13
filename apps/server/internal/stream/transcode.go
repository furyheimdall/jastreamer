package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	maximumTranscodes       = 2
	transcodeStartupTimeout = 5 * time.Second
	transcodeWaitDelay      = 2 * time.Second
)

type transcoder struct {
	path  string
	slots chan struct{}
}

type transcodeStream struct {
	reader io.Reader
	stdout io.ReadCloser
	cancel context.CancelFunc
	done   <-chan error
	close  sync.Once
}

type firstTranscodeByte struct {
	value byte
	count int
	err   error
}

func newTranscoder(path string) *transcoder {
	return &transcoder{path: path, slots: make(chan struct{}, maximumTranscodes)}
}

func (transcoder *transcoder) open(ctx context.Context, source *os.File, format transcodeFormat) (io.ReadCloser, error) {
	select {
	case transcoder.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	default:
		return nil, ErrTranscodeBusy
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-transcoder.slots
		}
	}()

	codec, muxer := "", ""
	switch format {
	case transcodeL16:
		codec, muxer = "pcm_s16be", "s16be"
	case transcodeWAV:
		codec, muxer = "pcm_s16le", "wav"
	default:
		return nil, ErrTranscodeFailed
	}
	processContext, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processContext, transcoder.path,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror",
		"-fd", "0", "-i", "fd:", "-map", "0:a:0",
		"-vn", "-sn", "-dn", "-ac", "2", "-ar", "44100",
		"-acodec", codec, "-f", muxer, "pipe:1",
	)
	command.Stdin = source
	command.Stderr = io.Discard
	command.WaitDelay = transcodeWaitDelay
	stdout, pipeWriter, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, ErrTranscodeFailed
	}
	// Wait must not close the read end before the HTTP response drains it.
	command.Stdout = pipeWriter
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		_ = pipeWriter.Close()
		cancel()
		return nil, ErrTranscodeFailed
	}
	_ = pipeWriter.Close()
	done := make(chan error, 1)
	go func() {
		err := command.Wait()
		<-transcoder.slots
		done <- err
		close(done)
	}()
	releaseSlot = false

	first := make(chan firstTranscodeByte, 1)
	go func() {
		var value [1]byte
		count, err := stdout.Read(value[:])
		first <- firstTranscodeByte{value: value[0], count: count, err: err}
	}()
	timer := time.NewTimer(transcodeStartupTimeout)
	defer timer.Stop()
	closeFailed := func() error {
		cancel()
		_ = stdout.Close()
		<-done
		return ErrTranscodeFailed
	}
	select {
	case result := <-first:
		if result.count == 1 {
			return &transcodeStream{
				reader: io.MultiReader(bytes.NewReader([]byte{result.value}), stdout),
				stdout: stdout,
				cancel: cancel,
				done:   done,
			}, nil
		}
		if result.err != nil && !errors.Is(result.err, io.EOF) {
			return nil, closeFailed()
		}
		return nil, closeFailed()
	case <-timer.C:
		return nil, closeFailed()
	case <-ctx.Done():
		cancel()
		_ = stdout.Close()
		<-done
		return nil, context.Cause(ctx)
	}
}

func (stream *transcodeStream) Read(destination []byte) (int, error) {
	return stream.reader.Read(destination)
}

func (stream *transcodeStream) Close() error {
	stream.close.Do(func() {
		stream.cancel()
		_ = stream.stdout.Close()
		<-stream.done
	})
	return nil
}
