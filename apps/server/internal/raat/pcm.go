package raat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

const (
	maximumPCMSampleRate = 1536000
	maximumPCMChannels   = 32
	maximumWAVHeader     = 64 << 10
	pcmStartupTimeout    = 5 * time.Second
	pcmWaitTimeout       = 2 * time.Second
)

type audioFormat struct {
	sampleType                 string
	sampleRate, bits, channels int
}

func (format audioFormat) valid() bool {
	if format.sampleRate <= 0 || format.sampleRate > maximumPCMSampleRate || format.channels <= 0 || format.channels > maximumPCMChannels {
		return false
	}
	switch format.sampleType {
	case "pcm":
		return format.bits == 16 || format.bits == 24 || format.bits == 32
	case "pcm_float32":
		return format.bits == 32
	case "pcm_float64":
		return format.bits == 64
	default:
		return false
	}
}

func (format audioFormat) frameBytes() int {
	if !format.valid() {
		return 0
	}
	// Dimensions are bounded before multiplication, including on 32-bit hosts.
	return format.channels * (format.bits / 8)
}

func selectAudioFormat(audio library.AudioProperties, supported []audioFormat) (audioFormat, error) {
	if audio.SampleRate == nil || *audio.SampleRate <= 0 || *audio.SampleRate > maximumPCMSampleRate {
		return audioFormat{}, errors.New("RAAT source sample rate is unknown or unsupported")
	}
	if audio.Channels == nil || *audio.Channels <= 0 || *audio.Channels > maximumPCMChannels {
		return audioFormat{}, errors.New("RAAT source channel count is unknown or unsupported")
	}
	minimumBits, sampleType := 0, "pcm"
	switch strings.ToLower(strings.TrimSpace(audio.Codec)) {
	case "pcm", "flac", "alac":
		if audio.BitsPerSample == nil || *audio.BitsPerSample < 1 || *audio.BitsPerSample > 32 {
			return audioFormat{}, errors.New("RAAT lossless source precision must be known and between 1 and 32 bits")
		}
		minimumBits = *audio.BitsPerSample
	case "ieee float":
		if audio.BitsPerSample == nil || (*audio.BitsPerSample != 32 && *audio.BitsPerSample != 64) {
			return audioFormat{}, errors.New("RAAT floating-point source precision must be known and 32 or 64 bits")
		}
		minimumBits = *audio.BitsPerSample
		sampleType = "pcm_float" + strconv.Itoa(minimumBits)
	case "mp3", "aac", "opus", "vorbis":
		// This is a high-precision decode of a lossy source, not source bit-perfect PCM.
		minimumBits = 24
	default:
		return audioFormat{}, fmt.Errorf("RAAT source codec %q is unknown or unsupported", audio.Codec)
	}
	var selected audioFormat
	for _, candidate := range supported {
		if !candidate.valid() || candidate.sampleType != sampleType || candidate.sampleRate != int(*audio.SampleRate) || candidate.channels != *audio.Channels || candidate.bits < minimumBits {
			continue
		}
		if selected.bits == 0 || candidate.bits < selected.bits {
			selected = candidate
		}
	}
	if !selected.valid() {
		return audioFormat{}, fmt.Errorf("RAAT output does not support source rate %d Hz, %d channels and %s precision %d without loss", *audio.SampleRate, *audio.Channels, sampleType, minimumBits)
	}
	return selected, nil
}

