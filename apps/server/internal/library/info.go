package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"unicode"
)

const (
	maximumInfoTagBytes      = 1 << 20
	maximumInfoTagValueBytes = 64 << 10
	maximumInfoTagKeyBytes   = 255
)

type tagCollector struct {
	values  map[string][]string
	bytes   int
	entries int
}

func newTagCollector() *tagCollector {
	return &tagCollector{values: make(map[string][]string)}
}

func (collector *tagCollector) add(key, value string) {
	if collector.entries >= maximumMetadataEntries {
		return
	}
	key = cleanTagKey(key)
	if strings.EqualFold(key, "METADATA_BLOCK_PICTURE") || strings.EqualFold(key, "COVERART") ||
		strings.EqualFold(key, "APIC") || strings.EqualFold(key, "PIC") || strings.EqualFold(key, "COVR") {
		return
	}
	value = cleanTagValue(value)
	if key == "" || value == "" || len(value) > maximumInfoTagValueBytes || collector.bytes > maximumInfoTagBytes-len(key)-len(value) {
		return
	}
	values := collector.values[key]
	if len(values) >= maximumMetadataValues {
		return
	}
	for _, existing := range values {
		if existing == value {
			return
		}
	}
	collector.values[key] = append(values, value)
	collector.bytes += len(key) + len(value)
	collector.entries++
}

func cleanTagKey(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "")
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	if len(value) > maximumInfoTagKeyBytes {
		return ""
	}
	return value
}

func cleanTagValue(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	return normalizeDisplay(value)
}

