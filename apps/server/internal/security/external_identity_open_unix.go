//go:build !windows

package security

import "os"

func openExternalIdentityFile(path string) (*os.File, error) {
	return os.Open(path)
}
