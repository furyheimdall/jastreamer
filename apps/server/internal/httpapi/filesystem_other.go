//go:build !windows

package httpapi

import "io/fs"

func filesystemRoots() ([]filesystemRoot, error) {
	return []filesystemRoot{{Name: "/", Path: "/"}}, nil
}

func validPlatformFilesystemPath(string) bool { return true }

func platformFilesystemLink(fs.FileInfo) bool { return false }
