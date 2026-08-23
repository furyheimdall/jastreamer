package catalog

import (
	"slices"
	"sort"
	"strings"

	"github.com/dhowden/tag"
)

type extractedTags struct {
	values map[string][]string
}

func extractRawTags(parsed tag.Metadata) extractedTags {
	result := extractedTags{values: make(map[string][]string)}
	raw := parsed.Raw()
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		normalizedKey := normalizeTagKey(key)
		switch value := raw[key].(type) {
		case *tag.Comm:
			result.values[normalizeTagKey(value.Description)] = append(result.values[normalizeTagKey(value.Description)], value.Text)
		case *tag.UFID:
			if strings.Contains(strings.ToLower(value.Provider), "musicbrainz") {
				result.values["MUSICBRAINZTRACKID"] = append(result.values["MUSICBRAINZTRACKID"], string(value.Identifier))
			}
		default:
			result.values[normalizedKey] = append(result.values[normalizedKey], rawTagStrings(value)...)
		}
	}
	return result
}

func rawTagStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return splitTagString(typed)
	case []byte:
		return splitTagString(string(typed))
	case []string:
		var values []string
		for _, value := range typed {
			values = append(values, splitTagString(value)...)
		}
		return values
	case [][]byte:
		var values []string
		for _, value := range typed {
			values = append(values, splitTagString(string(value))...)
		}
		return values
	case []any:
		var values []string
		for _, value := range typed {
			values = append(values, rawTagStrings(value)...)
		}
		return values
	default:
		return nil
	}
}

func splitTagString(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == '\x00' || character == ';' || character == ','
	})
}

func normalizedTagValues(values []string) []string {
	unique := make(map[string]string, len(values))
	for _, value := range values {
		display := normalizeDisplay(value)
		key := normalize(display)
		if key == "" {
			continue
		}
		if previous, exists := unique[key]; !exists || display < previous {
			unique[key] = display
		}
	}
	result := make([]string, 0, len(unique))
	for _, value := range unique {
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right string) int {
		return strings.Compare(normalize(left), normalize(right))
	})
	return result
}

func normalizeTagKey(value string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "", ":", "").Replace(strings.ToUpper(value))
}

func (tags extractedTags) first(keys ...string) string {
	for _, key := range keys {
		if values := tags.values[key]; len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func (tags extractedTags) all(keys ...string) []string {
	var values []string
	for _, key := range keys {
		values = append(values, tags.values[key]...)
	}
	return normalizedTagValues(values)
}
