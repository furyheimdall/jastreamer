//go:build linux

package transcode

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const executableChildDescriptor = 3

type executableBinding struct {
	path        string
	file        *os.File
	fingerprint string
	close       sync.Once
	closeErr    error
}

func bindExecutable(path string) (binding *executableBinding, err error) {
	if !filepath.IsAbs(path) {
		return nil, bindingError(executableUnsafeLocation, nil)
	}
	source, err := openLinuxSource(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if source != nil {
			err = errors.Join(err, source.Close())
		}
	}()
	info, err := source.Stat()
	if err != nil {
		return nil, bindingError(executableOpenFailed, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, bindingError(executableNotExecutable, nil)
	}
	if unix.Getuid() != unix.Geteuid() || unix.Getgid() != unix.Getegid() {
		return nil, bindingError(executableUnsafeLocation, nil)
	}
	if err := unix.Access(fmt.Sprintf("/proc/self/fd/%d", source.Fd()), unix.X_OK); err != nil {
		if errors.Is(err, unix.EACCES) {
			return nil, bindingError(executableNotExecutable, err)
		}
		return nil, bindingError(executableUnsafeLocation, err)
	}
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(source.Fd()), &filesystem); err != nil {
		return nil, bindingError(executableUnsafeLocation, err)
	}
	if filesystem.Flags&unix.ST_NOEXEC != 0 {
		return nil, bindingError(executableNotExecutable, nil)
	}

	snapshotFD, err := unix.MemfdCreate("jastreamer-ffmpeg", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, bindingError(executableBindingFailed, err)
	}
	writable := os.NewFile(uintptr(snapshotFD), "jastreamer-ffmpeg")
	defer func() {
		if writable != nil {
			err = errors.Join(err, writable.Close())
		}
	}()
	if _, err := io.Copy(writable, source); err != nil {
		return nil, bindingError(executableBindingFailed, err)
	}
	if closeErr := source.Close(); closeErr != nil {
		source = nil
		return nil, bindingError(executableOpenFailed, closeErr)
	}
	source = nil
	if _, err := unix.FcntlInt(writable.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_SEAL|unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_WRITE); err != nil {
		return nil, bindingError(executableBindingFailed, err)
	}
	fingerprint, err := fingerprintFile(writable)
	if err != nil {
		return nil, bindingError(executableHashFailed, err)
	}
	readonly, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", writable.Fd()))
	if err != nil {
		return nil, bindingError(executableBindingFailed, err)
	}
	if closeErr := writable.Close(); closeErr != nil {
		writable = nil
		return nil, bindingError(executableBindingFailed, errors.Join(closeErr, readonly.Close()))
	}
	writable = nil
	return &executableBinding{path: path, file: readonly, fingerprint: fingerprint}, nil
}

func openLinuxSource(path string) (*os.File, error) {
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 0 || components[len(components)-1] == "" {
		return nil, bindingError(executableNotExecutable, nil)
	}
	rootFD, err := unix.Open(string(filepath.Separator), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, classifyLinuxOpenError(err)
	}
	parent := os.NewFile(uintptr(rootFD), string(filepath.Separator))
	for _, component := range components[:len(components)-1] {
		nextFD, openErr := unix.Openat(int(parent.Fd()), component, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			return nil, errors.Join(classifyLinuxOpenError(openErr), parent.Close())
		}
		next := os.NewFile(uintptr(nextFD), component)
		if closeErr := parent.Close(); closeErr != nil {
			return nil, errors.Join(bindingError(executableOpenFailed, closeErr), next.Close())
		}
		parent = next
	}
	fd, openErr := unix.Openat(int(parent.Fd()), components[len(components)-1], unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if openErr != nil {
		return nil, errors.Join(classifyLinuxOpenError(openErr), parent.Close())
	}
	source := os.NewFile(uintptr(fd), path)
	if closeErr := parent.Close(); closeErr != nil {
		return nil, errors.Join(bindingError(executableOpenFailed, closeErr), source.Close())
	}
	return source, nil
}

func classifyLinuxOpenError(err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return bindingError(executableNotFound, err)
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR), errors.Is(err, unix.EXDEV), errors.Is(err, unix.EAGAIN):
		return bindingError(executableUnsafeLocation, err)
	default:
		return bindingError(executableOpenFailed, err)
	}
}

func (executable *executableBinding) command(arguments []string) *exec.Cmd {
	command := exec.Command(executable.path, arguments...)
	command.Path = fmt.Sprintf("/proc/self/fd/%d", executableChildDescriptor)
	command.ExtraFiles = []*os.File{executable.file}
	return command
}

func (executable *executableBinding) Close() error {
	executable.close.Do(func() {
		executable.closeErr = executable.file.Close()
	})
	return executable.closeErr
}
