//go:build windows

package httpapi

import "testing"

func TestCleanFilesystemPathRejectsWindowsDevicesAndAlternateStreams(t *testing.T) {
	valid := []string{
		`C:\`,
		`C:\Music`,
		`\\server\share`,
		`\\server\share\Music`,
	}
	for _, path := range valid {
		if _, err := cleanFilesystemPath(path); err != nil {
			t.Errorf("cleanFilesystemPath(%q) rejected an ordinary absolute path", path)
		}
	}

	invalid := []string{
		`C:relative`,
		`1:\Music`,
		`\\server`,
		`\\?\C:\Music`,
		`\\.\C:\Music`,
		`\??\C:\Music`,
		`\\??\C:\Music`,
		`C:\Music\track.flac:metadata`,
		`C:\CON`,
		`C:\Music\LPT1.txt`,
		`C:\Music\COM¹`,
		`C:\Music\CON .txt`,
		`C:\Music\LPT².log`,
	}
	for _, path := range invalid {
		if _, err := cleanFilesystemPath(path); err == nil {
			t.Errorf("cleanFilesystemPath(%q) accepted a Windows device or invalid path", path)
		}
	}
}
