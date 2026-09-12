package library

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestScanSurvivesMalformedFLACPictureAndMP4TypedTitle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "oversized-picture.flac"), testMalformedFLACPicture(64<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	mp4 := testMP4File(testMP4Item("\xa9nam", 21, []byte{7}), nil)
	if len(mp4) != 85 {
		t.Fatalf("malformed MP4 fixture length = %d, want 85", len(mp4))
	}
	if err := os.WriteFile(filepath.Join(root, "typed-title.m4a"), mp4, 0o600); err != nil {
		t.Fatal(err)
	}

	service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitScan(t, service, job.ID)
	if job.Status != "complete" || job.Discovered != 2 || job.Added != 2 || job.Errors != 0 {
		t.Fatalf("scan = %+v", job)
	}
	page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	tracks := page.Items.([]Track)
	if len(tracks) != 2 {
		t.Fatalf("tracks = %#v", tracks)
	}
	for _, track := range tracks {
		if track.Title != "oversized-picture" && track.Title != "typed-title" {
			t.Fatalf("unexpected fallback metadata: %+v", track)
		}
	}
}

func TestBoundedParsersPreserveSupportedMetadataArtworkAndDuration(t *testing.T) {
	t.Run("ID3", func(t *testing.T) {
		data := testID3File(map[string]string{
			"TIT2": "ID3 Title", "TPE1": "ID3 Artist", "TALB": "ID3 Album",
			"TPE2": "ID3 Album Artist", "TRCK": "2/9", "TPOS": "1/2", "TCON": "Jazz; Fusion",
		}, []byte{0xff, 0xd8, 0xff, 0xd9})
		media := readTestMedia(t, "song.mp3", data)
		if media.metadata.Title != "ID3 Title" || media.metadata.Artist != "ID3 Artist" || media.metadata.Album != "ID3 Album" || media.metadata.AlbumArtist != "ID3 Album Artist" || media.metadata.Track != 2 || media.metadata.Disc != 1 {
			t.Fatalf("metadata = %+v", media.metadata)
		}
		if len(media.metadata.Genres) != 2 || media.artworkMime != "image/jpeg" || !bytes.Equal(media.artwork, []byte{0xff, 0xd8, 0xff, 0xd9}) {
			t.Fatalf("genres/artwork = %#v %q %x", media.metadata.Genres, media.artworkMime, media.artwork)
		}
	})

	t.Run("FLAC", func(t *testing.T) {
		media := readTestMedia(t, "song.flac", testFLACFile())
		if media.metadata.Title != "FLAC Title" || media.metadata.Artist != "FLAC Artist" || media.metadata.Album != "FLAC Album" || media.metadata.Track != 3 || media.durationMS != 2000 {
			t.Fatalf("media = %+v", media)
		}
		if media.artworkMime != "image/jpeg" || !bytes.Equal(media.artwork, []byte{0xff, 0xd8, 0xff, 0xd9}) {
			t.Fatalf("artwork = %q %x", media.artworkMime, media.artwork)
		}
	})

	t.Run("OggVorbis", func(t *testing.T) {
		media := readTestMedia(t, "song.ogg", testVorbisFile())
		if media.metadata.Title != "Vorbis Title" || media.metadata.Artist != "Vorbis Artist" || media.metadata.Track != 6 || media.durationMS != 1000 {
			t.Fatalf("media = %+v", media)
		}
	})

	t.Run("Opus", func(t *testing.T) {
		media := readTestMedia(t, "song.opus", testOpusFile())
		if media.metadata.Title != "Opus Title" || media.metadata.Artist != "Opus Artist" || media.metadata.Track != 4 || media.durationMS != 1000 {
			t.Fatalf("media = %+v", media)
		}
	})

	t.Run("M4A", func(t *testing.T) {
		items := bytes.Join([][]byte{
			testMP4Item("\xa9nam", 1, []byte("M4A Title")),
			testMP4Item("\xa9ART", 1, []byte("M4A Artist")),
			testMP4Item("\xa9alb", 1, []byte("M4A Album")),
			testMP4Item("aART", 1, []byte("M4A Album Artist")),
			testMP4Item("\xa9gen", 1, []byte("Rock; Pop")),
			testMP4Item("trkn", 0, []byte{0, 0, 0, 5, 0, 9, 0, 0}),
			testMP4Item("disk", 0, []byte{0, 0, 0, 2, 0, 2, 0, 0}),
			testMP4Item("covr", 13, []byte{0xff, 0xd8, 0xff, 0xd9}),
		}, nil)
		media := readTestMedia(t, "song.m4a", testMP4File(items, testMP4MovieHeader(1000, 2000)))
		if media.metadata.Title != "M4A Title" || media.metadata.Artist != "M4A Artist" || media.metadata.Album != "M4A Album" || media.metadata.AlbumArtist != "M4A Album Artist" || media.metadata.Track != 5 || media.metadata.Disc != 2 || media.durationMS != 2000 {
			t.Fatalf("media = %+v", media)
		}
		if len(media.metadata.Genres) != 2 || media.artworkMime != "image/jpeg" || !bytes.Equal(media.artwork, []byte{0xff, 0xd8, 0xff, 0xd9}) {
			t.Fatalf("genres/artwork = %#v %q %x", media.metadata.Genres, media.artworkMime, media.artwork)
		}
	})
}

