package library

import "encoding/binary"

// Descriptor payloads are slices of the already bounded esds atom. Never scan
// arbitrary bytes for a tag: an AudioSpecificConfig may contain the same byte.
func mp4Descriptor(data []byte) (byte, []byte, []byte, bool) {
	if len(data) < 2 {
		return 0, nil, nil, false
	}
	length, width, ok := mp4DescriptorLength(data[1:])
	start := 1 + width
	if !ok || length > len(data)-start {
		return 0, nil, nil, false
	}
	return data[0], data[start : start+length], data[start+length:], true
}

func mp4ESDSAudioProperties(data []byte) AudioProperties {
	unknown := AudioProperties{Codec: "AAC (unverified)"}
	tag, payload, _, ok := mp4Descriptor(data)
	if !ok {
		return unknown
	}
	if tag == 3 {
		if len(payload) < 3 {
			return unknown
		}
		flags, offset := payload[2], 3
		if flags&0x80 != 0 {
			offset += 2
		}
		if flags&0x40 != 0 {
			if offset >= len(payload) {
				return unknown
			}
			offset += 1 + int(payload[offset])
		}
		if flags&0x20 != 0 {
			offset += 2
		}
		if offset > len(payload) {
			return unknown
		}
		tag, payload, _, ok = mp4Descriptor(payload[offset:])
	}
	if !ok || tag != 4 || len(payload) < 13 || payload[1]>>2 != 5 {
		return unknown
	}
	bitRate := int64Pointer(int64(binary.BigEndian.Uint32(payload[9:13])))
	switch payload[0] {
	case 0x69, 0x6b:
		return AudioProperties{Codec: "MP3", BitRate: bitRate}
	case 0xa5:
		return AudioProperties{Codec: "AC-3", BitRate: bitRate}
	case 0xa6:
		return AudioProperties{Codec: "E-AC-3", BitRate: bitRate}
	case 0x40, 0x66, 0x67, 0x68:
	default:
		return unknown
	}
	tag, specific, _, ok := mp4Descriptor(payload[13:])
	if !ok || tag != 5 || len(specific) > 256 {
		return unknown
	}
	rate, channels, ok := aacOutputFormat(specific)
	if !ok {
		return unknown
	}
	return AudioProperties{Codec: "AAC", SampleRate: int64Pointer(rate), Channels: intPointer(channels), BitRate: bitRate}
}

type aacBits struct {
	data   []byte
	offset int
}

func (bits *aacBits) read(count int) (uint32, bool) {
	if count < 0 || count > 32 || count > len(bits.data)*8-bits.offset {
		return 0, false
	}
	var value uint32
	for range count {
		value = value<<1 | uint32((bits.data[bits.offset/8]>>uint(7-bits.offset%8))&1)
		bits.offset++
	}
	return value, true
}

func (bits *aacBits) frequency() (int64, bool) {
	index, ok := bits.read(4)
	if !ok {
		return 0, false
	}
	if index == 15 {
		value, ok := bits.read(24)
		return int64(value), ok && value > 0
	}
	frequencies := [...]int64{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
	if index >= uint32(len(frequencies)) {
		return 0, false
	}
	return frequencies[index], true
}

func aacOutputFormat(data []byte) (int64, int, bool) {
	bits := aacBits{data: data}
	object, ok := bits.read(5)
	if !ok {
		return 0, 0, false
	}
	rate, ok := bits.frequency()
	if !ok {
		return 0, 0, false
	}
	configuration, ok := bits.read(4)
	// Program-config elements require separate channel-layout parsing; do not
	// infer them from the MP4 sample entry or advertise them as verified stereo.
	if !ok || configuration == 0 || configuration > 7 {
		return 0, 0, false
	}
	channels := int(configuration)
	if configuration == 7 {
		channels = 8
	}
	ps := object == 29
	explicitSBR := object == 5 || ps
	if explicitSBR {
		extensionRate, valid := bits.frequency()
		if !valid {
			return 0, 0, false
		}
		rate = max(rate, extensionRate)
		object, ok = bits.read(5)
	}
	if !ok || object != 2 { // LC, including HE-AAC's LC core.
		return 0, 0, false
	}
	if _, ok := bits.read(1); !ok { // frameLengthFlag
		return 0, 0, false
	}
	dependsOnCore, ok := bits.read(1)
	if !ok {
		return 0, 0, false
	}
	if dependsOnCore != 0 {
		if _, ok := bits.read(14); !ok {
			return 0, 0, false
		}
	}
	if _, ok := bits.read(1); !ok { // extensionFlag
		return 0, 0, false
	}
	if !explicitSBR {
		if sync, found := bits.read(11); found && sync == 0x2b7 {
			extensionObject, valid := bits.read(5)
			if !valid || extensionObject != 5 {
				return 0, 0, false
			}
			present, valid := bits.read(1)
			if !valid {
				return 0, 0, false
			}
			if present != 0 {
				extensionRate, valid := bits.frequency()
				if !valid {
					return 0, 0, false
				}
				rate = max(rate, extensionRate)
				if sync, found := bits.read(11); found && sync == 0x548 {
					present, valid := bits.read(1)
					if !valid {
						return 0, 0, false
					}
					ps = present != 0
				}
			}
		}
	}
	if ps {
		channels = max(channels, 2)
	}
	return rate, channels, true
}
