package analysis

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Pre-quantization parity tolerances account for libm differences across targets:
// tempo 0.25 BPM, loudness 0.05 dB, frequencies 1 Hz, and normalized bands 0.005.
func TestExtractMeaningfulFeaturesAndGoldenVector(t *testing.T) {
	f, err := AnalyzeWAV(context.Background(), rhythmicChord(44100, 12))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(f.Tempo-120) > .25 || f.Key != 9 {
		t.Fatalf("tempo/key = %.3f/%d", f.Tempo, f.Key)
	}
	if f.Loudness > -8 || f.Loudness < -24 || f.Centroid < 200 || f.Centroid > 1500 || f.Rolloff <= f.Centroid {
		t.Fatalf("spectral summary = %+v", f)
	}
	if f.Contrast <= 0 || f.Chroma[9] < f.Chroma[0] || f.MFCC[0] == f.MFCC[1] {
		t.Fatalf("non-meaningful bands: %+v", f)
	}
	want := []byte{102, 9, 192, 5, 255, 7, 170, 249, 138, 12, 240, 208, 39, 108, 17, 255, 174, 14, 140, 129, 126, 124, 125, 127, 129, 129, 128, 127, 127, 128, 1, 1}
	if !bytes.Equal(f.Vector, want) {
		t.Fatalf("vector = %v, want %v", f.Vector, want)
	}
}

func TestBoundedWindowsAndProvenance(t *testing.T) {
	data := rhythmicChord(44100, 90)
	f, err := AnalyzeWAV(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if f.AnalyzedSamples > MaxAnalysisSamples || f.SourceSamples != 90*44100 {
		t.Fatalf("sample bounds = %d/%d", f.AnalyzedSamples, f.SourceSamples)
	}
	if f.Provenance != CurrentProvenance() || f.ContentFingerprint == "" {
		t.Fatalf("provenance = %+v", f)
	}
}

func TestWorkerBoundedConcurrencyCancellationResumeAndRecompute(t *testing.T) {
	var active, peak atomic.Int32
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	w := NewWorker(2)
	w.analyze = func(ctx context.Context, data []byte) (Features, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return Features{Provenance: CurrentProvenance(), Vector: []byte{1}}, nil
		case <-ctx.Done():
			return Features{}, ctx.Err()
		}
	}
	jobs := []Job{{ID: "a", Fingerprint: "a"}, {ID: "b", Fingerprint: "b"}, {ID: "c", Fingerprint: "c"}}
	done := make(chan map[string]Result, 1)
	go func() { got, _ := w.Run(context.Background(), jobs, nil); done <- got }()
	<-started
	<-started
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d", peak.Load())
	}
	close(release)
	got := <-done
	if len(got) != 3 {
		t.Fatalf("results = %+v", got)
	}
	again, err := w.Run(context.Background(), jobs, got)
	if err != nil || again["a"].Attempts != 1 {
		t.Fatalf("unchanged work repeated: %+v %v", again, err)
	}
	jobs[0].Fingerprint = "changed"
	again, err = w.Run(context.Background(), jobs, again)
	if err != nil || again["a"].Attempts != 2 {
		t.Fatalf("changed fingerprint not recomputed: %+v %v", again, err)
	}
	cancel, stop := context.WithCancel(context.Background())
	stop()
	if _, err = w.Run(cancel, []Job{{ID: "cancel"}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestCorruptAndUnsupportedIsolation(t *testing.T) {
	corrupt, unsupported := []byte("RIFFbad"), unsupportedWAV()
	if root := os.Getenv("JSTREAMER_FIXTURES"); root != "" {
		if !filepath.IsAbs(root) {
			root = filepath.Join("..", "..", root)
		}
		corrupt = readEncodedFixture(t, filepath.Join(root, "corrupt.wav.b64"))
		unsupported = readEncodedFixture(t, filepath.Join(root, "unsupported.wav.b64"))
	}
	jobs := []Job{{ID: "good", Fingerprint: "g", WAV: rhythmicChord(8000, 3)}, {ID: "corrupt", WAV: corrupt}, {ID: "unsupported", WAV: unsupported}}
	got, err := NewWorker(2).Run(context.Background(), jobs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["good"].Status != StatusComplete || got["corrupt"].Status != StatusFailed || got["unsupported"].Status != StatusFailed {
		t.Fatalf("isolated results = %+v", got)
	}
	if got["corrupt"].FailureReason == "" || len(got["corrupt"].Vector) != 0 {
		t.Fatalf("failure persistence = %+v", got["corrupt"])
	}
}

func rhythmicChord(rate, seconds int) []byte {
	s := make([]int16, rate*seconds)
	tones := []float64{220, 277.1826, 329.6276}
	for i := range s {
		amp := .22
		if i%(rate/2) < rate/100 {
			amp += .7
		}
		var x float64
		for _, hz := range tones {
			x += math.Sin(2*math.Pi*hz*float64(i)/float64(rate)) / 3
		}
		s[i] = int16(amp * x * 32767)
	}
	return pcmWAV(rate, 1, s)
}
func pcmWAV(rate, channels int, samples []int16) []byte {
	b := make([]byte, 44+len(samples)*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(samples)*2))
	for i, x := range samples {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(x))
	}
	return b
}
func unsupportedWAV() []byte {
	b := pcmWAV(8000, 1, make([]int16, 800))
	binary.LittleEndian.PutUint16(b[20:], 3)
	return b
}

func TestCancellationDuringDSP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AnalyzeWAV(ctx, rhythmicChord(44100, 20))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestQuantizerRejectsNonFinite(t *testing.T) {
	f := Features{SampleRate: 44100, Tempo: math.NaN()}
	if _, err := quantize(f); err == nil {
		t.Fatal("NaN accepted")
	}
}

func readEncodedFixture(t *testing.T, path string) []byte {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWorkerRejectsDuplicateJobIDsBeforeExecution(t *testing.T) {
	var calls atomic.Int32
	w := NewWorker(2)
	w.analyze = func(context.Context, []byte) (Features, error) { calls.Add(1); return Features{}, nil }
	_, err := w.Run(context.Background(), []Job{{ID: "duplicate"}, {ID: "duplicate"}}, nil)
	if !errors.Is(err, ErrDuplicateJobID) || calls.Load() != 0 {
		t.Fatalf("duplicate result = %v calls=%d", err, calls.Load())
	}
}
