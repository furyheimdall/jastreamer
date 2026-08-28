package media

import "testing"

func TestParseRange_rejects_range_for_empty_resource(t *testing.T) {
	// Given
	const size = int64(0)

	// When
	_, err := parseRange("bytes=-1", size)

	// Then
	if err == nil {
		t.Fatal("empty resource range was accepted")
	}
}