// openPCM borrows source's descriptor, never its pathname. The caller retains
// ownership, but must not concurrently seek or read the shared file description.
// Source precision metadata must be trustworthy: the output WAV verifies the
// actual decoded rate/channels and encoded width, not the input's precision.
func openPCM(ctx context.Context, ffmpegPath string, source *os.File, format audioFormat, positionMS int64) (io.ReadCloser, error) {
	if !format.valid() || source == nil || ffmpegPath == "" || positionMS < 0 {
		return nil, errors.New("invalid RAAT PCM decoder arguments")
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("position RAAT source descriptor: %w", err)
	}
	codec := "pcm_s" + strconv.Itoa(format.bits) + "le"
	if format.sampleType != "pcm" {
		codec = "pcm_f" + strconv.Itoa(format.bits) + "le"
	}
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-err_detect", "explode",
		"-max_alloc", "33554432", "-cpucount", "1", "-filter_threads", "1", "-filter_complex_threads", "1",
		"-threads", "1", "-protocol_whitelist", "fd,pipe",
		"-format_whitelist", "wav,flac,mp3,mov,ogg,aac", "-fd", "0", "-i", "fd:",
	}
	if positionMS > 0 {
		// Output-side seeking decodes and discards up to the exact requested time.
		// Integer formatting avoids float rounding and duration overflow.
		arguments = append(arguments, "-ss", fmt.Sprintf("%d.%03d", positionMS/1000, positionMS%1000))
	}
	arguments = append(arguments,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "-1", "-threads", "1",
		"-c:a", codec, "-dither_method", "none", "-f", "wav", "-rf64", "never", "pipe:1")
	processContext, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processContext, ffmpegPath, arguments...)
	command.Stdin = source
	// Decoder diagnostics can contain untrusted metadata. Do not expose or retain them.
	command.Stderr = io.Discard
	command.WaitDelay = pcmWaitTimeout
	stdout, writer, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create RAAT PCM pipe: %w", err)
	}
	// An explicit pipe lets Wait reap the child without truncating unread stdout.
	command.Stdout = writer
	if err := command.Start(); err != nil {
		cancel()
		_ = writer.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start RAAT PCM decoder (FFmpeg with fd protocol required): %w", err)
	}
	_ = writer.Close()
	stream := &pcmStream{stdout: stdout, cancel: cancel, ctx: ctx, done: make(chan struct{}), frameBytes: format.frameBytes()}
	go func() {
		stream.waitErr = command.Wait()
		close(stream.done)
	}()
	type headerResult struct {
		length int64
		err    error
	}
	header := make(chan headerResult, 1)
	go func() {
		length, err := readPCMHeader(stdout, format)
		header <- headerResult{length: length, err: err}
	}()
	timer := time.NewTimer(pcmStartupTimeout)
	defer timer.Stop()
	select {
	case result := <-header:
		if result.err != nil {
			_ = stream.Close()
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			return nil, fmt.Errorf("RAAT PCM decoder startup (FFmpeg with fd protocol required): %w", result.err)
		}
		stream.remaining = result.length
		stream.padding = result.length > 0 && result.length%2 != 0
		return stream, nil
	case <-timer.C:
		_ = stream.Close()
		<-header
		return nil, errors.New("RAAT PCM decoder startup timed out")
	case <-ctx.Done():
		_ = stream.Close()
		<-header
		return nil, context.Cause(ctx)
	}
}

type pcmStream struct {
	stdout         *os.File
	cancel         context.CancelFunc
	ctx            context.Context
	done           chan struct{}
	waitErr        error // Published by closing done.
	closeOnce      sync.Once
	remaining      int64 // -1 denotes the streaming WAV data-size sentinel.
	frameBytes     int
	frameRemainder int
	padding        bool
	terminal       error
}

func (stream *pcmStream) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if stream.terminal != nil {
		return 0, stream.terminal
	}
	if stream.remaining == 0 {
		var trailer [1]byte
		if stream.padding {
			if _, err := io.ReadFull(stream.stdout, trailer[:]); err != nil {
				return 0, stream.finish(io.ErrUnexpectedEOF)
			}
			if trailer[0] != 0 {
				return 0, stream.finish(errors.New("RAAT PCM WAV padding is malformed"))
			}
			stream.padding = false
		}
		n, err := stream.stdout.Read(trailer[:])
		if n != 0 {
			return 0, stream.finish(errors.New("RAAT PCM decoder emitted data beyond the WAV payload"))
		}
		if err != nil {
			return 0, stream.finish(err)
		}
		return 0, nil
	}
	if stream.remaining > 0 && int64(len(destination)) > stream.remaining {
		destination = destination[:int(stream.remaining)]
	}
	n, err := stream.stdout.Read(destination)
	stream.frameRemainder = (stream.frameRemainder + n%stream.frameBytes) % stream.frameBytes
	if stream.remaining > 0 {
		stream.remaining -= int64(n)
	}
	if err != nil {
		return n, stream.finish(err)
	}
	return n, nil
}

