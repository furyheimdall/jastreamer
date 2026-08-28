package catalog

import (
	"errors"
	"fmt"
	"testing"
)

func TestBrowserPaginates100000AvailableTracks_withoutDuplicatesOrOmissions(t *testing.T) {
	// Given
	snapshot := EmptySnapshot()
	snapshot.Revision = 41
	for index := range 100_000 {
		id := TrackID(fmt.Sprintf("track-%06d", index))
		snapshot.Tracks[id] = Track{TrackID: id, Available: true, Metadata: Metadata{Title: fmt.Sprintf("Title %06d", index)}}
	}
	snapshot.Tracks["unavailable"] = Track{TrackID: "unavailable", Available: false}
	browser := NewBrowser(snapshot)

	// When
	seen := make(map[TrackID]bool, 100_000)
	cursor := ""
	var first, middle, last TrackID
	for pageNumber := 0; ; pageNumber++ {
		page, err := browser.Browse(BrowseRequest{Limit: 500, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if pageNumber == 0 {
			first = page.Items[0].TrackID
		}
		if pageNumber == 100 {
			middle = page.Items[0].TrackID
		}
		for _, item := range page.Items {
			if seen[item.TrackID] {
				t.Fatalf("duplicate track %q", item.TrackID)
			}
			seen[item.TrackID] = true
			last = item.TrackID
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}

	// Then
	if len(seen) != 100_000 || first != "track-000000" || middle != "track-050000" || last != "track-099999" {
		t.Fatalf("pagination count/anchors = %d/%s/%s/%s", len(seen), first, middle, last)
	}
}

func TestBrowserSearchesTrimmedNFCCaseFoldedMetadata_whenQueryMatchesAnyField(t *testing.T) {
	// Given
	snapshot := EmptySnapshot()
	snapshot.Revision = 7
	snapshot.Tracks["title"] = Track{TrackID: "title", Available: true, Metadata: Metadata{Title: "Cafe\u0301 Society"}}
	snapshot.Tracks["artist"] = Track{TrackID: "artist", Available: true, Metadata: Metadata{Artist: "STRASSE Quartet"}}
	snapshot.Tracks["album"] = Track{TrackID: "album", Available: true, Metadata: Metadata{Album: "NIGHT MUSIC"}}
	snapshot.Tracks["album-artist"] = Track{TrackID: "album-artist", Available: true, Metadata: Metadata{AlbumArtist: "The Collective"}}
	browser := NewBrowser(snapshot)

	// When / Then
	for query, want := range map[string]TrackID{"  CAFÉ  ": "title", "straße": "artist", "night": "album", "collective": "album-artist"} {
		page, err := browser.Browse(BrowseRequest{Query: query})
		if err != nil || len(page.Items) != 1 || page.Items[0].TrackID != want {
			t.Fatalf("query %q = %+v, %v; want %s", query, page, err, want)
		}
	}
}

func TestBrowserRejectsCursor_whenRevisionOrQueryChanges(t *testing.T) {
	// Given
	snapshot := EmptySnapshot()
	snapshot.Revision = 9
	for _, id := range []TrackID{"a", "b"} {
		snapshot.Tracks[id] = Track{TrackID: id, Available: true, Metadata: Metadata{Title: "needle"}}
	}
	first, err := NewBrowser(snapshot).Browse(BrowseRequest{Query: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}

	// When
	changed := snapshot
	changed.Revision++
	_, staleErr := NewBrowser(changed).Browse(BrowseRequest{Query: "needle", Cursor: first.NextCursor})
	_, queryErr := NewBrowser(snapshot).Browse(BrowseRequest{Query: "other", Cursor: first.NextCursor})

	// Then
	if !errors.Is(staleErr, ErrCatalogRevisionChanged) || !errors.Is(queryErr, ErrInvalidCursor) {
		t.Fatalf("cursor errors = %v / %v", staleErr, queryErr)
	}
}

func TestBrowserAppliesLimitsAndDoesNotExposePaths(t *testing.T) {
	// Given
	snapshot := EmptySnapshot()
	for index := range 600 {
		id := TrackID(fmt.Sprintf("%03d", index))
		snapshot.Tracks[id] = Track{TrackID: id, Available: true, RelativePath: "/secret/music.flac"}
	}
	browser := NewBrowser(snapshot)

	// When
	page, defaultErr := browser.Browse(BrowseRequest{})
	_, maxErr := browser.Browse(BrowseRequest{Limit: 501})
	_, queryErr := browser.Browse(BrowseRequest{Query: string(make([]byte, 201))})

	// Then
	if defaultErr != nil || len(page.Items) != 100 || !errors.Is(maxErr, ErrInvalidLimit) || !errors.Is(queryErr, ErrInvalidQuery) {
		t.Fatalf("limits = %d, %v/%v/%v", len(page.Items), defaultErr, maxErr, queryErr)
	}
}
