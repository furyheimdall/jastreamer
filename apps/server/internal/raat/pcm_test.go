package raat

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

func pcmTestProperties(codec string, rate int64, channels, bits int) library.AudioProperties {
	return library.AudioProperties{Codec: codec, SampleRate: &rate, Channels: &channels, BitsPerSample: &bits}
}

func TestSelectAudioFormatPreservesPrecision(t *testing.T) {
	integer16 := audioFormat{"pcm", 48000, 16, 2}
	integer24 := audioFormat{"pcm", 48000, 24, 2}
	integer32 := audioFormat{"pcm", 48000, 32, 2}
	float32 := audioFormat{"pcm_float32", 48000, 32, 2}
	float64 := audioFormat{"pcm_float64", 48000, 64, 2}
	tests := []struct {
		name      string
		audio     library.AudioProperties
		supported []audioFormat
		want      audioFormat
	}{
		{"smallest lossless container", pcmTestProperties("FLAC", 48000, 2, 16), []audioFormat{integer32, integer24, integer16}, integer16},
		{"zero-pad integer", pcmTestProperties("ALAC", 48000, 2, 16), []audioFormat{integer24}, integer24},
		{"8-bit PCM in 16-bit", pcmTestProperties("PCM", 48000, 2, 8), []audioFormat{integer16}, integer16},
		{"20-bit ALAC in 24-bit", pcmTestProperties("ALAC", 48000, 2, 20), []audioFormat{integer16, integer24}, integer24},
		{"non-container FLAC precision", pcmTestProperties("FLAC", 48000, 2, 23), []audioFormat{integer24}, integer24},
		{"24-bit precision", pcmTestProperties("PCM", 48000, 2, 24), []audioFormat{integer16, integer32}, integer32},
		{"32-bit precision", pcmTestProperties("FLAC", 48000, 2, 32), []audioFormat{integer16, integer24, integer32}, integer32},
		{"native float32", pcmTestProperties("IEEE Float", 48000, 2, 32), []audioFormat{integer32, float64, float32}, float32},
		{"native float64", pcmTestProperties("IEEE Float", 48000, 2, 64), []audioFormat{float32, float64}, float64},
		{"MP3 is not 16-bit", pcmTestProperties("MP3", 48000, 2, 16), []audioFormat{integer16, integer32, integer24}, integer24},
		{"AAC high precision", pcmTestProperties("AAC", 48000, 2, 0), []audioFormat{integer32}, integer32},
		{"Opus high precision", pcmTestProperties("Opus", 48000, 2, 0), []audioFormat{integer24}, integer24},
		{"Vorbis high precision", pcmTestProperties("Vorbis", 48000, 2, 0), []audioFormat{integer24}, integer24},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectAudioFormat(test.audio, test.supported)
			if err != nil || got != test.want {
				t.Fatalf("selectAudioFormat = %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
}

func TestSelectAudioFormatRejectsLossOrUnknownSource(t *testing.T) {
	pcm := audioFormat{"pcm", 48000, 24, 2}
	tests := []struct {
		name      string
		audio     library.AudioProperties
		supported []audioFormat
		message   string
	}{
		{"no integer truncation", pcmTestProperties("FLAC", 48000, 2, 32), []audioFormat{pcm}, "without loss"},
		{"no resampling", pcmTestProperties("PCM", 44100, 2, 16), []audioFormat{pcm}, "without loss"},
		{"no downmix", pcmTestProperties("PCM", 48000, 6, 24), []audioFormat{pcm}, "without loss"},
		{"no float to integer", pcmTestProperties("IEEE Float", 48000, 2, 32), []audioFormat{pcm}, "without loss"},
		{"no float narrowing", pcmTestProperties("IEEE Float", 48000, 2, 64), []audioFormat{{"pcm_float32", 48000, 32, 2}}, "without loss"},
		{"no float widening", pcmTestProperties("IEEE Float", 48000, 2, 32), []audioFormat{{"pcm_float64", 48000, 64, 2}}, "without loss"},
		{"no invented float alias", pcmTestProperties("IEEE Float", 48000, 2, 32), []audioFormat{{"pcm_float", 48000, 32, 2}}, "without loss"},
		{"unknown codec", pcmTestProperties("DSD", 48000, 2, 24), []audioFormat{pcm}, "codec"},
		{"unverified AAC", pcmTestProperties("AAC (unverified)", 48000, 2, 0), []audioFormat{pcm}, "codec"},
		{"missing rate", library.AudioProperties{Codec: "PCM"}, []audioFormat{pcm}, "sample rate"},
		{"zero rate", pcmTestProperties("PCM", 0, 2, 24), []audioFormat{pcm}, "sample rate"},
		{"huge rate", pcmTestProperties("PCM", 1<<62, 2, 24), []audioFormat{pcm}, "sample rate"},
		{"zero channels", pcmTestProperties("PCM", 48000, 0, 24), []audioFormat{pcm}, "channel count"},
		{"huge channels", pcmTestProperties("PCM", 48000, 1<<30, 24), []audioFormat{pcm}, "channel count"},
		{"unknown PCM bits", pcmTestProperties("PCM", 48000, 2, 0), []audioFormat{pcm}, "precision"},
		{"unknown FLAC bits", pcmTestProperties("FLAC", 48000, 2, 0), []audioFormat{pcm}, "precision"},
		{"unknown ALAC bits", pcmTestProperties("ALAC", 48000, 2, 0), []audioFormat{pcm}, "precision"},
		{"unknown float bits", pcmTestProperties("IEEE Float", 48000, 2, 0), []audioFormat{pcm}, "precision"},
		{"negative precision", pcmTestProperties("PCM", 48000, 2, -1), []audioFormat{pcm}, "precision"},
		{"oversized integer precision", pcmTestProperties("FLAC", 48000, 2, 33), []audioFormat{pcm}, "precision"},
		{"lossy output needs precision", pcmTestProperties("MP3", 48000, 2, 0), []audioFormat{{"pcm", 48000, 16, 2}}, "without loss"},
		{"invalid advertised format", pcmTestProperties("PCM", 48000, 2, 24), []audioFormat{{"pcm", 48000, 64, 2}}, "without loss"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := selectAudioFormat(test.audio, test.supported)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want containing %q", err, test.message)
			}
		})
	}
	for _, codec := range []string{"PCM", "FLAC", "ALAC", "IEEE Float"} {
		audio := pcmTestProperties(codec, 48000, 2, 24)
		audio.BitsPerSample = nil
		if _, err := selectAudioFormat(audio, []audioFormat{pcm}); err == nil {
			t.Errorf("accepted missing %s precision", codec)
		}
	}
}

func TestAudioFormatBounds(t *testing.T) {
	for _, format := range []audioFormat{
		{"pcm", 1, 16, 1}, {"pcm", maximumPCMSampleRate, 32, maximumPCMChannels},
		{"pcm_float32", 48000, 32, 2}, {"pcm_float64", maximumPCMSampleRate, 64, maximumPCMChannels},
	} {
		if !format.valid() || format.frameBytes() != format.bits/8*format.channels {
			t.Errorf("valid bounded format rejected: %+v", format)
		}
	}
	for _, format := range []audioFormat{
		{}, {"pcm", 0, 16, 2}, {"pcm", maximumPCMSampleRate + 1, 16, 2},
		{"pcm", 48000, 16, 0}, {"pcm", 48000, 16, maximumPCMChannels + 1},
		{"pcm", 48000, 1 << 30, 1 << 30}, {"pcm", 48000, 20, 2},
		{"pcm_float32", 48000, 64, 2}, {"pcm_float64", 48000, 32, 2},
		{"dsd", 48000, 16, 2}, {"pcm_float", 48000, 32, 2},
	} {
		if format.valid() || format.frameBytes() != 0 {
			t.Errorf("unsafe or unknown format accepted: %+v", format)
		}
	}
}

func pcmTestWAVFormat(format audioFormat, extensible bool) []byte {
	length := 16
	if extensible {
		length = 40
	}
	payload := make([]byte, length)
	tag := uint16(1)
	if format.sampleType != "pcm" {
		tag = 3
	}
	binary.LittleEndian.PutUint16(payload[:2], tag)
	binary.LittleEndian.PutUint16(payload[2:4], uint16(format.channels))
	binary.LittleEndian.PutUint32(payload[4:8], uint32(format.sampleRate))
	binary.LittleEndian.PutUint32(payload[8:12], uint32(format.sampleRate*format.frameBytes()))
	binary.LittleEndian.PutUint16(payload[12:14], uint16(format.frameBytes()))
	binary.LittleEndian.PutUint16(payload[14:16], uint16(format.bits))
	if extensible {
		binary.LittleEndian.PutUint16(payload[:2], 0xfffe)
		binary.LittleEndian.PutUint16(payload[16:18], 22)
		binary.LittleEndian.PutUint16(payload[18:20], uint16(format.bits))
		binary.LittleEndian.PutUint16(payload[24:26], tag)
		copy(payload[28:], []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71})
	}
	return payload
}

