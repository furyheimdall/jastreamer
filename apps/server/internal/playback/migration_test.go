package playback_test

import (
	"os"
	"testing"
)

func Test_Migration_playback_schema_exists(t *testing.T) {
	// Given
	const migration = "../../migrations/002_playback.sql"

	// When
	_, err := os.Stat(migration)
	// Then
	if err != nil {
		t.Fatalf("playback migration must exist: %v", err)
	}
}
