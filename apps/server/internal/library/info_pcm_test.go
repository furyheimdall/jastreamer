package library

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestInfoMP4PCMRepresentation(t *testing.T) {
	for _, test := range []struct {
		name     string
		kind     string
		version  uint16
		bits     uint32
		flags    uint32
		rate     float64
		channels uint32
		truncate int
		codec    string
		wantBits int
	}{
		{name: "legacy float32 placeholder", kind: "fl32", codec: "IEEE Float", wantBits: 32},
		{name: "legacy float64 placeholder", kind: "fl64", codec: "IEEE Float", wantBits: 64},
		{name: "version1 float32", kind: "fl32", version: 1, codec: "IEEE Float", wantBits: 32},
		{name: "version1 float64", kind: "fl64", version: 1, codec: "IEEE Float", wantBits: 64},
		{name: "legacy integer little endian", kind: "sowt", codec: "PCM", wantBits: 16},
		{name: "legacy integer big endian", kind: "twos", codec: "PCM", wantBits: 16},
		{name: "version1 integer24 placeholder", kind: "in24", version: 1, codec: "PCM", wantBits: 24},
		{name: "version1 integer32 placeholder", kind: "in32", version: 1, codec: "PCM", wantBits: 32},
		{name: "lpcm float32", kind: "lpcm", version: 2, bits: 32, flags: 9, rate: 96000, channels: 6, codec: "IEEE Float", wantBits: 32},
		{name: "lpcm float64 big endian", kind: "lpcm", version: 2, bits: 64, flags: 11, rate: 192000, channels: 2, codec: "IEEE Float", wantBits: 64},
		{name: "lpcm integer24", kind: "lpcm", version: 2, bits: 24, flags: 12, rate: 96000, channels: 6, codec: "PCM", wantBits: 24},
		{name: "lpcm integer32 big endian", kind: "lpcm", version: 2, bits: 32, flags: 14, rate: 192000, channels: 2, codec: "PCM", wantBits: 32},
		{name: "lpcm unsigned integer", kind: "lpcm", version: 2, bits: 8, flags: 8, rate: 48000, channels: 1, codec: "PCM", wantBits: 8},
		{name: "lpcm missing flags version0", kind: "lpcm", codec: "LPCM (unverified)"},
		{name: "lpcm missing flags version1", kind: "lpcm", version: 1, codec: "LPCM (unverified)"},
		{name: "lpcm unknown version", kind: "lpcm", version: 3, codec: "LPCM (unverified)"},
		{name: "lpcm truncated version2", kind: "lpcm", version: 2, bits: 32, flags: 9, rate: 96000, channels: 2, truncate: 63, codec: "LPCM (unverified)"},
		{name: "lpcm unsupported float width", kind: "lpcm", version: 2, bits: 24, flags: 9, rate: 96000, channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm missing precision", kind: "lpcm", version: 2, rate: 96000, channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm missing channels", kind: "lpcm", version: 2, bits: 24, rate: 96000, codec: "LPCM (unverified)"},
		{name: "lpcm zero rate", kind: "lpcm", version: 2, bits: 24, channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm negative rate", kind: "lpcm", version: 2, bits: 24, rate: -96000, channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm fractional rate", kind: "lpcm", version: 2, bits: 24, rate: 96000.5, channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm infinite rate", kind: "lpcm", version: 2, bits: 24, rate: math.Inf(1), channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm NaN rate", kind: "lpcm", version: 2, bits: 24, rate: math.NaN(), channels: 2, codec: "LPCM (unverified)"},
		{name: "lpcm overflowing rate", kind: "lpcm", version: 2, bits: 24, rate: math.MaxFloat64, channels: 2, codec: "LPCM (unverified)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			length := 28
			if test.version == 1 {
				length = 44
			} else if test.version == 2 {
				length = 64
			}
			base := make([]byte, length)
			binary.BigEndian.PutUint16(base[6:8], 1)
			binary.BigEndian.PutUint16(base[8:10], test.version)
			binary.BigEndian.PutUint16(base[16:18], 2)
			binary.BigEndian.PutUint16(base[18:20], 16)
			binary.BigEndian.PutUint32(base[24:28], 44100<<16)
			if test.version == 2 {
				binary.BigEndian.PutUint32(base[28:32], 72)
				binary.BigEndian.PutUint64(base[32:40], math.Float64bits(test.rate))
				binary.BigEndian.PutUint32(base[40:44], test.channels)
				binary.BigEndian.PutUint32(base[44:48], 0x7f000000)
				binary.BigEndian.PutUint32(base[48:52], test.bits)
				binary.BigEndian.PutUint32(base[52:56], test.flags)
			}
			if test.truncate != 0 {
				base = base[:test.truncate]
			}
			stsd := testMP4Atom("stsd", append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, testMP4Atom(test.kind, base)...))
			trak := testMP4Atom("trak", testMP4Atom("mdia", testMP4Atom("minf", testMP4Atom("stbl", stsd))))
			moov := testMP4Atom("moov", append(testMP4MovieHeader(1000, 1000), trak...))
			content := append(testMP4Atom("ftyp", []byte("M4A \x00\x00\x00\x00isomM4A ")), moov...)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "pcm.m4a"), content, 0600); err != nil {
				t.Fatal(err)
			}
			service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
			job, err := service.StartScan(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if job = waitScan(t, service, job.ID); job.Status != "complete" || job.Added != 1 {
				t.Fatalf("fixture scan: %#v", job)
			}
			page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			info, err := service.Info(t.Context(), page.Items.([]Track)[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			audio := info.Audio
			if audio.Codec != test.codec {
				t.Fatalf("codec = %q, want %q", audio.Codec, test.codec)
			}
			if test.wantBits == 0 {
				if audio.BitsPerSample != nil || audio.Channels != nil || audio.SampleRate != nil {
					t.Fatalf("ambiguous LPCM advertised a verified format: %#v", audio)
				}
				return
			}
			wantRate, wantChannels := int64(44100), 2
			if test.version == 2 {
				wantRate, wantChannels = int64(test.rate), int(test.channels)
			}
			if audio.BitsPerSample == nil || *audio.BitsPerSample != test.wantBits ||
				audio.SampleRate == nil || *audio.SampleRate != wantRate ||
				audio.Channels == nil || *audio.Channels != wantChannels {
				t.Fatalf("incorrect PCM precision/layout: %#v; want %d-bit %d Hz %d channels", audio, test.wantBits, wantRate, wantChannels)
			}
		})
	}
}
