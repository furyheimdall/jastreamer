package library

import (
	"encoding/binary"
	"errors"
	"os"
)

const maximumMP4Depth = 8

var mp4TextAtoms = map[string]string{
	"\xa9nam": "title",
	"\xa9ART": "artist",
	"\xa9art": "artist",
	"aART":    "albumartist",
	"\xa9alb": "album",
	"\xa9gen": "genre",
}

type mp4ParseBudget struct {
	atoms         int
	metadataBytes int64
	artwork       []byte
	artworkMIME   string
}

func readMP4Metadata(file *os.File, size int64) (storedMetadata, []byte, string, error) {
	result := storedMetadata{Genres: []string{}}
	if size < 8 {
		return result, nil, "", errors.New("truncated MP4 file")
	}
	budget := &mp4ParseBudget{}
	err := walkMP4Metadata(file, 0, size, 0, budget, &result)
	result.Genres = normalizedValues(result.Genres)
	return result, budget.artwork, budget.artworkMIME, err
}

func walkMP4Metadata(file *os.File, start, limit int64, depth int, budget *mp4ParseBudget, result *storedMetadata) error {
	if depth > maximumMP4Depth {
		return errors.New("MP4 atom nesting is too deep")
	}
	for offset := start; offset < limit; {
		if limit-offset < 8 {
			return errors.New("truncated MP4 atom header")
		}
		budget.atoms++
		if budget.atoms > maximumMetadataEntries {
			return errors.New("too many MP4 atoms")
		}
		atom, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return errors.New("invalid MP4 atom bounds")
		}
		payloadStart := atom.offset + atom.header
		payloadLimit := atom.offset + atom.size
		switch atom.kind {
		case "moov", "udta":
			if err := walkMP4Metadata(file, payloadStart, payloadLimit, depth+1, budget, result); err != nil {
				return err
			}
		case "meta":
			if payloadLimit-payloadStart < 4 {
				return errors.New("truncated MP4 meta atom")
			}
			if err := walkMP4Metadata(file, payloadStart+4, payloadLimit, depth+1, budget, result); err != nil {
				return err
			}
		case "ilst":
			if err := readMP4ItemList(file, payloadStart, payloadLimit, depth+1, budget, result); err != nil {
				return err
			}
		}
		offset = payloadLimit
	}
	return nil
}

func readMP4ItemList(file *os.File, start, limit int64, depth int, budget *mp4ParseBudget, result *storedMetadata) error {
	if depth > maximumMP4Depth {
		return errors.New("MP4 metadata nesting is too deep")
	}
	for offset := start; offset < limit; {
		if limit-offset < 8 {
			return errors.New("truncated MP4 metadata item")
		}
		budget.atoms++
		if budget.atoms > maximumMetadataEntries {
			return errors.New("too many MP4 metadata items")
		}
		item, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return errors.New("invalid MP4 metadata item bounds")
		}
		if err := readMP4Item(file, item, budget, result); err != nil {
			return err
		}
		offset = item.offset + item.size
	}
	return nil
}

func readMP4Item(file *os.File, item mp4Atom, budget *mp4ParseBudget, result *storedMetadata) error {
	limit := item.offset + item.size
	for offset := item.offset + item.header; offset < limit; {
		if limit-offset < 8 {
			return errors.New("truncated MP4 data atom")
		}
		budget.atoms++
		if budget.atoms > maximumMetadataEntries {
			return errors.New("too many MP4 data atoms")
		}
		dataAtom, ok := readMP4Atom(file, offset, limit)
		if !ok {
			return errors.New("invalid MP4 data atom bounds")
		}
		if dataAtom.kind == "data" {
			payloadOffset := dataAtom.offset + dataAtom.header
			payloadLength := dataAtom.size - dataAtom.header
			if payloadLength < 8 {
				return errors.New("truncated MP4 typed metadata")
			}
			var typedHeader [8]byte
			if _, err := file.ReadAt(typedHeader[:], payloadOffset); err != nil {
				return err
			}
			class := binary.BigEndian.Uint32(typedHeader[:4]) & 0x00ffffff
			valueOffset := payloadOffset + 8
			valueLength := payloadLength - 8
			if err := applyMP4Value(file, item.kind, class, valueOffset, valueLength, budget, result); err != nil {
				return err
			}
		}
		offset = dataAtom.offset + dataAtom.size
	}
	return nil
}

func applyMP4Value(file *os.File, name string, class uint32, offset, length int64, budget *mp4ParseBudget, result *storedMetadata) error {
	if field, ok := mp4TextAtoms[name]; ok {
		if class != 1 {
			return nil
		}
		if length < 0 || length > maximumTagStringBytes || budget.metadataBytes > maximumTagBytes-length {
			return errors.New("MP4 text metadata is too large")
		}
		budget.metadataBytes += length
		value := make([]byte, int(length))
		if _, err := file.ReadAt(value, offset); err != nil {
			return err
		}
		text := normalizeDisplay(string(value))
		switch field {
		case "title":
			result.Title = firstNonempty(result.Title, text)
		case "artist":
			result.Artist = firstNonempty(result.Artist, text)
		case "album":
			result.Album = firstNonempty(result.Album, text)
		case "albumartist":
			result.AlbumArtist = firstNonempty(result.AlbumArtist, text)
		case "genre":
			result.Genres = appendSplitValues(result.Genres, text)
		}
		return nil
	}
	if name == "trkn" || name == "disk" {
		if class != 0 || length < 6 || length > 32 {
			return nil
		}
		value := make([]byte, int(length))
		if _, err := file.ReadAt(value, offset); err != nil {
			return err
		}
		number := int(binary.BigEndian.Uint16(value[2:4]))
		if name == "trkn" {
			result.Track = number
		} else {
			result.Disc = number
		}
		return nil
	}
	if name != "covr" || class != 13 && class != 14 || len(budget.artwork) > 0 {
		return nil
	}
	if length <= 0 || length > maximumArtworkBytes || budget.metadataBytes > maximumTagBytes-length {
		return errors.New("MP4 artwork is too large")
	}
	budget.metadataBytes += length
	picture := make([]byte, int(length))
	if _, err := file.ReadAt(picture, offset); err != nil {
		return err
	}
	declaredMIME := "image/jpeg"
	if class == 14 {
		declaredMIME = "image/png"
	}
	mime := normalizeImageMIME(declaredMIME, picture)
	if mime != "" {
		budget.artwork = picture
		budget.artworkMIME = mime
	}
	return nil
}
