package analysis

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"

	"github.com/eaburns/flac"
	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/pion/opus"
)

func decodeMP3(ctx context.Context, r io.Reader) (int, int, []int16, error) {
	d, err := mp3.NewDecoder(r)
	if err != nil {
		return 0, 0, nil, ErrCorrupt
	}
	rate, ch := d.SampleRate(), 2
	raw := make([]byte, sampleLimit(rate, ch)*2)
	n, err := readContext(ctx, d, raw)
	if err != nil && err != io.EOF {
		return 0, 0, nil, err
	}
	out := make([]int16, n/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return rate, ch, out, nil
}
func decodeVorbis(ctx context.Context, r io.Reader) (int, int, []int16, error) {
	d, err := oggvorbis.NewReader(r)
	if err != nil {
		return 0, 0, nil, ErrCorrupt
	}
	rate, ch := d.SampleRate(), d.Channels()
	buf := make([]float32, sampleLimit(rate, ch))
	n, err := readFloatContext(ctx, d, buf)
	if err != nil && err != io.EOF {
		return 0, 0, nil, err
	}
	out := make([]int16, n)
	for i, v := range buf[:n] {
		out[i] = int16(max(-1, min(1, v)) * 32767)
	}
	return rate, ch, out, nil
}
func decodeFLAC(ctx context.Context, r io.Reader) (int, int, []int16, error) {
	d, err := flac.NewDecoder(r)
	if err != nil {
		return 0, 0, nil, ErrCorrupt
	}
	rate, ch := d.SampleRate, d.NChannels
	limit := sampleLimit(rate, ch)
	out := make([]int16, 0, limit)
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return 0, 0, nil, err
		}
		raw, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, nil, ErrCorrupt
		}
		width := d.BitsPerSample / 8
		for i := 0; i+width <= len(raw) && len(out) < limit; i += width {
			switch width {
			case 1:
				out = append(out, int16(int8(raw[i]))<<8)
			case 2:
				out = append(out, int16(binary.LittleEndian.Uint16(raw[i:])))
			case 3:
				v := int32(raw[i]) | int32(raw[i+1])<<8 | int32(raw[i+2])<<16
				if v&0x800000 != 0 {
					v |= ^int32(0xffffff)
				}
				out = append(out, int16(v>>8))
			}
		}
	}
	return rate, ch, out, nil
}
func decodeOpus(ctx context.Context, r io.Reader) (int, int, []int16, error) {
	br := bufio.NewReader(r)
	rate, ch := 48000, 2
	dec, err := opus.NewDecoderWithOutput(rate, ch)
	if err != nil {
		return 0, 0, nil, err
	}
	limit := sampleLimit(rate, ch)
	out := make([]int16, 0, limit)
	packet := []byte{}
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return 0, 0, nil, err
		}
		h := make([]byte, 27)
		if _, err := io.ReadFull(br, h); err == io.EOF {
			break
		} else if err != nil || string(h[:4]) != "OggS" {
			return 0, 0, nil, ErrCorrupt
		}
		laces := make([]byte, int(h[26]))
		if _, err = io.ReadFull(br, laces); err != nil {
			return 0, 0, nil, ErrCorrupt
		}
		for _, n := range laces {
			part := make([]byte, int(n))
			if _, err = io.ReadFull(br, part); err != nil {
				return 0, 0, nil, ErrCorrupt
			}
			packet = append(packet, part...)
			if n < 255 {
				if len(packet) > 8 && string(packet[:8]) == "OpusHead" {
					ch = int(packet[9])
					dec, err = opus.NewDecoderWithOutput(rate, ch)
				} else if len(packet) > 8 && string(packet[:8]) != "OpusTags" {
					pcm := make([]int16, 5760*ch)
					var count int
					count, err = dec.DecodeToInt16(packet, pcm)
					if err == nil {
						out = append(out, pcm[:min(len(pcm), count*ch)]...)
					}
				}
				packet = nil
				if err != nil {
					return 0, 0, nil, ErrCorrupt
				}
			}
		}
	}
	return rate, ch, out, nil
}
func readContext(ctx context.Context, r io.Reader, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

type floatReader interface{ Read([]float32) (int, error) }

func readFloatContext(ctx context.Context, r floatReader, b []float32) (int, error) {
	n := 0
	for n < len(b) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
