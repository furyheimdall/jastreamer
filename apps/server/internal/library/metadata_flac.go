package library

import (
	"encoding/binary"
	"errors"
	"os"
)

const (
	maximumFLACBlocks      = 1024
	maximumTagStringBytes  = 1 << 20
	maximumTagCommentCount = 4096
)

func readFLACMetadata(file *os.File, size int64) (storedMetadata, []byte, string, error) {
	result := storedMetadata{Genres: []string{}}
	if size < 8 {
		return result, nil, "", errors.New("truncated FLAC metadata")
	}
	var signature [4]byte
	if _, err := file.ReadAt(signature[:], 0); err != nil || string(signature[:]) != "fLaC" {
		return result, nil, "", errors.New("invalid FLAC signature")
	}

	offset := int64(4)
	metadataBytes := int64(0)
	var artwork []byte
	var artworkMIME string
	for blockCount := 0; blockCount < maximumFLACBlocks; blockCount++ {
		if offset > size-4 {
			return result, artwork, artworkMIME, errors.New("truncated FLAC block header")
		}
		var header [4]byte
		if _, err := file.ReadAt(header[:], offset); err != nil {
			return result, artwork, artworkMIME, err
		}
		last := header[0]&0x80 != 0
		kind := header[0] & 0x7f
		length := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		payloadOffset := offset + 4
		if length > size-payloadOffset || metadataBytes > maximumTagBytes-4-length {
			return result, artwork, artworkMIME, errors.New("invalid FLAC metadata bounds")
		}
		metadataBytes += 4 + length

		switch kind {
		case 4:
			if length > 0 {
				payload := make([]byte, int(length))
				if _, err := file.ReadAt(payload, payloadOffset); err != nil {
					return result, artwork, artworkMIME, err
				}
				comments, err := parseVorbisComments(payload)
				if err != nil {
					return result, artwork, artworkMIME, err
				}
				mergeMetadata(&result, metadataFromVorbisComments(comments))
			}
		case 6:
			if len(artwork) == 0 {
				picture, mime, err := readFLACPicture(file, payloadOffset, length)
				if err != nil {
					return result, artwork, artworkMIME, err
				}
				artwork = picture
				artworkMIME = mime
			}
		}

		offset = payloadOffset + length
		if last {
			return result, artwork, artworkMIME, nil
		}
	}
	return result, artwork, artworkMIME, errors.New("too many FLAC metadata blocks")
}

func readFLACPicture(file *os.File, offset, length int64) ([]byte, string, error) {
	if length < 32 {
		return nil, "", errors.New("truncated FLAC picture")
	}
	end := offset + length
	var fixed [8]byte
	if _, err := file.ReadAt(fixed[:], offset); err != nil {
		return nil, "", err
	}
	mimeLength := int64(binary.BigEndian.Uint32(fixed[4:8]))
	offset += 8
	if mimeLength > 255 || mimeLength > end-offset-4 {
		return nil, "", errors.New("invalid FLAC picture MIME length")
	}
	mimeBytes := make([]byte, int(mimeLength))
	if _, err := file.ReadAt(mimeBytes, offset); err != nil {
		return nil, "", err
	}
	offset += mimeLength

	var lengthBytes [4]byte
	if _, err := file.ReadAt(lengthBytes[:], offset); err != nil {
		return nil, "", err
	}
	descriptionLength := int64(binary.BigEndian.Uint32(lengthBytes[:]))
	offset += 4
	if descriptionLength > maximumTagStringBytes || descriptionLength > end-offset-20 {
		return nil, "", errors.New("invalid FLAC picture description length")
	}
	offset += descriptionLength + 16
	if _, err := file.ReadAt(lengthBytes[:], offset); err != nil {
		return nil, "", err
	}
	pictureLength := int64(binary.BigEndian.Uint32(lengthBytes[:]))
	offset += 4
	if pictureLength <= 0 || pictureLength > maximumArtworkBytes || pictureLength > end-offset {
		return nil, "", errors.New("invalid FLAC picture data length")
	}

	picture := make([]byte, int(pictureLength))
	if _, err := file.ReadAt(picture, offset); err != nil {
		return nil, "", err
	}
	mime := normalizeImageMIME(string(mimeBytes), picture)
	if mime == "" {
		return nil, "", nil
	}
	return picture, mime, nil
}
