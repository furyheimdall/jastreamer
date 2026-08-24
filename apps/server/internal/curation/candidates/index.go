package candidates

import (
	"slices"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/analysis"
	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func IndexSnapshot(snapshot catalog.Snapshot) Index {
	tracks := make([]Track, 0, len(snapshot.Tracks))
	for _, value := range snapshot.Tracks {
		track := cloneTrack(Track{
			Catalog: value,
			Signals: Signals{
				Genres: value.Metadata.Genres, Styles: value.Metadata.Styles,
				Moods: value.Metadata.Moods, LocalTags: value.Metadata.LocalTags,
			},
		})
		if value.AnalysisStatus == catalog.AnalysisComplete &&
			value.AnalysisProvenance == analysis.CurrentProvenance() {
			track.Signals.AcousticVector = slices.Clone([]byte(value.AnalysisVector))
		}
		tracks = append(tracks, track)
	}
	return NewIndex(snapshot.Revision, tracks)
}

func NewIndex(revision uint64, values []Track) Index {
	tracks := make([]Track, len(values))
	for index := range values {
		tracks[index] = cloneTrack(values[index])
	}
	slices.SortFunc(tracks, func(left, right Track) int {
		return strings.Compare(string(left.Catalog.TrackID), string(right.Catalog.TrackID))
	})
	return Index{Revision: revision, Tracks: tracks}
}

func cloneTrack(value Track) Track {
	value.Catalog.Metadata.Genres = slices.Clone(value.Catalog.Metadata.Genres)
	value.Catalog.Metadata.Styles = slices.Clone(value.Catalog.Metadata.Styles)
	value.Catalog.Metadata.Moods = slices.Clone(value.Catalog.Metadata.Moods)
	value.Catalog.Metadata.LocalTags = slices.Clone(value.Catalog.Metadata.LocalTags)
	value.Signals.Genres = slices.Clone(value.Signals.Genres)
	value.Signals.Styles = slices.Clone(value.Signals.Styles)
	value.Signals.Moods = slices.Clone(value.Signals.Moods)
	value.Signals.LocalTags = slices.Clone(value.Signals.LocalTags)
	value.Signals.AcousticVector = slices.Clone(value.Signals.AcousticVector)
	return value
}
