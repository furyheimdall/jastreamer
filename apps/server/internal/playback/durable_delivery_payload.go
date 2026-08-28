package playback

import (
	"bytes"
	"encoding/json"
	"strings"
)

const maxDurableCommandPayload = 64 << 10

func validateCommandPayload(payload json.RawMessage) error {
	if len(payload) > maxDurableCommandPayload || !json.Valid(payload) {
		return ErrInvalidRequest
	}
	sensitive, err := commandPayloadContainsSecret(payload)
	if err != nil {
		return err
	}
	if sensitive {
		return ErrSensitivePayload
	}
	return nil
}

func commandPayloadContainsSecret(payload json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return false, ErrInvalidRequest
	}
	switch trimmed[0] {
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return false, err
		}
		for key, value := range object {
			switch strings.ToLower(key) {
			case "token", "secret", "setup_secret", "private_key":
				return true, nil
			}
			sensitive, err := commandPayloadContainsSecret(value)
			if err != nil {
				return false, err
			}
			if sensitive {
				return true, nil
			}
		}
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return false, err
		}
		for _, item := range items {
			sensitive, err := commandPayloadContainsSecret(item)
			if err != nil {
				return false, err
			}
			if sensitive {
				return true, nil
			}
		}
	}
	return false, nil
}