func TestVorbisPictureLengthIsCheckedInsideDecodedPayload(t *testing.T) {
	picture := make([]byte, 32)
	binary.BigEndian.PutUint32(picture[28:32], 64<<20)
	comments := map[string][]string{
		"METADATA_BLOCK_PICTURE": {base64.StdEncoding.EncodeToString(picture)},
	}
	if artwork, mime := vorbisPicture(comments); artwork != nil || mime != "" {
		t.Fatalf("artwork = %x, mime = %q", artwork, mime)
	}
}
func TestParserStructuralBudgetsRejectTinyMalformedInputs(t *testing.T) {
	t.Run("ID3TagLength", func(t *testing.T) {
		header := []byte{'I', 'D', '3', 4, 0, 0, 0x7f, 0x7f, 0x7f, 0x7f}
		media := readTestMedia(t, "oversized.mp3", header)
		if media.metadata.Title != "oversized" {
			t.Fatalf("fallback metadata = %+v", media.metadata)
		}
	})

	t.Run("VorbisCommentCount", func(t *testing.T) {
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint32(payload[4:8], ^uint32(0))
		if _, err := parseVorbisComments(payload); err == nil {
			t.Fatal("accepted impossible Vorbis comment count")
		}
	})

	t.Run("MP4AtomLength", func(t *testing.T) {
		header := make([]byte, 8)
		binary.BigEndian.PutUint32(header[:4], ^uint32(0))
		copy(header[4:], "moov")
		if err := readTestMP4MetadataError(t, append(testMP4Atom("ftyp", []byte("M4A ")), header...)); err == nil {
			t.Fatal("accepted MP4 atom beyond its enclosing file")
		}
	})

	t.Run("MP4Nesting", func(t *testing.T) {
		nested := testMP4Atom("free", nil)
		for range maximumMP4Depth + 2 {
			nested = testMP4Atom("moov", nested)
		}
		if err := readTestMP4MetadataError(t, nested); err == nil {
			t.Fatal("accepted excessive MP4 atom nesting")
		}
	})

	t.Run("MP4AtomCount", func(t *testing.T) {
		data := make([]byte, 0, (maximumMetadataEntries+1)*8)
		for range maximumMetadataEntries + 1 {
			data = append(data, testMP4Atom("free", nil)...)
		}
		if err := readTestMP4MetadataError(t, data); err == nil {
			t.Fatal("accepted excessive MP4 atom count")
		}
	})
}

func readTestMP4MetadataError(t *testing.T, data []byte) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "metadata.m4a")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _, _, err = readMP4Metadata(file, int64(len(data)))
	return err
}

func readTestMedia(t *testing.T, name string, data []byte) mediaInfo {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	media, err := readMedia(file, name, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return media
}

func testMalformedFLACPicture(pictureLength uint32) []byte {
	payload := make([]byte, 32)
	binary.BigEndian.PutUint32(payload[28:32], pictureLength)
	return append([]byte{'f', 'L', 'a', 'C', 0x86, 0, 0, 32}, payload...)
}

func testID3File(text map[string]string, picture []byte) []byte {
	var frames bytes.Buffer
	for _, name := range []string{"TIT2", "TPE1", "TALB", "TPE2", "TRCK", "TPOS", "TCON"} {
		value := text[name]
		if value == "" {
			continue
		}
		testWriteID3Frame(&frames, name, append([]byte{0}, []byte(value)...))
	}
	if len(picture) > 0 {
		payload := append([]byte{0}, []byte("image/jpeg")...)
		payload = append(payload, 0, 3, 0)
		payload = append(payload, picture...)
		testWriteID3Frame(&frames, "APIC", payload)
	}
	body := frames.Bytes()
	header := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 0}
	testPutSynchsafe(header[6:10], len(body))
	return append(header, body...)
}

func testWriteID3Frame(target *bytes.Buffer, name string, payload []byte) {
	target.WriteString(name)
	binary.Write(target, binary.BigEndian, uint32(len(payload)))
	target.Write([]byte{0, 0})
	target.Write(payload)
}

