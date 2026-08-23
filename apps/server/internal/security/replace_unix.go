//go:build !windows

package security

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
