package library

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInfoInspectsWAVMetadataOnDemand(t *testing.T) {
	root := t.TempDir()
	writeInfoTestWAV(t, filepath.Join(root, "song.wav"))
	service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitScan(t, service, job.ID)
	if job.Status != "complete" || job.Added != 1 || job.Errors != 0 {
		t.Fatalf("scan = %+v", job)
	}
	page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	tracks := page.Items.([]Track)
	info, err := service.Info(t.Context(), tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Audio.Codec != "PCM" || info.Audio.SampleRate == nil || *info.Audio.SampleRate != 44_100 ||
		info.Audio.Channels == nil || *info.Audio.Channels != 2 || info.Audio.BitsPerSample == nil ||
		*info.Audio.BitsPerSample != 16 || info.Audio.BitRate == nil || *info.Audio.BitRate != 1_411_200 {
		t.Fatalf("audio = %+v", info.Audio)
	}
	if !reflect.DeepEqual(info.Tags["TITLE"], []string{"Fixture title"}) ||
		!reflect.DeepEqual(info.Tags["ARTIST"], []string{"Fixture artist"}) ||
		!reflect.DeepEqual(info.Tags["DATE"], []string{"2026"}) ||
		!reflect.DeepEqual(info.Tags["TRACKNUMBER"], []string{"1/7"}) {
		t.Fatalf("tags = %#v", info.Tags)
	}
}

func TestInspectFLACExcludesEmbeddedPictureTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "song.flac")
	data := flacInfoFixture()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	audio, tags := inspectTrackFile(file, "flac", int64(len(data)))
	if audio.Codec != "FLAC" || audio.SampleRate == nil || *audio.SampleRate != 96_000 ||
		audio.Channels == nil || *audio.Channels != 2 || audio.BitsPerSample == nil || *audio.BitsPerSample != 24 ||
		audio.BitRate != nil {
		t.Fatalf("audio = %+v", audio)
	}
	if !reflect.DeepEqual(tags["TITLE"], []string{"Fixture title"}) || !reflect.DeepEqual(tags["GENRE"], []string{"Ambient"}) {
		t.Fatalf("tags = %#v", tags)
	}
	if _, exists := tags["METADATA_BLOCK_PICTURE"]; exists {
		t.Fatalf("picture bytes escaped into tags: %#v", tags)
	}
}

func TestMP3InfoHeaderPreservesConstantBitRate(t *testing.T) {
	data := make([]byte, 2*432)
	binary.BigEndian.PutUint32(data[:4], 0xfffb78c0)
	binary.BigEndian.PutUint32(data[432:436], 0xfffb78c0)
	copy(data[21:], "Info")
	binary.BigEndian.PutUint32(data[25:29], 3)
	binary.BigEndian.PutUint32(data[29:33], 1)
	binary.BigEndian.PutUint32(data[33:37], uint32(len(data)))
	path := filepath.Join(t.TempDir(), "short.mp3")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	audio, _ := inspectTrackFile(file, "mp3", int64(len(data)))
	if audio.BitRate == nil {
		t.Fatal("Info frame lost the constant bitrate")
	}
	if *audio.BitRate != 96_000 {
		t.Fatalf("Info frame inflated the constant bitrate: got %d, want 96000", *audio.BitRate)
	}
}

func writeInfoTestWAV(t *testing.T, path string) {
	t.Helper()
	var chunks bytes.Buffer
	var format bytes.Buffer
	binary.Write(&format, binary.LittleEndian, uint16(1))
	binary.Write(&format, binary.LittleEndian, uint16(2))
	binary.Write(&format, binary.LittleEndian, uint32(44_100))
	binary.Write(&format, binary.LittleEndian, uint32(176_400))
	binary.Write(&format, binary.LittleEndian, uint16(4))
	binary.Write(&format, binary.LittleEndian, uint16(16))
	writeInfoTestRIFFChunk(&chunks, "fmt ", format.Bytes())

	var list bytes.Buffer
	list.WriteString("INFO")
	writeInfoTestRIFFChunk(&list, "INAM", []byte("Fixture title\x00"))
	writeInfoTestRIFFChunk(&list, "IART", []byte("Fixture artist\x00"))
	writeInfoTestRIFFChunk(&list, "ICRD", []byte("2026\x00"))
	writeInfoTestRIFFChunk(&list, "IPRT", []byte("1/7\x00"))
	writeInfoTestRIFFChunk(&chunks, "LIST", list.Bytes())
	writeInfoTestRIFFChunk(&chunks, "data", make([]byte, 176_400))

	var data bytes.Buffer
	data.WriteString("RIFF")
	binary.Write(&data, binary.LittleEndian, uint32(4+chunks.Len()))
	data.WriteString("WAVE")
	data.Write(chunks.Bytes())
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeInfoTestRIFFChunk(target *bytes.Buffer, name string, payload []byte) {
	target.WriteString(name)
	binary.Write(target, binary.LittleEndian, uint32(len(payload)))
	target.Write(payload)
	if len(payload)%2 != 0 {
		target.WriteByte(0)
	}
}

func flacInfoFixture() []byte {
	var data bytes.Buffer
	data.WriteString("fLaC")
	data.Write([]byte{0, 0, 0, 34})
	streamInfo := make([]byte, 34)
	packed := uint64(96_000)<<44 | uint64(1)<<41 | uint64(23)<<36 | 96_000
	binary.BigEndian.PutUint64(streamInfo[10:18], packed)
	data.Write(streamInfo)

	var comments bytes.Buffer
	binary.Write(&comments, binary.LittleEndian, uint32(4))
	comments.WriteString("test")
	values := []string{"TITLE=Fixture title", "GENRE=Ambient", "METADATA_BLOCK_PICTURE=opaque-binary-placeholder"}
	binary.Write(&comments, binary.LittleEndian, uint32(len(values)))
	for _, value := range values {
		binary.Write(&comments, binary.LittleEndian, uint32(len(value)))
		comments.WriteString(value)
	}
	length := comments.Len()
	data.Write([]byte{0x84, byte(length >> 16), byte(length >> 8), byte(length)})
	data.Write(comments.Bytes())
	return data.Bytes()
}
