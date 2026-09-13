package library

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestInfoUsesAACDecoderChannelsInsteadOfSampleEntry(t *testing.T) {
	for _, test := range []struct {
		name     string
		asc      []byte
		channels int
	}{
		{"six channel decoder with stereo placeholder", []byte{0x12, 0x30}, 6},
		{"stereo LC decoder", []byte{0x12, 0x10}, 2},
		{"missing decoder configuration", nil, 0},
		{"unsupported AAC Main profile", []byte{0x0a, 0x10}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			base := make([]byte, 28)
			binary.BigEndian.PutUint16(base[6:8], 1)
			binary.BigEndian.PutUint16(base[16:18], 2)
			binary.BigEndian.PutUint16(base[18:20], 16)
			binary.BigEndian.PutUint32(base[24:28], 44100<<16)
			if test.asc != nil {
				decoder := make([]byte, 13)
				decoder[0], decoder[1] = 0x40, 0x15
				decoder = append(decoder, 5, byte(len(test.asc)))
				decoder = append(decoder, test.asc...)
				es := append([]byte{0, 1, 0, 4, byte(len(decoder))}, decoder...)
				esds := append([]byte{0, 0, 0, 0, 3, byte(len(es))}, es...)
				base = append(base, testMP4Atom("esds", esds)...)
			}
			stsd := testMP4Atom("stsd", append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, testMP4Atom("mp4a", base)...))
			trak := testMP4Atom("trak", testMP4Atom("mdia", testMP4Atom("minf", testMP4Atom("stbl", stsd))))
			moov := testMP4Atom("moov", append(testMP4MovieHeader(1000, 1000), trak...))
			content := append(testMP4Atom("ftyp", []byte("M4A \x00\x00\x00\x00isomM4A ")), moov...)
			if err := os.WriteFile(filepath.Join(root, "decoder.m4a"), content, 0600); err != nil {
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
			if test.channels == 0 {
				if info.Audio.Codec == "AAC" || info.Audio.Channels != nil || info.Audio.SampleRate != nil {
					t.Fatalf("unverified decoder advertised as native AAC: %#v", info.Audio)
				}
			} else if info.Audio.Codec != "AAC" || info.Audio.Channels == nil || *info.Audio.Channels != test.channels || info.Audio.SampleRate == nil || *info.Audio.SampleRate != 44100 {
				t.Fatalf("decoder configuration not reflected in audio properties: %#v", info.Audio)
			}
		})
	}
}

func TestWAVDuplicateFormatDoesNotVerifyConflictingAudio(t *testing.T) {
	var chunks bytes.Buffer
	first := make([]byte, 16)
	binary.LittleEndian.PutUint16(first, 1)
	binary.LittleEndian.PutUint16(first[2:], 6)
	binary.LittleEndian.PutUint32(first[4:], 96000)
	binary.LittleEndian.PutUint16(first[14:], 24)
	writeInfoTestRIFFChunk(&chunks, "fmt ", first)
	second := append([]byte(nil), first...)
	binary.LittleEndian.PutUint16(second[2:], 2)
	binary.LittleEndian.PutUint32(second[4:], 44100)
	binary.LittleEndian.PutUint16(second[14:], 16)
	writeInfoTestRIFFChunk(&chunks, "fmt ", second)
	content := make([]byte, 12)
	copy(content, "RIFF")
	binary.LittleEndian.PutUint32(content[4:], uint32(chunks.Len()+4))
	copy(content[8:], "WAVE")
	content = append(content, chunks.Bytes()...)
	path := filepath.Join(t.TempDir(), "ambiguous.wav")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	audio := inspectWAV(file, int64(len(content)), &tagCollector{})
	if audio.Codec != "" || audio.SampleRate != nil || audio.Channels != nil {
		t.Fatalf("a duplicate format hid the first audio layout: %#v", audio)
	}
}