func testPutSynchsafe(target []byte, value int) {
	target[0] = byte(value >> 21)
	target[1] = byte(value >> 14 & 0x7f)
	target[2] = byte(value >> 7 & 0x7f)
	target[3] = byte(value & 0x7f)
}

func testFLACFile() []byte {
	streamInfo := make([]byte, 34)
	packed := uint64(48000)<<44 | uint64(96000)
	binary.BigEndian.PutUint64(streamInfo[10:18], packed)
	comments := testVorbisCommentPayload([]string{
		"TITLE=FLAC Title", "ARTIST=FLAC Artist", "ALBUM=FLAC Album",
		"ALBUMARTIST=FLAC Album Artist", "TRACKNUMBER=3/10", "DISCNUMBER=1/1", "GENRE=Jazz",
	})
	picture := testFLACPicture([]byte{0xff, 0xd8, 0xff, 0xd9})
	result := []byte("fLaC")
	result = append(result, testFLACBlock(0, streamInfo)...)
	result = append(result, testFLACBlock(4, comments)...)
	result = append(result, testFLACBlock(0x80|6, picture)...)
	return result
}

func testFLACBlock(kind byte, payload []byte) []byte {
	result := []byte{kind, byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))}
	return append(result, payload...)
}

func testFLACPicture(picture []byte) []byte {
	var result bytes.Buffer
	binary.Write(&result, binary.BigEndian, uint32(3))
	binary.Write(&result, binary.BigEndian, uint32(len("image/jpeg")))
	result.WriteString("image/jpeg")
	binary.Write(&result, binary.BigEndian, uint32(0))
	for range 4 {
		binary.Write(&result, binary.BigEndian, uint32(1))
	}
	binary.Write(&result, binary.BigEndian, uint32(len(picture)))
	result.Write(picture)
	return result.Bytes()
}

func testVorbisCommentPayload(comments []string) []byte {
	var result bytes.Buffer
	binary.Write(&result, binary.LittleEndian, uint32(4))
	result.WriteString("test")
	binary.Write(&result, binary.LittleEndian, uint32(len(comments)))
	for _, comment := range comments {
		binary.Write(&result, binary.LittleEndian, uint32(len(comment)))
		result.WriteString(comment)
	}
	return result.Bytes()
}

func testOpusFile() []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 2
	tags := append([]byte("OpusTags"), testVorbisCommentPayload([]string{"TITLE=Opus Title", "ARTIST=Opus Artist", "TRACKNUMBER=4"})...)
	return testOggPage(48000, head, tags)
}

func testVorbisFile() []byte {
	head := make([]byte, 30)
	copy(head, "\x01vorbis")
	binary.LittleEndian.PutUint32(head[12:16], 44100)
	tags := append([]byte("\x03vorbis"), testVorbisCommentPayload([]string{"TITLE=Vorbis Title", "ARTIST=Vorbis Artist", "TRACKNUMBER=6"})...)
	return testOggPage(44100, head, tags)
}

func testOggPage(granule uint64, packets ...[]byte) []byte {
	segments := make([]byte, len(packets))
	for index, packet := range packets {
		segments[index] = byte(len(packet))
	}
	page := make([]byte, 27+len(segments))
	copy(page, "OggS")
	binary.LittleEndian.PutUint64(page[6:14], granule)
	page[26] = byte(len(segments))
	copy(page[27:], segments)
	for _, packet := range packets {
		page = append(page, packet...)
	}
	return page
}

func testMP4File(items, movieHeader []byte) []byte {
	ftyp := testMP4Atom("ftyp", []byte("M4A \x00\x00\x00\x00isomM4A "))
	ilst := testMP4Atom("ilst", items)
	meta := testMP4Atom("meta", append([]byte{0, 0, 0, 0}, ilst...))
	udta := testMP4Atom("udta", meta)
	moov := testMP4Atom("moov", append(movieHeader, udta...))
	return append(ftyp, moov...)
}

func testMP4Item(name string, class uint32, value []byte) []byte {
	typed := make([]byte, 8)
	binary.BigEndian.PutUint32(typed[:4], class)
	data := testMP4Atom("data", append(typed, value...))
	return testMP4Atom(name, data)
}

func testMP4MovieHeader(timescale, duration uint32) []byte {
	payload := make([]byte, 20)
	binary.BigEndian.PutUint32(payload[12:16], timescale)
	binary.BigEndian.PutUint32(payload[16:20], duration)
	return testMP4Atom("mvhd", payload)
}

func testMP4Atom(kind string, payload []byte) []byte {
	result := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(8+len(payload)))
	copy(result[4:8], kind)
	return append(result, payload...)
}
