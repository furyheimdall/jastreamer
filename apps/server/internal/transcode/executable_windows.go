//go:build windows

package transcode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

type executableBinding struct {
	path        string
	file        *os.File
	parents     []windows.Handle
	fingerprint string
	close       sync.Once
	closeErr    error
}

func bindExecutable(path string) (binding *executableBinding, err error) {
	path = filepath.Clean(path)
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return nil, bindingError(executableNotExecutable, nil)
	}
	parents, err := lockWindowsParents(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if binding == nil {
			err = errors.Join(err, closeWindowsHandles(parents))
		}
	}()
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, bindingError(executableUnsafeLocation, err)
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ|windows.GENERIC_EXECUTE,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN,
		0,
	)
	if err != nil {
		return nil, classifyWindowsOpenError(err)
	}
	file := os.NewFile(uintptr(handle), path)
	defer func() {
		if binding == nil {
			err = errors.Join(err, file.Close())
		}
	}()
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return nil, bindingError(executableOpenFailed, err)
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, bindingError(executableUnsafeLocation, nil)
	}
	if information.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_DEVICE) != 0 {
		return nil, bindingError(executableNotExecutable, nil)
	}
	fingerprint, err := fingerprintFile(file)
	if err != nil {
		return nil, bindingError(executableHashFailed, err)
	}
	return &executableBinding{path: path, file: file, parents: parents, fingerprint: fingerprint}, nil
}

func lockWindowsParents(path string) ([]windows.Handle, error) {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, bindingError(executableUnsafeLocation, nil)
	}
	root := volume + `\`
	rootPointer, err := windows.UTF16PtrFromString(root)
	if err != nil || windows.GetDriveType(rootPointer) != windows.DRIVE_FIXED {
		return nil, bindingError(executableUnsafeLocation, err)
	}
	relative := strings.TrimLeft(strings.TrimPrefix(path, volume), `\/`)
	components := strings.FieldsFunc(relative, func(value rune) bool { return value == '\\' || value == '/' })
	if len(components) == 0 {
		return nil, bindingError(executableNotExecutable, nil)
	}
	parents := make([]windows.Handle, 0, len(components)-1)
	current := root
	for _, component := range components[:len(components)-1] {
		current = filepath.Join(current, component)
		handle, openErr := lockWindowsDirectory(current)
		if openErr != nil {
			return nil, errors.Join(openErr, closeWindowsHandles(parents))
		}
		parents = append(parents, handle)
	}
	return parents, nil
}

func lockWindowsDirectory(path string) (windows.Handle, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, bindingError(executableUnsafeLocation, err)
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, classifyWindowsOpenError(err)
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return windows.InvalidHandle, errors.Join(bindingError(executableOpenFailed, err), windows.CloseHandle(handle))
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return windows.InvalidHandle, errors.Join(bindingError(executableUnsafeLocation, nil), windows.CloseHandle(handle))
	}
	return handle, nil
}

func classifyWindowsOpenError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return bindingError(executableNotFound, err)
	case errors.Is(err, windows.ERROR_CANT_ACCESS_FILE):
		return bindingError(executableUnsafeLocation, err)
	default:
		return bindingError(executableOpenFailed, err)
	}
}

func closeWindowsHandles(handles []windows.Handle) error {
	var err error
	for index := len(handles) - 1; index >= 0; index-- {
		err = errors.Join(err, windows.CloseHandle(handles[index]))
	}
	return err
}

func (executable *executableBinding) command(arguments []string) *exec.Cmd {
	return exec.Command(executable.path, arguments...)
}

func (executable *executableBinding) Close() error {
	executable.close.Do(func() {
		executable.closeErr = errors.Join(executable.file.Close(), closeWindowsHandles(executable.parents))
	})
	return executable.closeErr
}
