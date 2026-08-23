package analysis

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Decoder interface {
	Decode(context.Context, string) (int, int, []int16, error)
}
type readSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}
type LocalDecoder struct {
	open func(string) (readSeekCloser, error)
}

func (d LocalDecoder) Decode(ctx context.Context, path string) (rate, channels int, samples []int16, err error) {
	open := d.open
	if open == nil {
		open = func(path string) (readSeekCloser, error) { return os.Open(path) }
	}
	f, err := open(path)
	if err != nil {
		return 0, 0, nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		return decodeWAVFile(ctx, f)
	case ".mp3":
		return decodeMP3(ctx, f)
	case ".flac":
		return decodeFLAC(ctx, f)
	case ".ogg":
		return decodeVorbis(ctx, f)
	case ".opus":
		return decodeOpus(ctx, f)
	default:
		return 0, 0, nil, ErrUnsupported
	}
}
func AnalyzeFile(ctx context.Context, path string, decoder Decoder) (Features, error) {
	if decoder == nil {
		decoder = LocalDecoder{}
	}
	rate, channels, samples, err := decoder.Decode(ctx, path)
	if err != nil {
		return Features{}, err
	}
	return AnalyzeWAV(ctx, makeWAV(rate, channels, samples))
}
func sampleLimit(rate, channels int) int { return 12 * rate * channels }
