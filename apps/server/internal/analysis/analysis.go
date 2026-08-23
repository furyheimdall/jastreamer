package analysis

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/cmplx"
)

const (
	CurrentSchemaVersion = 1
	AnalyzerID           = "jstreamer-classical"
	AnalyzerVersion      = "1.0.0"
	NormalizerID         = "acoustic-linear"
	NormalizerVersion    = "1.0.0"
	VectorLength         = 32
	MaxAnalysisSamples   = 12 * 44100
	windowSeconds        = 4
	fftSize              = 2048
	hopSize              = 1024
)

var ErrUnsupported = errors.New("analysis: unsupported PCM format")
var ErrCorrupt = errors.New("analysis: corrupt WAV")

type Provenance struct {
	SchemaVersion                                                int
	AnalyzerID, AnalyzerVersion, NormalizerID, NormalizerVersion string
}

func CurrentProvenance() Provenance {
	return Provenance{CurrentSchemaVersion, AnalyzerID, AnalyzerVersion, NormalizerID, NormalizerVersion}
}

type Features struct {
	Provenance                                   Provenance
	ContentFingerprint                           string
	SampleRate, SourceSamples, AnalyzedSamples   int
	Tempo, Loudness, Centroid, Contrast, Rolloff float64
	Key                                          int
	Chroma, MFCC                                 [12]float64
	Vector                                       []byte
}

// AnalyzeWAV uses at most three four-second windows (start/middle/end). Parity
// tolerances before quantization are: tempo .25 BPM, loudness .05 dB,
// frequencies 1 Hz, and normalized chroma/MFCC .005. Quantization is normative.
func AnalyzeWAV(ctx context.Context, data []byte) (Features, error) {
	rate, source, samples, err := decodeWAVBytes(data)
	if err != nil {
		return Features{}, err
	}
	if err := ctx.Err(); err != nil {
		return Features{}, err
	}
	f := Features{Provenance: CurrentProvenance(), ContentFingerprint: fmt.Sprintf("%x", sha256.Sum256(data)), SampleRate: rate, SourceSamples: source, AnalyzedSamples: len(samples)}
	var square float64
	for _, x := range samples {
		v := float64(x) / 32768
		square += v * v
	}
	f.Loudness = 20 * math.Log10(math.Sqrt(square/float64(len(samples)))+1e-12)
	var chroma, bands [12]float64
	var centroid, rolloff, contrast float64
	frames := 0
	for start := 0; start+fftSize <= len(samples); start += hopSize {
		if frames&31 == 0 {
			if err := ctx.Err(); err != nil {
				return Features{}, err
			}
		}
		spectrum := fftFrame(samples[start : start+fftSize])
		total := 0.0
		for _, m := range spectrum {
			total += m
		}
		if total == 0 {
			continue
		}
		frames++
		centroid += spectralCentroid(spectrum, rate, total)
		rolloff += spectralRolloff(spectrum, rate, total)
		contrast += spectralContrast(spectrum)
		for bin, m := range spectrum[1:] {
			hz := float64(bin+1) * float64(rate) / fftSize
			if hz < 40 {
				continue
			}
			note := int(math.Round(12*math.Log2(hz/440))) + 69
			chroma[(note%12+12)%12] += m
			band := int(12 * math.Log2(hz/40) / math.Log2(float64(rate)/80))
			if band >= 0 && band < 12 {
				bands[band] += m
			}
		}
	}
	if frames == 0 {
		return Features{}, ErrCorrupt
	}
	f.Centroid = centroid / float64(frames)
	f.Rolloff = rolloff / float64(frames)
	f.Contrast = contrast / float64(frames)
	maxChroma := 0.0
	for _, v := range chroma {
		if v > maxChroma {
			maxChroma = v
		}
	}
	for i := range chroma {
		f.Chroma[i] = chroma[i] / maxChroma
		if f.Chroma[i] > f.Chroma[f.Key] {
			f.Key = i
		}
	}
	for k := range f.MFCC {
		for n, v := range bands {
			f.MFCC[k] += math.Log1p(v/float64(frames)) * math.Cos(math.Pi*float64(k)*(float64(n)+.5)/12)
		}
		f.MFCC[k] /= 12
	}
	f.Tempo = estimateTempo(samples, rate)
	f.Vector, err = quantize(f)
	if err != nil {
		return Features{}, err
	}
	return f, nil
}