func pcmTestChunk(name string, payload []byte) []byte {
	result := make([]byte, 8+len(payload)+len(payload)%2)
	copy(result, name)
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(payload)))
	copy(result[8:], payload)
	return result
}

func pcmTestRIFF(chunks ...[]byte) []byte {
	result := []byte("RIFF\x00\x00\x00\x00WAVE")
	for _, chunk := range chunks {
		result = append(result, chunk...)
	}
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(result)-8))
	return result
}

func TestReadPCMHeaderPreservesPayload(t *testing.T) {
	for _, format := range []audioFormat{{"pcm", 44100, 16, 2}, {"pcm", 96000, 24, 1}, {"pcm", 192000, 32, 6}, {"pcm_float32", 48000, 32, 2}, {"pcm_float64", 96000, 64, 2}} {
		for _, extensible := range []bool{false, true} {
			pcm := bytes.Repeat([]byte{0xa5}, format.frameBytes()*3)
			wav := pcmTestRIFF(pcmTestChunk("JUNK", []byte{1, 2, 3}), pcmTestChunk("fmt ", pcmTestWAVFormat(format, extensible)), pcmTestChunk("data", pcm))
			reader := bytes.NewReader(wav)
			length, err := readPCMHeader(reader, format)
			if err != nil || length != int64(len(pcm)) {
				t.Fatalf("%+v extensible=%v: length=%d, error=%v", format, extensible, length, err)
			}
			actual := make([]byte, len(pcm))
			if _, err := io.ReadFull(reader, actual); err != nil || !bytes.Equal(actual, pcm) {
				t.Fatalf("parser consumed or changed PCM: %x, %v", actual, err)
			}
		}
	}
}

