//go:build !unix

package library

import "os"

func openRootReadOnly(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}

func openCacheReadOnly(path string) (*os.File, error) {
	return os.Open(path)
}
