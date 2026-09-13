package httpapi

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

const (
	filesystemPageSize = 100
	filesystemReadSize = 100
)

type filesystemRoot struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type filesystemEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type filesystemPage struct {
	Path       string            `json:"path"`
	Parent     *string           `json:"parent"`
	Roots      []filesystemRoot  `json:"roots"`
	Entries    []filesystemEntry `json:"entries"`
	NextOffset *int              `json:"next_offset"`
}

func (service *server) filesystem(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "directory"
	}
	if kind != "directory" && kind != "file" {
		writeError(w, fault.New(http.StatusBadRequest, "INVALID_QUERY", "조회할 파일 종류가 올바르지 않습니다."))
		return
	}
	offset, err := integer(r, "offset", 0, 0, int(^uint(0)>>1)-filesystemPageSize)
	if err != nil {
		writeError(w, err)
		return
	}
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		roots, rootErr := filesystemRoots()
		if rootErr != nil {
			writeError(w, rootErr)
			return
		}
		reply(w, http.StatusOK, filesystemPage{
			Path:    "",
			Parent:  nil,
			Roots:   roots,
			Entries: make([]filesystemEntry, 0),
		})
		return
	}
	path, err := cleanFilesystemPath(rawPath)
	if err != nil {
		writeError(w, err)
		return
	}
	roots, err := filesystemRoots()
	if err != nil {
		writeError(w, err)
		return
	}
	entries, nextOffset, err := listFilesystemDirectory(r.Context(), path, kind, offset)
	if err != nil {
		writeError(w, filesystemFault(err))
		return
	}
	var parent *string
	if value := filepath.Dir(path); value != path {
		parent = &value
	}
	reply(w, http.StatusOK, filesystemPage{
		Path:       path,
		Parent:     parent,
		Roots:      roots,
		Entries:    entries,
		NextOffset: nextOffset,
	})
}

func cleanFilesystemPath(path string) (string, error) {
	if strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) || !validPlatformFilesystemPath(path) {
		return "", fault.New(http.StatusBadRequest, "INVALID_PATH", "서버의 올바른 절대 경로가 필요합니다.")
	}
	cleaned := filepath.Clean(path)
	if !validPlatformFilesystemPath(cleaned) {
		return "", fault.New(http.StatusBadRequest, "INVALID_PATH", "서버의 올바른 절대 경로가 필요합니다.")
	}
	return cleaned, nil
}

func listFilesystemDirectory(ctx context.Context, path, kind string, offset int) ([]filesystemEntry, *int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fault.New(http.StatusBadRequest, "PATH_NOT_DIRECTORY", "요청한 경로는 디렉터리가 아닙니다.")
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer directory.Close()

	entries := make([]filesystemEntry, 0, filesystemPageSize+1)
	eligible := 0
	for len(entries) <= filesystemPageSize {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		batch, readErr := directory.ReadDir(filesystemReadSize)
		for _, entry := range batch {
			candidate, ok := classifyFilesystemEntry(path, entry, kind)
			if !ok {
				continue
			}
			if eligible < offset {
				eligible++
				continue
			}
			eligible++
			entries = append(entries, candidate)
			if len(entries) > filesystemPageSize {
				break
			}
		}
		if len(entries) > filesystemPageSize {
			break
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, nil, readErr
		}
	}
	if len(entries) <= filesystemPageSize {
		return entries, nil, nil
	}
	next := offset + filesystemPageSize
	return entries[:filesystemPageSize], &next, nil
}

func classifyFilesystemEntry(parent string, entry fs.DirEntry, kind string) (filesystemEntry, bool) {
	info, err := entry.Info()
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || platformFilesystemLink(info) {
		return filesystemEntry{}, false
	}
	entryKind := ""
	switch {
	case info.IsDir():
		entryKind = "directory"
	case kind == "file" && info.Mode().IsRegular():
		entryKind = "file"
	default:
		return filesystemEntry{}, false
	}
	return filesystemEntry{Name: entry.Name(), Path: filepath.Join(parent, entry.Name()), Kind: entryKind}, true
}

func filesystemFault(err error) error {
	var known *fault.Error
	if errors.As(err, &known) {
		return err
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fault.New(http.StatusNotFound, "PATH_NOT_FOUND", "요청한 경로를 찾을 수 없습니다.")
	case errors.Is(err, syscall.ENOTDIR):
		return fault.New(http.StatusBadRequest, "PATH_NOT_DIRECTORY", "요청한 경로는 디렉터리가 아닙니다.")
	case errors.Is(err, fs.ErrPermission):
		return fault.New(http.StatusForbidden, "PATH_ACCESS_DENIED", "요청한 경로에 접근할 수 없습니다.")
	default:
		return err
	}
}
