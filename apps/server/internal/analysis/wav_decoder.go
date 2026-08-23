package analysis

import (
	"context"
	"encoding/binary"
	"io"
)

func decodeWAVBytes(d []byte) (rate, source int, out []int16, err error) {
	if len(d) < 12 || string(d[:4]) != "RIFF" || string(d[8:12]) != "WAVE" {
		return 0, 0, nil, ErrCorrupt
	}
	channels, bits, format, dataAt, dataLen := 0, 0, 0, 0, 0
	for p := 12; p+8 <= len(d); {
		n := int(binary.LittleEndian.Uint32(d[p+4:]))
		if n < 0 || p+8+n+n%2 > len(d) {
			return 0, 0, nil, ErrCorrupt
		}
		switch string(d[p : p+4]) {
		case "fmt ":
			if n < 16 {
				return 0, 0, nil, ErrCorrupt
			}
			format = int(binary.LittleEndian.Uint16(d[p+8:]))
			channels = int(binary.LittleEndian.Uint16(d[p+10:]))
			rate = int(binary.LittleEndian.Uint32(d[p+12:]))
			bits = int(binary.LittleEndian.Uint16(d[p+22:]))
		case "data":
			dataAt, dataLen = p+8, n
		}
		p += 8 + n + n%2
	}
	if format != 1 || channels < 1 || channels > 8 || bits != 16 || rate < 4000 || rate > 192000 {
		return 0, 0, nil, ErrUnsupported
	}
	frameBytes := channels * 2
	if dataAt == 0 || dataLen < frameBytes || dataLen%frameBytes != 0 {
		return 0, 0, nil, ErrCorrupt
	}
	source = dataLen / frameBytes
	window := windowSeconds * rate
	starts := []int{0}
	if source > window {
		starts = []int{0, (source - window) / 2, source - window}
	}
	capSamples := MaxAnalysisSamples * rate / 44100
	for _, start := range starts {
		end := min(source, start+window)
		for i := start; i < end && len(out) < capSamples; i++ {
			sum := 0
			for c := 0; c < channels; c++ {
				sum += int(int16(binary.LittleEndian.Uint16(d[dataAt+(i*channels+c)*2:])))
			}
			out = append(out, int16(sum/channels))
		}
	}
	return rate, source, out, nil
}
func decodeWAVFile(ctx context.Context, r io.ReadSeeker) (int, int, []int16, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return 0, 0, nil, ErrCorrupt
	}
	rate, channels, bits, format := 0, 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, nil, err
		}
		h := make([]byte, 8)
		_, err := io.ReadFull(r, h)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return 0, 0, nil, ErrCorrupt
		}
		n := int64(binary.LittleEndian.Uint32(h[4:]))
		switch string(h[:4]) {
		case "fmt ":
			if n < 16 {
				return 0, 0, nil, ErrCorrupt
			}
			b := make([]byte, 16)
			if _, err = io.ReadFull(r, b); err != nil {
				return 0, 0, nil, ErrCorrupt
			}
			format = int(binary.LittleEndian.Uint16(b))
			channels = int(binary.LittleEndian.Uint16(b[2:]))
			rate = int(binary.LittleEndian.Uint32(b[4:]))
			bits = int(binary.LittleEndian.Uint16(b[14:]))
			_, err = r.Seek(n-16+n%2, io.SeekCurrent)
		case "data":
			if format != 1 || bits != 16 || channels < 1 || channels > 8 || rate < 4000 || rate > 192000 || n%int64(channels*2) != 0 {
				return 0, 0, nil, ErrUnsupported
			}
			take := min(n, int64(sampleLimit(rate, channels)*2))
			raw := make([]byte, take)
			_, err = io.ReadFull(r, raw)
			if err == nil {
				samples := make([]int16, len(raw)/2)
				for i := range samples {
					samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
				}
				return rate, channels, samples, nil
			}
		default:
			_, err = r.Seek(n+n%2, io.SeekCurrent)
		}
		if err != nil {
			return 0, 0, nil, ErrCorrupt
		}
	}
	return 0, 0, nil, ErrCorrupt
}
func makeWAV(rate, channels int, s []int16) []byte {
	b := make([]byte, 44+len(s)*2)
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
	binary.LittleEndian.PutUint32(b[40:], uint32(len(s)*2))
	for i, x := range s {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(x))
	}
	return b
}
