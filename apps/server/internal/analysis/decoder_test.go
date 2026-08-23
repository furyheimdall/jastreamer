package analysis

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalDecoderSupportsMandatoryFormats(t *testing.T) {
	root := os.Getenv("JSTREAMER_FIXTURES")
	if root == "" {
		root = filepath.Join("..", "..", "..", "..", "tooling", "fixtures", "music", "analysis")
	}
	for _, name := range []string{"canonical.flac", "canonical.mp3", "canonical.ogg", "canonical.opus", "canonical.wav"} {
		t.Run(name, func(t *testing.T) {
			encoded, err := os.ReadFile(filepath.Join(root, name+".b64"))
			if err != nil {
				t.Fatal(err)
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), name)
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			rate, channels, samples, err := (LocalDecoder{}).Decode(context.Background(), path)
			if err != nil || rate < 4000 || channels < 1 || len(samples) == 0 {
				t.Fatalf("decode = %d/%d/%d %v", rate, channels, len(samples), err)
			}
		})
	}
}

func TestRIFFChunkPaddingOrderAndAlignment(t *testing.T) {
	base := pcmWAV(8000, 2, make([]int16, 4096))
	junk := []byte{'J', 'U', 'N', 'K', 3, 0, 0, 0, 'a', 'b', 'c', 0}
	base = append(base[:12], append(junk, base[12:]...)...)
	binary.LittleEndian.PutUint32(base[4:], uint32(len(base)-8))
	path := filepath.Join(t.TempDir(), "odd.wav")
	if err := os.WriteFile(path, base, 0600); err != nil {
		t.Fatal(err)
	}
	rate, ch, samples, err := (LocalDecoder{}).Decode(context.Background(), path)
	if err != nil || rate != 8000 || ch != 2 || len(samples) == 0 {
		t.Fatalf("padded decode = %d/%d/%d %v", rate, ch, len(samples), err)
	}
	bad := pcmWAV(8000, 2, make([]int16, 4096))
	binary.LittleEndian.PutUint32(bad[40:], uint32(len(bad)-45))
	path = filepath.Join(t.TempDir(), "bad.wav")
	if err = os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = (LocalDecoder{}).Decode(context.Background(), path); err == nil {
		t.Fatal("misaligned PCM accepted")
	}
}

type closeFailFile struct{ io.ReadSeeker }

func (closeFailFile) Close() error { return errors.New("close failed") }
func TestLocalDecoderPropagatesCloseFailure(t *testing.T) {
	d := LocalDecoder{open: func(string) (readSeekCloser, error) {
		return closeFailFile{bytes.NewReader(rhythmicChord(8000, 3))}, nil
	}}
	_, _, _, err := d.Decode(context.Background(), "fixture.wav")
	if err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Fatalf("close error = %v", err)
	}
}
