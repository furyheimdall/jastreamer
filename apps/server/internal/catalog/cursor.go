package catalog

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

type browseCursor struct {
	Version   int    `json:"v"`
	Revision  uint64 `json:"r"`
	QueryHash string `json:"q"`
	Offset    int    `json:"o"`
}

func encodeBrowseCursor(revision uint64, query string, offset int) string {
	payload, err := json.Marshal(browseCursor{
		Version: 1, Revision: revision, QueryHash: browseQueryHash(query), Offset: offset,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeBrowseCursor(value string, revision uint64, query string) (int, error) {
	if value == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	var cursor browseCursor
	decoderErr := json.Unmarshal(payload, &cursor)
	if decoderErr != nil || cursor.Version != 1 || cursor.Offset < 0 {
		return 0, ErrInvalidCursor
	}
	if cursor.Revision != revision {
		return 0, ErrCatalogRevisionChanged
	}
	if cursor.QueryHash != browseQueryHash(query) {
		return 0, ErrInvalidCursor
	}
	return cursor.Offset, nil
}

func browseQueryHash(query string) string {
	digest := sha256.Sum256([]byte(query))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
