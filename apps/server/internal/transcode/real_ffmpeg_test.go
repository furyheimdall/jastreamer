package transcode

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

type l16GoldenManifest struct {
	SchemaVersion          int               `json:"schema_version"`
	FFmpegReferenceVersion string            `json:"ffmpeg_reference_version"`
	Output                 l16GoldenOutput   `json:"output"`
	SHA256                 map[string]string `json:"sha256"`
}

type l16GoldenOutput struct {
	MediaType     string `json:"media_type"`
	SampleRateHz  int    `json:"sample_rate_hz"`
	Channels      int    `json:"channels"`
	BitsPerSample int    `json:"bits_per_sample"`
	ByteOrder     string `json:"byte_order"`
}

func TestInstalledFFmpeg_transcodes_five_codec_fixtures_to_checked_in_L16_goldens(t *testing.T) {
	// Given
	path := os.Getenv("JASTREAMER_TEST_FFMPEG")
	if path == "" {
		if runtime.GOOS == "windows" {
			t.Skip("JASTREAMER_TEST_FFMPEG is not configured")
		}
		path = "/usr/bin/ffmpeg"
	}
	if !filepath.IsAbs(path) {
		t.Fatal("JASTREAMER_TEST_FFMPEG must be an absolute path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("explicit FFmpeg test executable is unavailable")
	}
	approval := Probe(t.Context(), path, 5*time.Second)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})
	if approval.Status != StatusAvailable {
		t.Skipf("explicit FFmpeg lacks required support: %s", approval.ErrorCode)
	}
	provider, err := NewProvider(Config{Approval: approval, StartTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	manifest := readL16GoldenManifest(t)
	fixtures := []struct {
		name   string
		format catalog.Format
	}{
		{name: "flac", format: catalog.FormatFLAC},
		{name: "mp3", format: catalog.FormatMP3},
		{name: "ogg", format: catalog.FormatOggVorbis},
		{name: "opus", format: catalog.FormatOpus},
		{name: "wav", format: catalog.FormatPCMWAV},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			encoded, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tooling", "fixtures", "music", "analysis", "canonical."+fixture.name+".b64"))
			if err != nil {
				t.Fatal(err)
			}
			source, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded)))
			if err != nil {
				t.Fatal(err)
			}

			// When
			output := transcodeFixture(t, provider, source, fixture.format)

			// Then
			if outputSHA256(output) != manifest.SHA256[fixture.name] {
				t.Fatalf("L16 SHA-256 = %s, golden = %s", outputSHA256(output), manifest.SHA256[fixture.name])
			}
			assertStereoBigEndianPCM(t, output)
		})
	}
}

func TestL16GoldenComparison_rejects_wrong_hash(t *testing.T) {
	// Given
	output := []byte{0x12, 0x34, 0x12, 0x34}
	wrongGolden := strings.Repeat("0", sha256.Size*2)

	// When
	matches := outputSHA256(output) == wrongGolden

	// Then
	if matches {
		t.Fatal("wrong checked-in golden was accepted")
	}
}

func readL16GoldenManifest(t *testing.T) l16GoldenManifest {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("testdata", "l16-golden-sha256.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest l16GoldenManifest
	if err := json.Unmarshal(value, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.Output != (l16GoldenOutput{MediaType: "audio/L16", SampleRateHz: 44100, Channels: 2, BitsPerSample: 16, ByteOrder: "big_endian"}) {
		t.Fatalf("invalid L16 golden manifest metadata: %+v", manifest)
	}
	return manifest
}

func outputSHA256(output []byte) string {
	digest := sha256.Sum256(output)
	return hex.EncodeToString(digest[:])
}

func assertStereoBigEndianPCM(t *testing.T, output []byte) {
	t.Helper()
	if len(output) < 44100*4 || len(output)%4 != 0 || bytes.Equal(output, make([]byte, len(output))) {
		t.Fatalf("invalid PCM output length/signal: %d bytes", len(output))
	}
	for index := 0; index < len(output); index += 4 {
		if output[index] != output[index+2] || output[index+1] != output[index+3] {
			t.Fatalf("mono fixture was not duplicated to stereo at frame %d", index/4)
		}
	}
}

func transcodeFixture(t *testing.T, provider *Provider, source []byte, format catalog.Format) []byte {
	t.Helper()
	stream, err := provider.Open(t.Context(), bytes.NewReader(source), format)
	if err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return output
}