func fftFrame(samples []int16) []float64 {
	x := make([]complex128, fftSize)
	for i, s := range samples {
		x[i] = complex(float64(s)/32768*(.5-.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))), 0)
	}
	for i, j := 1, 0; i < fftSize; i++ {
		bit := fftSize >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for n := 2; n <= fftSize; n <<= 1 {
		wlen := cmplx.Exp(complex(0, -2*math.Pi/float64(n)))
		for i := 0; i < fftSize; i += n {
			w := complex(1, 0)
			for j := 0; j < n/2; j++ {
				u, v := x[i+j], x[i+j+n/2]*w
				x[i+j], x[i+j+n/2] = u+v, u-v
				w *= wlen
			}
		}
	}
	o := make([]float64, fftSize/2)
	for i := range o {
		o[i] = cmplx.Abs(x[i])
	}
	return o
}
func spectralCentroid(s []float64, rate int, total float64) float64 {
	v := 0.0
	for i, m := range s {
		v += float64(i) * float64(rate) / fftSize * m
	}
	return v / total
}
func spectralRolloff(s []float64, rate int, total float64) float64 {
	sum := 0.0
	for i, m := range s {
		sum += m
		if sum >= .85*total {
			return float64(i) * float64(rate) / fftSize
		}
	}
	return float64(rate) / 2
}
func spectralContrast(s []float64) float64 {
	lo, hi := math.MaxFloat64, 0.0
	for _, m := range s[2:] {
		if m < lo {
			lo = m
		}
		if m > hi {
			hi = m
		}
	}
	return 20 * math.Log10((hi+1e-9)/(lo+1e-9))
}
func estimateTempo(samples []int16, rate int) float64 {
	hop := max(1, rate/100)
	energy := make([]float64, len(samples)/hop)
	for i := range energy {
		for _, x := range samples[i*hop : (i+1)*hop] {
			energy[i] += math.Abs(float64(x))
		}
	}
	on := make([]float64, len(energy))
	for i := 1; i < len(energy); i++ {
		on[i] = max(0, energy[i]-energy[i-1])
	}
	lo, hi := 30, min(len(on)-1, 100)
	best, lag := 0.0, lo
	for l := lo; l <= hi; l++ {
		score := 0.0
		for i := l; i < len(on); i++ {
			score += on[i] * on[i-l]
		}
		score /= float64(len(on) - l)
		if score > best {
			best, lag = score, l
		}
	}
	if lag > 75 {
		lag /= 2
	}
	return 6000 / float64(lag)
}

func quantize(f Features) ([]byte, error) {
	values := []float64{f.Tempo, f.Loudness, f.Centroid, f.Contrast, f.Rolloff}
	values = append(values, f.Chroma[:]...)
	values = append(values, f.MFCC[:]...)
	for _, x := range values {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, errors.New("analysis: non-finite feature")
		}
	}
	v := make([]byte, VectorLength)
	q := func(x, lo, hi float64) byte { return byte(math.Round(min(1, max(0, (x-lo)/(hi-lo))) * 255)) }
	v[0] = q(f.Tempo, 40, 240)
	v[1] = byte(f.Key)
	v[2] = q(f.Loudness, -80, 0)
	v[3] = q(f.Centroid, 0, float64(f.SampleRate)/2)
	v[4] = q(f.Contrast, 0, 120)
	v[5] = q(f.Rolloff, 0, float64(f.SampleRate)/2)
	for i := 0; i < 12; i++ {
		v[6+i] = q(f.Chroma[i], 0, 1)
		v[18+i] = q(f.MFCC[i], -20, 20)
	}
	v[30] = CurrentSchemaVersion
	v[31] = 1
	return v, nil
}