func TestReadPCMHeaderStreamingAndRF64(t *testing.T) {
	format := audioFormat{"pcm", 48000, 24, 2}
	fmtChunk := pcmTestChunk("fmt ", pcmTestWAVFormat(format, true))
	dataChunk := pcmTestChunk("data", bytes.Repeat([]byte{0x5a}, 12))
	streaming := pcmTestRIFF(fmtChunk, dataChunk)
	binary.LittleEndian.PutUint32(streaming[4:8], ^uint32(0))
	binary.LittleEndian.PutUint32(streaming[12+len(fmtChunk)+4:12+len(fmtChunk)+8], ^uint32(0))
	if size, err := readPCMHeader(bytes.NewReader(streaming), format); err != nil || size != -1 {
		t.Fatalf("streaming WAV size=%d error=%v", size, err)
	}
	ds64 := make([]byte, 28)
	binary.LittleEndian.PutUint64(ds64[:8], uint64(4+36+len(fmtChunk)+len(dataChunk)))
	binary.LittleEndian.PutUint64(ds64[8:16], 12)
	binary.LittleEndian.PutUint64(ds64[16:24], 2)
	rf64 := pcmTestRIFF(pcmTestChunk("ds64", ds64), fmtChunk, dataChunk)
	copy(rf64[:4], "RF64")
	binary.LittleEndian.PutUint32(rf64[4:8], ^uint32(0))
	binary.LittleEndian.PutUint32(rf64[12+36+len(fmtChunk)+4:12+36+len(fmtChunk)+8], ^uint32(0))
	if size, err := readPCMHeader(bytes.NewReader(rf64), format); err != nil || size != 12 {
		t.Fatalf("RF64 size=%d error=%v", size, err)
	}
	for _, offset := range []int{20, 28, 36} {
		malformed := bytes.Clone(rf64)
		binary.LittleEndian.PutUint64(malformed[offset:offset+8], ^uint64(0))
		if _, err := readPCMHeader(bytes.NewReader(malformed), format); err == nil {
			t.Errorf("accepted overflowing RF64 field at %d", offset)
		}
	}
}

