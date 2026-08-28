package media

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type validatedFile struct {
	root     string
	relative string
	rootInfo os.FileInfo
	fileInfo os.FileInfo
}

func openValidated(path validatedFile, claims Claims) (*os.File, error) {
	root, err := os.OpenRoot(path.root)
	if err != nil {
		return nil, ErrTrackUnavailable
	}
	defer root.Close()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return nil, ErrTrackUnavailable
	}
	if !os.SameFile(path.rootInfo, rootInfo) {
		return nil, ErrUnsafePath
	}
	file, err := openRootReadOnly(root, path.relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrTrackUnavailable
		}
		return nil, ErrUnsafePath
	}
	info, statErr := file.Stat()
	if statErr != nil || !os.SameFile(path.fileInfo, info) || !info.Mode().IsRegular() || info.Size() != claims.FileSize || info.ModTime().UnixNano() != claims.ModifiedNS {
		return nil, errors.Join(ErrStaleFile, file.Close())
	}
	return file, nil
}

type fileIdentity struct {
	size       int64
	modifiedNS int64
}

func safeRegularPath(root, relative string, identity fileIdentity) (validatedFile, error) {
	if filepath.IsAbs(relative) || relative == "" || filepath.Clean(relative) != relative {
		return validatedFile{}, ErrUnsafePath
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return validatedFile{}, ErrUnsafePath
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return validatedFile{}, ErrTrackUnavailable
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return validatedFile{}, ErrUnsafePath
	}
	if !rootInfo.IsDir() {
		return validatedFile{}, ErrTrackUnavailable
	}
	current := absoluteRoot
	for component := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return validatedFile{}, ErrUnsafePath
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return validatedFile{}, ErrTrackUnavailable
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return validatedFile{}, ErrUnsafePath
		}
	}
	info, err := os.Lstat(current)
	if err != nil {
		return validatedFile{}, ErrTrackUnavailable
	}
	if !info.Mode().IsRegular() || info.Size() != identity.size || info.ModTime().UnixNano() != identity.modifiedNS {
		return validatedFile{}, ErrStaleFile
	}
	return validatedFile{root: absoluteRoot, relative: relative, rootInfo: rootInfo, fileInfo: info}, nil
}