func (stream *pcmStream) finish(readErr error) error {
	if !errors.Is(readErr, io.EOF) {
		stream.cancel()
	}
	timer := time.NewTimer(pcmWaitTimeout)
	defer timer.Stop()
	select {
	case <-stream.done:
	case <-timer.C:
		stream.cancel()
		<-stream.done
		readErr = errors.New("RAAT PCM decoder did not exit after its output ended")
	case <-stream.ctx.Done():
		stream.cancel()
		<-stream.done
	}
	_ = stream.stdout.Close()
	stream.cancel()
	switch {
	case stream.ctx.Err() != nil:
		stream.terminal = context.Cause(stream.ctx)
	case stream.waitErr != nil:
		stream.terminal = fmt.Errorf("RAAT PCM decode failed: %w", stream.waitErr)
	case errors.Is(readErr, io.EOF) && (stream.remaining > 0 || stream.frameRemainder != 0):
		stream.terminal = io.ErrUnexpectedEOF
	default:
		stream.terminal = readErr
	}
	return stream.terminal
}

func (stream *pcmStream) Close() error {
	stream.closeOnce.Do(func() {
		stream.cancel()
		_ = stream.stdout.Close()
		<-stream.done
	})
	return nil
}

// readPCMHeader consumes only the bounded WAV header, leaving the PCM unread.
// A nonnegative result is an exact byte count; -1 is the streaming sentinel.
func readPCMHeader(reader io.Reader, expected audioFormat) (int64, error) {
	if !expected.valid() {
		return 0, errors.New("invalid expected PCM format")
	}
	var header [12]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, fmt.Errorf("read WAV header: %w", err)
	}
	rf64 := string(header[:4]) == "RF64"
	if (!rf64 && string(header[:4]) != "RIFF") || string(header[8:]) != "WAVE" {
		return 0, errors.New("PCM decoder did not emit RIFF/RF64 WAVE")
	}
	riffSize := uint64(binary.LittleEndian.Uint32(header[4:8]))
	unknownSize := riffSize == uint64(^uint32(0))
	if rf64 && !unknownSize {
		return 0, errors.New("RF64 header is missing its size sentinel")
	}
	if !unknownSize && riffSize < 4 {
		return 0, errors.New("invalid RIFF size")
	}
	consumed := uint64(4)
	dataSize := uint64(0)
	hasFormat, hasDS64 := false, false
	for range 128 {
		if consumed+8 > maximumWAVHeader || (!unknownSize && consumed+8 > riffSize) {
			return 0, errors.New("WAV header exceeds its size limit")
		}
		var chunk [8]byte
		if _, err := io.ReadFull(reader, chunk[:]); err != nil {
			return 0, fmt.Errorf("read WAV chunk: %w", err)
		}
		consumed += 8
		length := uint64(binary.LittleEndian.Uint32(chunk[4:]))
		if string(chunk[:4]) == "data" {
			if !hasFormat || (rf64 && !hasDS64) {
				return 0, errors.New("WAV data precedes required format or ds64 chunk")
			}
			if length == uint64(^uint32(0)) {
				if rf64 {
					length = dataSize
				} else if unknownSize {
					return -1, nil
				} else {
					return 0, errors.New("finite RIFF has an unbounded data chunk")
				}
			}
			// Compare before adding, keeping malformed 64-bit sizes overflow-safe.
			if length > uint64(1<<63-1) || length%uint64(expected.frameBytes()) != 0 || (!unknownSize && (consumed > riffSize || length > riffSize-consumed || length+length%2 != riffSize-consumed)) {
				return 0, errors.New("WAV data size is invalid or not frame aligned")
			}
			return int64(length), nil
		}
		padded := length + length%2
		if padded > maximumWAVHeader-consumed || (!unknownSize && padded > riffSize-consumed) {
			return 0, errors.New("WAV chunk exceeds header bounds")
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if hasFormat || length < 16 || length > 64 {
				return 0, errors.New("invalid or duplicate WAV format chunk")
			}
			var payload [64]byte
			if _, err := io.ReadFull(reader, payload[:int(length)]); err != nil {
				return 0, fmt.Errorf("read WAV format: %w", err)
			}
			if err := validatePCMFormat(payload[:int(length)], expected); err != nil {
				return 0, err
			}
			hasFormat = true
		case "ds64":
			if !rf64 || hasDS64 || consumed != 12 || length < 28 || length > 64 {
				return 0, errors.New("invalid RF64 ds64 chunk")
			}
			var payload [64]byte
			if _, err := io.ReadFull(reader, payload[:int(length)]); err != nil {
				return 0, fmt.Errorf("read RF64 sizes: %w", err)
			}
			if binary.LittleEndian.Uint32(payload[24:28]) != 0 || length != 28 {
				return 0, errors.New("unsupported RF64 size table")
			}
			riffSize = binary.LittleEndian.Uint64(payload[:8])
			dataSize = binary.LittleEndian.Uint64(payload[8:16])
			if riffSize < consumed+padded || riffSize > uint64(1<<63-1) || dataSize > riffSize {
				return 0, errors.New("invalid RF64 sizes")
			}
			if frames := binary.LittleEndian.Uint64(payload[16:24]); frames != 0 && (dataSize%uint64(expected.frameBytes()) != 0 || frames != dataSize/uint64(expected.frameBytes())) {
				return 0, errors.New("RF64 sample count does not match PCM data")
			}
			unknownSize, hasDS64 = false, true
		default:
			if _, err := io.CopyN(io.Discard, reader, int64(length)); err != nil {
				return 0, fmt.Errorf("skip WAV chunk: %w", err)
			}
		}
		if length%2 != 0 {
			var padding [1]byte
			if _, err := io.ReadFull(reader, padding[:]); err != nil {
				return 0, fmt.Errorf("read WAV padding: %w", err)
			}
		}
		consumed += padded
	}
	return 0, errors.New("too many WAV header chunks")
}