func intPointer(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func int64Pointer(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

// Info opens the catalogued file through the same descriptor-safe path as playback
// and inspects it only for this request. No inspection result is persisted.
func (service *Service) Info(ctx context.Context, id string) (TrackInfo, error) {
	file, track, err := service.Open(ctx, id)
	if err != nil {
		return TrackInfo{}, err
	}
	defer file.Close()

	audio, tags := inspectTrackFile(file, track.Format, track.Size)
	if err := ctx.Err(); err != nil {
		return TrackInfo{}, err
	}
	return TrackInfo{Track: track, Audio: audio, Tags: tags}, nil
}

func inspectTrackFile(file *os.File, format string, size int64) (AudioProperties, map[string][]string) {
	collector := newTagCollector()
	var audio AudioProperties
	switch format {
	case "flac":
		audio = inspectFLAC(file, size, collector)
	case "mp3":
		audio = inspectMP3(file, size, collector)
	case "wav":
		audio = inspectWAV(file, size, collector)
	case "ogg", "opus":
		audio = inspectOgg(file, size, collector)
	case "m4a":
		audio = inspectMP4(file, size, collector)
	}
	return audio, collector.values
}

func addVorbisTags(collector *tagCollector, comments map[string][]string) {
	for key, values := range comments {
		if key == "METADATA_BLOCK_PICTURE" || key == "COVERART" {
			continue
		}
		for _, value := range values {
			collector.add(key, value)
		}
	}
}

func inspectFLAC(file *os.File, size int64, collector *tagCollector) AudioProperties {
	audio := AudioProperties{}
	if size < 8 {
		return audio
	}
	var signature [4]byte
	if _, err := file.ReadAt(signature[:], 0); err != nil || string(signature[:]) != "fLaC" {
		return audio
	}
	audio.Codec = "FLAC"
	offset := int64(4)
	metadataBytes := int64(0)
	for range maximumFLACBlocks {
		if offset > size-4 {
			break
		}
		var header [4]byte
		if _, err := file.ReadAt(header[:], offset); err != nil {
			break
		}
		last := header[0]&0x80 != 0
		kind := header[0] & 0x7f
		length := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		payloadOffset := offset + 4
		if length > size-payloadOffset || metadataBytes > maximumTagBytes-4-length {
			break
		}
		metadataBytes += 4 + length
		switch kind {
		case 0:
			if length == 34 {
				var streamInfo [34]byte
				if _, err := file.ReadAt(streamInfo[:], payloadOffset); err == nil {
					packed := binary.BigEndian.Uint64(streamInfo[10:18])
					rate := int64(packed >> 44)
					channels := int(packed>>41&7) + 1
					bits := int(packed>>36&31) + 1
					audio.SampleRate = int64Pointer(rate)
					audio.Channels = intPointer(channels)
					audio.BitsPerSample = intPointer(bits)
				}
			}
		case 4:
			if length > 0 {
				payload := make([]byte, int(length))
				if _, err := file.ReadAt(payload, payloadOffset); err == nil {
					if comments, err := parseVorbisComments(payload); err == nil {
						addVorbisTags(collector, comments)
					}
				}
			}
		}
		offset = payloadOffset + length
		if last {
			break
		}
	}
	return audio
}

func inspectMP3(file *os.File, size int64, collector *tagCollector) AudioProperties {
	audioStart, validStart := readID3Info(file, size, collector)
	readID3v1Info(file, size, collector)
	if !validStart || audioStart > size-4 {
		return AudioProperties{}
	}
	readSize := int64(1 << 20)
	if size-audioStart < readSize {
		readSize = size - audioStart
	}
	data := make([]byte, int(readSize))
	count, _ := file.ReadAt(data, audioStart)
	data = data[:count]
	start := findMP3Frame(data, 0)
	if start < 0 {
		return AudioProperties{}
	}
	rawHeader := binary.BigEndian.Uint32(data[start : start+4])
	header, ok := decodeMP3Header(rawHeader)
	if !ok || start+header.frameLength > len(data) {
		return AudioProperties{}
	}
	audio := AudioProperties{
		Codec:      "MP3",
		SampleRate: int64Pointer(header.sampleRate),
		Channels:   intPointer(2),
	}
	if header.mono {
		audio.Channels = intPointer(1)
	}
	frame := data[start : start+header.frameLength]
	crcBytes := 0
	if rawHeader>>16&1 == 0 {
		crcBytes = 2
	}
	sideInfo := 17
	if header.version == 1 {
		sideInfo = 32
	}
	if header.mono {
		if header.version == 1 {
			sideInfo = 17
		} else {
			sideInfo = 9
		}
	}
	xing := 4 + crcBytes + sideInfo
	if xing+12 <= len(frame) && string(frame[xing:xing+4]) == "Info" {
		audio.BitRate = int64Pointer(header.bitrate * 1000)
		return audio
	}
	if xing+12 <= len(frame) && string(frame[xing:xing+4]) == "Xing" {
		flags := binary.BigEndian.Uint32(frame[xing+4 : xing+8])
		position := xing + 8
		var frames, streamBytes uint32
		if flags&1 != 0 && position+4 <= len(frame) {
			frames = binary.BigEndian.Uint32(frame[position : position+4])
			position += 4
		}
		if flags&2 != 0 && position+4 <= len(frame) {
			streamBytes = binary.BigEndian.Uint32(frame[position : position+4])
		}
		audio.BitRate = mp3AverageBitRate(streamBytes, frames, header)
		return audio
	}
	vbri := 4 + 32
	if vbri+18 <= len(frame) && string(frame[vbri:vbri+4]) == "VBRI" {
		streamBytes := binary.BigEndian.Uint32(frame[vbri+10 : vbri+14])
		frames := binary.BigEndian.Uint32(frame[vbri+14 : vbri+18])
		audio.BitRate = mp3AverageBitRate(streamBytes, frames, header)
		return audio
	}
	position := start
	for range 24 {
		if position+4 > len(data) {
			return audio
		}
		candidate, valid := decodeMP3Header(binary.BigEndian.Uint32(data[position : position+4]))
		if !valid || candidate.bitrate != header.bitrate || candidate.sampleRate != header.sampleRate {
			return audio
		}
		position += candidate.frameLength
	}
	audio.BitRate = int64Pointer(header.bitrate * 1000)
	return audio
}

func mp3AverageBitRate(streamBytes, frames uint32, header mp3Header) *int64 {
	if streamBytes == 0 || frames == 0 || header.samplesFrame <= 0 || header.sampleRate <= 0 {
		return nil
	}
	units := int64(frames) * header.samplesFrame
	if units <= 0 {
		return nil
	}
	bitsPerSecond := int64(streamBytes) * 8 * header.sampleRate / units
	return int64Pointer(bitsPerSecond)
}

func readID3Info(file *os.File, size int64, collector *tagCollector) (int64, bool) {
	if size < 10 {
		return 0, true
	}
	var header [10]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return 0, false
	}
	if string(header[:3]) != "ID3" {
		return 0, true
	}
	if header[3] < 2 || header[3] > 4 {
		return 0, false
	}
	tagSize, ok := decodeSynchsafe32(header[6:10])
	if !ok || tagSize > maximumTagBytes || int64(tagSize) > size-10 {
		return 0, false
	}
	data := make([]byte, tagSize)
	if _, err := file.ReadAt(data, 10); err != nil {
		return 0, false
	}
	if header[3] == 4 && header[5]&0x10 != 0 {
		if len(data) < 10 || string(data[len(data)-10:len(data)-7]) != "3DI" {
			return 0, false
		}
		data = data[:len(data)-10]
	}
	parseID3InfoFrames(data, int(header[3]), header[5], collector)
	start := int64(10 + tagSize)
	if start > size {
		return 0, false
	}
	return start, true
}

func parseID3InfoFrames(data []byte, version int, tagFlags byte, collector *tagCollector) {
	offset := 0
	if tagFlags&0x40 != 0 {
		if len(data) < 4 {
			return
		}
		if version == 3 {
			extended := int(binary.BigEndian.Uint32(data[:4]))
			if extended < 0 || extended > len(data)-4 {
				return
			}
			offset = 4 + extended
		} else if version == 4 {
			extended, ok := decodeSynchsafe32(data[:4])
			if !ok || extended < 4 || extended > len(data) {
				return
			}
			offset = extended
		}
	}
	for range maximumMetadataEntries {
		if offset >= len(data) {
			return
		}
		name, payload, frameFlags, next, ok := nextID3Frame(data, offset, version)
		if !ok || name == "" {
			return
		}
		offset = next
		if id3FrameUnsupported(version, frameFlags) {
			continue
		}
		payload, ok = prepareID3Payload(payload, version, frameFlags)
		if !ok {
			return
		}
		if tagFlags&0x80 != 0 || version == 4 && frameFlags&0x0002 != 0 {
			payload = removeID3Unsynchronisation(payload)
		}
		key := id3InfoKey(name)
		switch {
		case name == "TXXX" || name == "TXX":
			description, value := decodeID3DescribedText(payload, 0, false)
			if description != "" {
				key = strings.ToUpper(description)
			} else {
				key = "CUSTOM"
			}
			collector.add(key, value)
		case strings.HasPrefix(name, "T"):
			collector.add(key, decodeID3TextFrame(payload))
		case name == "COMM" || name == "COM":
			_, value := decodeID3DescribedText(payload, 3, false)
			collector.add("COMMENT", value)
		case name == "USLT" || name == "ULT":
			_, value := decodeID3DescribedText(payload, 3, false)
			collector.add("LYRICS", value)
		case strings.HasPrefix(name, "W") && name != "WXXX" && name != "WXX":
			collector.add(key, string(payload))
		case name == "WXXX" || name == "WXX":
			description, value := decodeID3DescribedText(payload, 0, true)
			if description != "" {
				key = "URL:" + description
			}
			collector.add(key, value)
		}
	}
}

func decodeID3DescribedText(payload []byte, prefix int, url bool) (string, string) {
	if len(payload) < 1+prefix {
		return "", ""
	}
	encoding := payload[0]
	text := payload[1+prefix:]
	end := id3Terminator(text, encoding)
	if end < 0 {
		return "", ""
	}
	description := decodeID3String(encoding, text[:end])
	end++
	if encoding == 1 || encoding == 2 {
		end++
	}
	if end > len(text) {
		return "", ""
	}
	if url {
		return cleanTagValue(description), string(text[end:])
	}
	return cleanTagValue(description), decodeID3String(encoding, text[end:])
}

func id3InfoKey(name string) string {
	switch name {
	case "TIT2", "TT2":
		return "TITLE"
	case "TPE1", "TP1":
		return "ARTIST"
	case "TALB", "TAL":
		return "ALBUM"
	case "TPE2", "TP2":
		return "ALBUMARTIST"
	case "TRCK", "TRK":
		return "TRACKNUMBER"
	case "TPOS", "TPA":
		return "DISCNUMBER"
	case "TCON", "TCO":
		return "GENRE"
	case "TDRC", "TYER", "TYE", "TDAT", "TDA":
		return "DATE"
	case "TCOM", "TCM":
		return "COMPOSER"
	case "TIT1", "TT1":
		return "GROUPING"
	case "TCOP", "TCR":
		return "COPYRIGHT"
	case "TPUB", "TPB":
		return "PUBLISHER"
	case "TSRC", "TRC":
		return "ISRC"
	case "TBPM", "TBP":
		return "BPM"
	case "TEXT", "TXT":
		return "LYRICIST"
	case "TENC", "TEN":
		return "ENCODEDBY"
	default:
		return name
	}
}

func readID3v1Info(file *os.File, size int64, collector *tagCollector) {
	if size < 128 {
		return
	}
	var data [128]byte
	if _, err := file.ReadAt(data[:], size-128); err != nil || string(data[:3]) != "TAG" {
		return
	}
	clean := func(value []byte) string { return strings.Trim(string(value), "\x00 ") }
	collector.add("TITLE", clean(data[3:33]))
	collector.add("ARTIST", clean(data[33:63]))
	collector.add("ALBUM", clean(data[63:93]))
	collector.add("DATE", clean(data[93:97]))
	commentEnd := 127
	if data[125] == 0 && data[126] != 0 {
		commentEnd = 125
		collector.add("TRACKNUMBER", strconv.Itoa(int(data[126])))
	}
	collector.add("COMMENT", clean(data[97:commentEnd]))
}

func inspectOgg(file *os.File, size int64, collector *tagCollector) AudioProperties {
	packets := oggPackets(file, size, 3)
	audio := AudioProperties{}
	for _, packet := range packets {
		switch {
		case bytes.HasPrefix(packet, []byte("OpusHead")) && len(packet) >= 19:
			audio.Codec = "Opus"
			audio.SampleRate = int64Pointer(48000)
			audio.Channels = intPointer(int(packet[9]))
		case bytes.HasPrefix(packet, []byte("\x01vorbis")) && len(packet) >= 30:
			audio.Codec = "Vorbis"
			audio.Channels = intPointer(int(packet[11]))
			audio.SampleRate = int64Pointer(int64(binary.LittleEndian.Uint32(packet[12:16])))
			nominal := int64(int32(binary.LittleEndian.Uint32(packet[20:24])))
			if nominal <= 0 {
				maximum := int64(int32(binary.LittleEndian.Uint32(packet[16:20])))
				minimum := int64(int32(binary.LittleEndian.Uint32(packet[24:28])))
				if maximum > 0 && maximum == minimum {
					nominal = maximum
				}
			}
			audio.BitRate = int64Pointer(nominal)
		case bytes.HasPrefix(packet, []byte("OpusTags")):
			if comments, err := parseVorbisComments(packet[8:]); err == nil {
				addVorbisTags(collector, comments)
			}
		case bytes.HasPrefix(packet, []byte("\x03vorbis")):
			if comments, err := parseVorbisComments(packet[7:]); err == nil {
				addVorbisTags(collector, comments)
			}
		}
	}
	return audio
}

func inspectWAV(file *os.File, size int64, collector *tagCollector) AudioProperties {
	audio := AudioProperties{}
	if size < 12 {
		return audio
	}
	var riff [12]byte
	if _, err := file.ReadAt(riff[:], 0); err != nil || string(riff[:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return audio
	}
	limit := size
	if declaredEnd := 8 + int64(binary.LittleEndian.Uint32(riff[4:8])); declaredEnd >= 12 && declaredEnd < limit {
		limit = declaredEnd
	}
	metadataRead := int64(0)
	formatSeen := false
	for offset, entries := int64(12), 0; offset <= limit-8 && entries < maximumMetadataEntries; entries++ {
		var header [8]byte
		if _, err := file.ReadAt(header[:], offset); err != nil {
			break
		}
		length := int64(binary.LittleEndian.Uint32(header[4:8]))
		payloadOffset := offset + 8
		padding := length & 1
		if length+padding > limit-payloadOffset {
			break
		}
		switch string(header[:4]) {
		case "fmt ":
			if formatSeen {
				return AudioProperties{}
			}
			formatSeen = true
			if length >= 16 {
				var payload [40]byte
				count := min(length, int64(len(payload)))
				metadataRead += count
				if _, err := file.ReadAt(payload[:count], payloadOffset); err == nil {
					audio = wavAudioProperties(payload[:count])
				}
			}
		case "LIST":
			if length <= 4<<20 && metadataRead <= maximumTagBytes-length {
				metadataRead += length
				payload := make([]byte, int(length))
				if _, err := file.ReadAt(payload, payloadOffset); err == nil {
					addWAVInfoTags(payload, collector)
				}
			}
		}
		offset = payloadOffset + length + padding
	}
	return audio
}

func wavAudioProperties(payload []byte) AudioProperties {
	if len(payload) < 16 {
		return AudioProperties{}
	}
	formatTag := binary.LittleEndian.Uint16(payload[:2])
	channels := int(binary.LittleEndian.Uint16(payload[2:4]))
	rate := int64(binary.LittleEndian.Uint32(payload[4:8]))
	byteRate := int64(binary.LittleEndian.Uint32(payload[8:12]))
	bits := int(binary.LittleEndian.Uint16(payload[14:16]))
	codec := ""
	meaningfulBits := false
	switch formatTag {
	case 1:
		codec, meaningfulBits = "PCM", true
	case 2:
		codec = "Microsoft ADPCM"
	case 3:
		codec, meaningfulBits = "IEEE Float", true
	case 6:
		codec = "A-law"
	case 7:
		codec = "Mu-law"
	case 0x11:
		codec = "IMA ADPCM"
	case 0x55:
		codec = "MP3"
	case 0xfffe:
		if len(payload) >= 40 && bytes.Equal(payload[28:40], []byte{0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}) {
			subformat := binary.LittleEndian.Uint32(payload[24:28])
			switch subformat {
			case 1:
				codec, meaningfulBits = "PCM", true
			case 3:
				codec, meaningfulBits = "IEEE Float", true
			}
			validBits := int(binary.LittleEndian.Uint16(payload[18:20]))
			if meaningfulBits && validBits > 0 && validBits <= bits {
				bits = validBits
			}
		}
	}
	audio := AudioProperties{
		Codec:      codec,
		SampleRate: int64Pointer(rate),
		Channels:   intPointer(channels),
	}
	if meaningfulBits {
		audio.BitsPerSample = intPointer(bits)
	}
	if byteRate <= (1<<63-1)/8 {
		audio.BitRate = int64Pointer(byteRate * 8)
	}
	return audio
}

func addWAVInfoTags(payload []byte, collector *tagCollector) {
	if len(payload) < 4 || string(payload[:4]) != "INFO" {
		return
	}
	for offset, entries := 4, 0; offset+8 <= len(payload) && entries < maximumMetadataEntries; entries++ {
		key := string(payload[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(payload[offset+4 : offset+8]))
		offset += 8
		if length < 0 || length > len(payload)-offset {
			return
		}
		collector.add(wavInfoKey(key), strings.Trim(string(payload[offset:offset+length]), "\x00 "))
		if length+length%2 > len(payload)-offset {
			return
		}
		offset += length + length%2
	}
}

func wavInfoKey(key string) string {
	switch key {
	case "INAM":
		return "TITLE"
	case "IART":
		return "ARTIST"
	case "IPRD":
		return "ALBUM"
	case "ICRD":
		return "DATE"
	case "IGNR":
		return "GENRE"
	case "ITRK", "IPRT":
		return "TRACKNUMBER"
	case "ICMT":
		return "COMMENT"
	case "ICOP":
		return "COPYRIGHT"
	case "IWRI":
		return "WRITER"
	case "ISFT":
		return "SOFTWARE"
	default:
		return key
	}
}

type mp4InfoBudget struct {
	atoms        int
	metadataRead int64
}

func inspectMP4(file *os.File, size int64, collector *tagCollector) AudioProperties {
	audio := AudioProperties{}
	if size < 8 {
		return audio
	}
	budget := &mp4InfoBudget{}
	walkMP4Info(file, 0, size, 0, budget, collector, &audio)
	return audio
}

func walkMP4Info(file *os.File, start, limit int64, depth int, budget *mp4InfoBudget, collector *tagCollector, audio *AudioProperties) {
	if depth > maximumMP4Depth {
		return
	}
	for offset := start; offset < limit; {
		if limit-offset < 8 || budget.atoms >= maximumMetadataEntries {
			return
		}
		budget.atoms++
		atom, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return
		}
		payloadStart := atom.offset + atom.header
		payloadLimit := atom.offset + atom.size
		switch atom.kind {
		case "moov", "trak", "mdia", "minf", "stbl", "udta":
			walkMP4Info(file, payloadStart, payloadLimit, depth+1, budget, collector, audio)
		case "meta":
			if payloadLimit-payloadStart >= 4 {
				walkMP4Info(file, payloadStart+4, payloadLimit, depth+1, budget, collector, audio)
			}
		case "ilst":
			readMP4InfoItems(file, payloadStart, payloadLimit, budget, collector)
		case "stsd":
			if audio.Codec == "" {
				readMP4SampleDescription(file, payloadStart, payloadLimit, budget, audio)
			}
		}
		offset = payloadLimit
	}
}

func readMP4InfoItems(file *os.File, start, limit int64, budget *mp4InfoBudget, collector *tagCollector) {
	for offset := start; offset < limit && budget.atoms < maximumMetadataEntries; {
		budget.atoms++
		item, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return
		}
		itemLimit := item.offset + item.size
		for childOffset := item.offset + item.header; childOffset < itemLimit && budget.atoms < maximumMetadataEntries; {
			budget.atoms++
			dataAtom, valid := readMP4Atom(file, childOffset, itemLimit)
			if !valid {
				return
			}
			if dataAtom.kind == "data" {
				payloadOffset := dataAtom.offset + dataAtom.header
				payloadLength := dataAtom.size - dataAtom.header
				if payloadLength >= 8 {
					var typed [8]byte
					if _, err := file.ReadAt(typed[:], payloadOffset); err == nil {
						class := binary.BigEndian.Uint32(typed[:4]) & 0x00ffffff
						readMP4InfoValue(file, item.kind, class, payloadOffset+8, payloadLength-8, budget, collector)
					}
				}
			}
			childOffset = dataAtom.offset + dataAtom.size
		}
		offset = itemLimit
	}
}

var mp4InfoTextAtoms = map[string]string{
	"\xa9nam": "TITLE",
	"\xa9ART": "ARTIST",
	"\xa9art": "ARTIST",
	"aART":    "ALBUMARTIST",
	"\xa9alb": "ALBUM",
	"\xa9gen": "GENRE",
	"\xa9day": "DATE",
	"\xa9wrt": "COMPOSER",
	"\xa9cmt": "COMMENT",
	"\xa9grp": "GROUPING",
	"\xa9lyr": "LYRICS",
	"\xa9wrk": "WORK",
	"cprt":    "COPYRIGHT",
	"\xa9cpy": "COPYRIGHT",
	"\xa9too": "ENCODEDBY",
	"\xa9des": "DESCRIPTION",
	"desc":    "DESCRIPTION",
	"ldes":    "DESCRIPTION",
	"catg":    "CATEGORY",
	"keyw":    "KEYWORDS",
	"purl":    "PODCASTURL",
	"egid":    "EPISODEID",
	"soal":    "ALBUMSORT",
	"soar":    "ARTISTSORT",
	"sonm":    "TITLESORT",
	"soco":    "COMPOSERSORT",
}

func readMP4InfoValue(file *os.File, name string, class uint32, offset, length int64, budget *mp4InfoBudget, collector *tagCollector) {
	if class == 1 || class == 2 {
		key, known := mp4InfoTextAtoms[name]
		if !known {
			key = name
			for _, character := range []byte(key) {
				if character < 0x20 || character > 0x7e {
					key = ""
					break
				}
			}
		}
		if key == "" || length < 0 || length > maximumTagStringBytes || budget.metadataRead > maximumTagBytes-length {
			return
		}
		budget.metadataRead += length
		value := make([]byte, int(length))
		if _, err := file.ReadAt(value, offset); err != nil {
			return
		}
		if class == 2 {
			collector.add(key, decodeID3String(2, value))
		} else {
			collector.add(key, string(value))
		}
		return
	}
	if (name == "trkn" || name == "disk") && class == 0 && length >= 6 && length <= 32 {
		value := make([]byte, int(length))
		if _, err := file.ReadAt(value, offset); err != nil {
			return
		}
		number := binary.BigEndian.Uint16(value[2:4])
		total := binary.BigEndian.Uint16(value[4:6])
		if number == 0 {
			return
		}
		text := strconv.Itoa(int(number))
		if total > 0 {
			text += "/" + strconv.Itoa(int(total))
		}
		key := "TRACKNUMBER"
		if name == "disk" {
			key = "DISCNUMBER"
		}
		collector.add(key, text)
	}
}

func readMP4SampleDescription(file *os.File, start, limit int64, budget *mp4InfoBudget, audio *AudioProperties) {
	if limit-start < 8 {
		return
	}
	var header [8]byte
	if _, err := file.ReadAt(header[:], start); err != nil {
		return
	}
	count := int(binary.BigEndian.Uint32(header[4:8]))
	if count > maximumMetadataEntries {
		return
	}
	offset := start + 8
	for range count {
		if offset >= limit || budget.atoms >= maximumMetadataEntries {
			return
		}
		budget.atoms++
		entry, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return
		}
		codec, lossless := mp4AudioCodec(entry.kind)
		if codec != "" && entry.size-entry.header >= 28 {
			base := make([]byte, 28)
			if _, err := file.ReadAt(base, entry.offset+entry.header); err == nil {
				channels := int(binary.BigEndian.Uint16(base[16:18]))
				bits := int(binary.BigEndian.Uint16(base[18:20]))
				rate := int64(binary.BigEndian.Uint32(base[24:28]) >> 16)
				audio.Codec = codec
				audio.Channels = intPointer(channels)
				audio.SampleRate = int64Pointer(rate)
				if entry.kind == "mp4a" {
					// Sample-entry channels/rate may be placeholders. Only the
					// decoder configuration can verify the AAC output format.
					*audio = AudioProperties{Codec: "AAC (unverified)"}
				}
				if lossless {
					audio.BitsPerSample = intPointer(bits)
				}
				version := binary.BigEndian.Uint16(base[8:10])
				fixedLength := int64(28)
				switch version {
				case 1:
					fixedLength = 44
				case 2:
					fixedLength = 64
				}
				readMP4AudioChildren(file, entry, fixedLength, audio)
			}
			return
		}
		offset = entry.offset + entry.size
	}
}

