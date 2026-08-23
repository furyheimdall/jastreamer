//go:build !windows

package playback

import "os"

func atomicReplace(source, target string) error {
	return os.Rename(source, target)
}
