package catalog

import (
	"slices"
	"testing"
)

func TestMetadataFromTagNormalizesRawCurationValueForms(t *testing.T) {
	metadata := metadataFromTag(fractionalMetadata{raw: map[string]any{
		"GENRE":      []any{" Dream  Pop ", []byte("Ambient;Café Pop"), []string{"ambient"}},
		"STYLE":      []byte("Ethereal\x00Shoegaze"),
		"MOOD":       []string{" Calm ", "Reflective,Calm"},
		"LOCAL_TAGS": [][]byte{[]byte("Night"), []byte("Focus;night")},
	}})
	if !slices.Equal(metadata.Genres, []string{"Ambient", "Café Pop", "Dream Pop"}) {
		t.Fatalf("genres = %v", metadata.Genres)
	}
	if !slices.Equal(metadata.Styles, []string{"Ethereal", "Shoegaze"}) {
		t.Fatalf("styles = %v", metadata.Styles)
	}
	if !slices.Equal(metadata.Moods, []string{"Calm", "Reflective"}) {
		t.Fatalf("moods = %v", metadata.Moods)
	}
	if !slices.Equal(metadata.LocalTags, []string{"Focus", "Night"}) {
		t.Fatalf("local tags = %v", metadata.LocalTags)
	}
}
