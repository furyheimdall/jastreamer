//go:build !windows

package security

func canonicalFilePathsEqual(configured, resolved string) bool {
	return configured == resolved
}
