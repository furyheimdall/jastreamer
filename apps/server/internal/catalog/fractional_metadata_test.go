package catalog

import (
	"testing"

	"github.com/dhowden/tag"
)

func TestMetadataFromTagParsesFractionalTrackAndUnknownDisc(t *testing.T) {
	metadata := metadataFromTag(fractionalMetadata{
		raw: map[string]any{"TRACKNUMBER": "3/12", "DISCNUMBER": "-1/2"},
	})
	if metadata.Track != 3 || metadata.Disc != 0 {
		t.Fatalf("track/disc = %d/%d, want 3/unknown", metadata.Track, metadata.Disc)
	}
}

type fractionalMetadata struct {
	raw map[string]any
}

func (fractionalMetadata) Format() tag.Format           { return tag.VORBIS }
func (fractionalMetadata) FileType() tag.FileType       { return tag.OGG }
func (fractionalMetadata) Title() string                { return "" }
func (fractionalMetadata) Album() string                { return "" }
func (fractionalMetadata) Artist() string               { return "" }
func (fractionalMetadata) AlbumArtist() string          { return "" }
func (fractionalMetadata) Composer() string             { return "" }
func (fractionalMetadata) Year() int                    { return 0 }
func (fractionalMetadata) Genre() string                { return "" }
func (fractionalMetadata) Track() (int, int)            { return 0, 0 }
func (fractionalMetadata) Disc() (int, int)             { return 0, 0 }
func (fractionalMetadata) Picture() *tag.Picture        { return nil }
func (fractionalMetadata) Lyrics() string               { return "" }
func (fractionalMetadata) Comment() string              { return "" }
func (metadata fractionalMetadata) Raw() map[string]any { return metadata.raw }
