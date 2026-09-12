package library

import (
	"bytes"
	"encoding/binary"
	"os"
)

func mediaDuration(file *os.File, format string, size int64) int64 {
	switch format {
	case "flac":
		return flacDuration(file)
	case "mp3":
		return mp3Duration(file, size)
	case "ogg", "opus":
		return oggDuration(file, format, size)
	case "m4a":
		return mp4Duration(file, size)
	default:
		return 0
	}
}

func flacDuration(file *os.File) int64 {
	var header [42]byte
	if _, err := file.ReadAt(header[:], 0); err != nil || string(header[:4]) != "fLaC" || header[4]&0x7f != 0 || header[5] != 0 || int(header[6])<<8|int(header[7]) != 34 {
		return 0
	}
	packed := binary.BigEndian.Uint64(header[18:26])
	rate := packed >> 44
	samples := packed & 0x0000000fffffffff
	if rate == 0 || samples == 0 {
		return 0
	}
	return int64(samples * 1000 / rate)
}

var mpeg1Layer3Bitrates = [...]int64{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var mpeg2Layer3Bitrates = [...]int64{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
var mpegSampleRates = [...]int64{44100, 48000, 32000, 0}

type mp3Header struct {
	version      int
	bitrate      int64
	sampleRate   int64
	frameLength  int
	samplesFrame int64
	mono         bool
}

func decodeMP3Header(value uint32) (mp3Header, bool) {
	if value&0xffe00000 != 0xffe00000 || value>>17&3 != 1 || value>>19&3 == 1 {
		return mp3Header{}, false
	}
	versionBits := int(value >> 19 & 3)
	bitrateIndex := int(value >> 12 & 15)
	rateIndex := int(value >> 10 & 3)
	var version int
	var bitrate int64
	sampleRate := mpegSampleRates[rateIndex]
	switch versionBits {
	case 3:
		version = 1
		bitrate = mpeg1Layer3Bitrates[bitrateIndex]
	case 2:
		version = 2
		bitrate = mpeg2Layer3Bitrates[bitrateIndex]
		sampleRate /= 2
	case 0:
		version = 25
		bitrate = mpeg2Layer3Bitrates[bitrateIndex]
		sampleRate /= 4
	}
	if bitrate == 0 || sampleRate == 0 {
		return mp3Header{}, false
	}
	padding := int(value >> 9 & 1)
	coefficient := int64(144)
	samples := int64(1152)
	if version != 1 {
		coefficient = 72
		samples = 576
	}
	length := int(coefficient*bitrate*1000/sampleRate) + padding
	if length < 24 {
		return mp3Header{}, false
	}
	return mp3Header{version: version, bitrate: bitrate, sampleRate: sampleRate, frameLength: length, samplesFrame: samples, mono: value>>6&3 == 3}, true
}

func findMP3Frame(data []byte, start int) int {
	for index := start; index+4 <= len(data); index++ {
		if _, ok := decodeMP3Header(binary.BigEndian.Uint32(data[index : index+4])); ok {
			return index
		}
	}
	return -1
}

func mp3Duration(file *os.File, size int64) int64 {
	readSize := int64(1 << 20)
	if size < readSize {
		readSize = size
	}
	if readSize < 4 {
		return 0
	}
	data := make([]byte, int(readSize))
	count, _ := file.ReadAt(data, 0)
	data = data[:count]
	searchStart := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		if (data[6]|data[7]|data[8]|data[9])&0x80 != 0 {
			return 0
		}
		searchStart = 10 + int(data[6])<<21 + int(data[7])<<14 + int(data[8])<<7 + int(data[9])
		if data[5]&0x10 != 0 {
			searchStart += 10
		}
	}
	start := findMP3Frame(data, searchStart)
	if start < 0 {
		return 0
	}
	header, ok := decodeMP3Header(binary.BigEndian.Uint32(data[start : start+4]))
	if !ok || start+header.frameLength > len(data) {
		return 0
	}
	frame := data[start : start+header.frameLength]
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
	xing := 4 + sideInfo
	if xing+12 <= len(frame) && (string(frame[xing:xing+4]) == "Xing" || string(frame[xing:xing+4]) == "Info") {
		flags := binary.BigEndian.Uint32(frame[xing+4 : xing+8])
		if flags&1 != 0 {
			frames := binary.BigEndian.Uint32(frame[xing+8 : xing+12])
			if frames > 0 {
				return int64(frames) * header.samplesFrame * 1000 / header.sampleRate
			}
		}
	}
	vbri := 4 + 32
	if vbri+18 <= len(frame) && string(frame[vbri:vbri+4]) == "VBRI" {
		frames := binary.BigEndian.Uint32(frame[vbri+14 : vbri+18])
		if frames > 0 {
			return int64(frames) * header.samplesFrame * 1000 / header.sampleRate
		}
	}
	// Only extrapolate when a bounded sample proves the stream constant bitrate.
	position := start
	framesChecked := 0
	for framesChecked < 24 && position+4 <= len(data) {
		candidate, valid := decodeMP3Header(binary.BigEndian.Uint32(data[position : position+4]))
		if !valid || candidate.bitrate != header.bitrate || candidate.sampleRate != header.sampleRate {
			return 0
		}
		position += candidate.frameLength
		framesChecked++
	}
	if framesChecked < 2 || size <= int64(start) {
		return 0
	}
	audioBytes := size - int64(start)
	if audioBytes > (1<<63-1)/8 {
		return 0
	}
	return audioBytes * 8 / header.bitrate
}

func oggDuration(file *os.File, format string, size int64) int64 {
	frontSize := int64(128 << 10)
	if size < frontSize {
		frontSize = size
	}
	if frontSize <= 0 {
		return 0
	}
	front := make([]byte, int(frontSize))
	count, _ := file.ReadAt(front, 0)
	front = front[:count]
	var rate int64
	var preSkip int64
	if format == "opus" {
		index := bytes.Index(front, []byte("OpusHead"))
		if index < 0 || index+12 > len(front) {
			return 0
		}
		rate = 48000
		preSkip = int64(binary.LittleEndian.Uint16(front[index+10 : index+12]))
	} else {
		index := bytes.Index(front, []byte("\x01vorbis"))
		if index < 0 || index+16 > len(front) {
			return 0
		}
		rate = int64(binary.LittleEndian.Uint32(front[index+12 : index+16]))
	}
	if rate <= 0 {
		return 0
	}
	tailSize := int64(256 << 10)
	if size < tailSize {
		tailSize = size
	}
	tail := make([]byte, int(tailSize))
	count, _ = file.ReadAt(tail, size-tailSize)
	tail = tail[:count]
	for index := len(tail) - 27; index >= 0; index-- {
		if index+14 <= len(tail) && string(tail[index:index+4]) == "OggS" {
			granule := binary.LittleEndian.Uint64(tail[index+6 : index+14])
			if granule != ^uint64(0) && granule > uint64(preSkip) {
				return scaledDurationMilliseconds(granule-uint64(preSkip), uint64(rate))
			}
		}
	}
	return 0
}
func scaledDurationMilliseconds(units, unitsPerSecond uint64) int64 {
	if units == 0 || unitsPerSecond == 0 {
		return 0
	}
	seconds := units / unitsPerSecond
	if seconds > uint64(1<<63-1)/1000 {
		return 0
	}
	return int64(seconds*1000 + units%unitsPerSecond*1000/unitsPerSecond)
}

type mp4Atom struct {
	offset, size, header int64
	kind                 string
}

func readMP4Atom(file *os.File, offset, limit int64) (mp4Atom, bool) {
	if offset < 0 || limit < 0 || offset > limit-8 {
		return mp4Atom{}, false
	}
	var header [16]byte
	if _, err := file.ReadAt(header[:8], offset); err != nil {
		return mp4Atom{}, false
	}
	remaining := limit - offset
	size := int64(binary.BigEndian.Uint32(header[:4]))
	headerSize := int64(8)
	if size == 1 {
		if remaining < 16 {
			return mp4Atom{}, false
		}
		if _, err := file.ReadAt(header[8:16], offset+8); err != nil {
			return mp4Atom{}, false
		}
		extended := binary.BigEndian.Uint64(header[8:16])
		if extended > uint64(remaining) {
			return mp4Atom{}, false
		}
		size = int64(extended)
		headerSize = 16
	} else if size == 0 {
		size = remaining
	}
	if size < headerSize || size > remaining {
		return mp4Atom{}, false
	}
	return mp4Atom{offset: offset, size: size, header: headerSize, kind: string(header[4:8])}, true
}

func mp4Duration(file *os.File, size int64) int64 {
	atoms := 0
	var walk func(start, limit int64, depth int) int64
	walk = func(start, limit int64, depth int) int64 {
		if depth > maximumMP4Depth {
			return 0
		}
		for offset := start; offset <= limit-8; {
			atoms++
			if atoms > maximumMetadataEntries {
				return 0
			}
			atom, ok := readMP4Atom(file, offset, limit)
			if !ok {
				return 0
			}
			if atom.kind == "mdhd" || atom.kind == "mvhd" {
				payloadLength := atom.size - atom.header
				if payloadLength >= 20 {
					var payload [32]byte
					readLength := int64(20)
					if payloadLength >= 32 {
						readLength = 32
					}
					if _, err := file.ReadAt(payload[:readLength], atom.offset+atom.header); err == nil {
						if payload[0] == 1 && readLength == 32 {
							timescale := binary.BigEndian.Uint32(payload[20:24])
							duration := binary.BigEndian.Uint64(payload[24:32])
							if milliseconds := mp4Milliseconds(duration, timescale); milliseconds > 0 {
								return milliseconds
							}
						} else if payload[0] == 0 {
							timescale := binary.BigEndian.Uint32(payload[12:16])
							duration := binary.BigEndian.Uint32(payload[16:20])
							if milliseconds := mp4Milliseconds(uint64(duration), timescale); milliseconds > 0 {
								return milliseconds
							}
						}
					}
				}
			}
			switch atom.kind {
			case "moov", "trak", "mdia":
				if duration := walk(atom.offset+atom.header, atom.offset+atom.size, depth+1); duration > 0 {
					return duration
				}
			}
			offset += atom.size
		}
		return 0
	}
	return walk(0, size, 0)
}

func mp4Milliseconds(duration uint64, timescale uint32) int64 {
	if duration == 0 || timescale == 0 {
		return 0
	}
	seconds := duration / uint64(timescale)
	if seconds > uint64(1<<63-1)/1000 {
		return 0
	}
	return int64(seconds*1000 + duration%uint64(timescale)*1000/uint64(timescale))
}