func mp4AudioCodec(kind string) (string, bool) {
	switch kind {
	case "mp4a":
		return "AAC", false
	case "alac":
		return "ALAC", true
	case "fLaC":
		return "FLAC", true
	case "Opus":
		return "Opus", false
	case "ac-3":
		return "AC-3", false
	case "ec-3":
		return "E-AC-3", false
	case "lpcm", "sowt", "twos", "in24", "in32", "fl32", "fl64":
		return "PCM", true
	default:
		return "", false
	}
}

func readMP4AudioChildren(file *os.File, entry mp4Atom, fixedLength int64, audio *AudioProperties) {
	start := entry.offset + entry.header + fixedLength
	limit := entry.offset + entry.size
	decoderSeen := false
	for offset, children := start, 0; offset <= limit-8 && children < maximumMetadataEntries; children++ {
		child, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return
		}
		payloadOffset := child.offset + child.header
		payloadLength := child.size - child.header
		switch child.kind {
		case "alac":
			configOffset := payloadOffset
			if payloadLength >= 28 {
				configOffset += 4
			} else if payloadLength < 24 {
				break
			}
			var config [24]byte
			if _, err := file.ReadAt(config[:], configOffset); err == nil {
				audio.BitsPerSample = intPointer(int(config[5]))
				audio.Channels = intPointer(int(config[9]))
				// Encoders may fill avgBitRate with the PCM rate, not the ALAC stream rate.
				audio.BitRate = nil
				audio.SampleRate = int64Pointer(int64(binary.BigEndian.Uint32(config[20:24])))
			}
		case "esds":
			if entry.kind != "mp4a" {
				break
			}
			if decoderSeen {
				*audio = AudioProperties{Codec: "AAC (unverified)"}
				return
			}
			decoderSeen = true
			if payloadLength > 4 && payloadLength <= 64<<10 {
				payload := make([]byte, int(payloadLength))
				if _, err := file.ReadAt(payload, payloadOffset); err == nil {
					*audio = mp4ESDSAudioProperties(payload[4:])
				}
			}
		}
		offset = child.offset + child.size
	}
}

func mp4DescriptorLength(data []byte) (int, int, bool) {
	length := 0
	for index := range min(len(data), 4) {
		if length > (1<<28-1)>>7 {
			return 0, 0, false
		}
		length = length<<7 | int(data[index]&0x7f)
		if data[index]&0x80 == 0 {
			return length, index + 1, true
		}
	}
	return 0, 0, false
}