func TestReadPCMHeaderRejectsMalformedOrMismatchedOutput(t *testing.T) {
	format := audioFormat{"pcm", 48000, 24, 2}
	validFormat := pcmTestWAVFormat(format, true)
	fmtChunk := pcmTestChunk("fmt ", validFormat)
	dataChunk := pcmTestChunk("data", make([]byte, 12))
	valid := pcmTestRIFF(fmtChunk, dataChunk)
	cases := map[string][]byte{
		"not WAV":           []byte("not a WAV file"),
		"data first":        pcmTestRIFF(dataChunk, fmtChunk),
		"duplicate format":  pcmTestRIFF(fmtChunk, fmtChunk, dataChunk),
		"short format":      pcmTestRIFF(pcmTestChunk("fmt ", make([]byte, 15)), dataChunk),
		"unaligned PCM":     pcmTestRIFF(fmtChunk, pcmTestChunk("data", make([]byte, 5))),
		"huge chunk":        pcmTestRIFF([]byte{'J', 'U', 'N', 'K', 255, 255, 255, 255}),
		"trailing chunks":   pcmTestRIFF(fmtChunk, dataChunk, pcmTestChunk("JUNK", nil)),
		"RF64 without ds64": append([]byte("RF64\xff\xff\xff\xff"), valid[8:]...),
		"ds64 in RIFF":      pcmTestRIFF(pcmTestChunk("ds64", make([]byte, 28)), fmtChunk, dataChunk),
	}
	for _, field := range []struct {
		name   string
		offset int
		value  uint16
	}{
		{"channels", 2, 1}, {"rate", 4, 44100}, {"byte rate", 8, 1}, {"alignment", 12, 8},
		{"container bits", 14, 32}, {"extension length", 16, 21}, {"valid bits", 18, 16},
		{"zero valid bits", 18, 0}, {"float subformat", 24, 3}, {"unknown GUID", 28, 1},
	} {
		payload := bytes.Clone(validFormat)
		binary.LittleEndian.PutUint16(payload[field.offset:field.offset+2], field.value)
		cases[field.name] = pcmTestRIFF(pcmTestChunk("fmt ", payload), dataChunk)
	}
	tooMany := []byte("RIFF\xff\xff\xff\xffWAVE")
	for range 128 {
		tooMany = append(tooMany, pcmTestChunk("JUNK", nil)...)
	}
	cases["chunk count bound"] = append(tooMany, valid[12:]...)
	for name, wav := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := readPCMHeader(bytes.NewReader(wav), format); err == nil {
				t.Fatal("accepted malformed or mismatched WAV")
			}
		})
	}
	// Every truncation inside the header must fail, including missing padding.
	for cut := range len(valid) - 12 {
		if _, err := readPCMHeader(bytes.NewReader(valid[:cut]), format); err == nil {
			t.Errorf("accepted header truncated at %d", cut)
		}
	}
}
