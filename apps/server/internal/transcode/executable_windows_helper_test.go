//go:build windows

package transcode

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

const (
	windowsHelperEnabled = "JASTREAMER_WINDOWS_FFMPEG_HELPER"
	windowsHelperMode    = "JASTREAMER_WINDOWS_FFMPEG_MODE"
)

func TestMain(m *testing.M) {
	if os.Getenv(windowsHelperEnabled) == "1" {
		if err := runWindowsFFmpegHelper(); err != nil {
			if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
				os.Exit(71)
			}
			os.Exit(70)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runWindowsFFmpegHelper() error {
	mode := os.Getenv(windowsHelperMode)
	if mode == "runtime" || mode == "runtime_blocking" {
		if _, err := fmt.Fprint(os.Stdout, "approved"); err != nil {
			return err
		}
		if mode == "runtime_blocking" {
			_, err := io.Copy(io.Discard, os.Stdin)
			return err
		}
		return nil
	}
	if len(os.Args) < 2 {
		return errors.New("missing FFmpeg helper argument")
	}
	if os.Args[1] == "-version" {
		replaced, err := attemptWindowsProbeMutation(mode)
		if err != nil {
			return err
		}
		if replaced {
			if err := os.WriteFile(os.Getenv("FFMPEG_REPLACEMENT_MARKER"), []byte("replacement"), 0o600); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintln(os.Stdout, "ffmpeg version 6.1.1 Copyright fixture")
		return err
	}
	if len(os.Args) != 3 || os.Args[1] != "-hide_banner" {
		return errors.New("unexpected FFmpeg helper arguments")
	}
	switch os.Args[2] {
	case "-decoders":
		_, err := fmt.Fprintln(os.Stdout, " A....D flac\n A....D mp3\n A....D vorbis\n A....D opus\n A....D pcm_s16le")
		return err
	case "-encoders":
		_, err := fmt.Fprintln(os.Stdout, " A..... pcm_s16be")
		return err
	default:
		return errors.New("unexpected FFmpeg capability argument")
	}
}

func attemptWindowsProbeMutation(mode string) (bool, error) {
	if err := os.WriteFile(os.Getenv("FFMPEG_ATTEMPT_MARKER"), []byte("attempted"), 0o600); err != nil {
		return false, err
	}
	switch mode {
	case "probe_replace":
		err := moveFileReplacing(os.Getenv("FFMPEG_REPLACEMENT"), os.Getenv("FFMPEG_SELF"))
		return err == nil, nil
	case "probe_rewrite":
		value, err := os.ReadFile(os.Getenv("FFMPEG_REPLACEMENT"))
		if err != nil {
			return false, err
		}
		err = os.WriteFile(os.Getenv("FFMPEG_SELF"), value, 0o700)
		return err == nil, nil
	case "probe_parent":
		err := moveFile(os.Getenv("FFMPEG_PARENT"), os.Getenv("FFMPEG_OLD_PARENT"))
		if err != nil {
			return false, nil
		}
		err = moveFile(os.Getenv("FFMPEG_REPLACEMENT_PARENT"), os.Getenv("FFMPEG_PARENT"))
		return err == nil, nil
	default:
		return false, errors.New("unexpected FFmpeg helper mode")
	}
}

func moveFile(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePointer, destinationPointer, windows.MOVEFILE_WRITE_THROUGH)
}

func moveFileReplacing(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePointer, destinationPointer, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func copyWindowsTestExecutable(t *testing.T, path string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, value, 0o700); err != nil {
		t.Fatal(err)
	}
}

func availableApproval(t *testing.T, path string) *Approval {
	t.Helper()
	executable, err := bindExecutable(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	approval := newApproval(Diagnostic{Status: StatusAvailable, SHA256: executable.fingerprint}, executable)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})
	return approval
}

func newTestProvider(t *testing.T, config Config) *Provider {
	t.Helper()
	provider, err := NewProvider(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	return provider
}

func readWindowsStream(t *testing.T, stream io.ReadCloser) []byte {
	t.Helper()
	output, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return output
}