func validatePCMFormat(payload []byte, expected audioFormat) error {
	if len(payload) < 16 {
		return errors.New("truncated WAV format")
	}
	tag := binary.LittleEndian.Uint16(payload[:2])
	bits := int(binary.LittleEndian.Uint16(payload[14:16]))
	validBits := bits
	if len(payload) != 16 && (len(payload) < 18 || int(binary.LittleEndian.Uint16(payload[16:18])) != len(payload)-18) {
		return errors.New("invalid WAV format extension length")
	}
	if tag == 0xfffe {
		if len(payload) < 40 || !bytes.Equal(payload[26:40], []byte{0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}) {
			return errors.New("invalid extensible WAV subformat")
		}
		tag = binary.LittleEndian.Uint16(payload[24:26])
		validBits = int(binary.LittleEndian.Uint16(payload[18:20]))
	}
	expectedTag := uint16(1)
	if expected.sampleType != "pcm" {
		expectedTag = 3
	}
	if tag != expectedTag || int(binary.LittleEndian.Uint16(payload[2:4])) != expected.channels || uint64(binary.LittleEndian.Uint32(payload[4:8])) != uint64(expected.sampleRate) || bits != expected.bits || validBits != expected.bits {
		return errors.New("decoded WAV sample type, rate, channels, or precision does not match the negotiated RAAT format")
	}
	if int(binary.LittleEndian.Uint16(payload[12:14])) != expected.frameBytes() || uint64(binary.LittleEndian.Uint32(payload[8:12])) != uint64(expected.sampleRate)*uint64(expected.frameBytes()) {
		return errors.New("decoded WAV block alignment or byte rate is invalid")
	}
	return nil
}
