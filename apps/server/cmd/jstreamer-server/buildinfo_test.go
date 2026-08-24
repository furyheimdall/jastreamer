package main

import "testing"

func TestResolvedSourceRevision_uses_release_injection(t *testing.T) {
	// Given
	previous := sourceRevision
	sourceRevision = "fixture-revision"
	t.Cleanup(func() { sourceRevision = previous })

	// When
	resolved := resolvedSourceRevision()

	// Then
	if resolved != "fixture-revision" {
		t.Fatalf("source revision = %q", resolved)
	}
}
