package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDiagnosticLogRetainsNewestRecordsAcrossRestart(t *testing.T) {
	directory := t.TempDir()
	writer, err := openDiagnosticLog(directory, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	writer.maxBytes = 18
	for index := 0; index < 9; index++ {
		if _, err := fmt.Fprintf(writer, "entry-%02d\n", index); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err = openDiagnosticLog(directory, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.maxBytes = 18
	_, _ = io.WriteString(writer, "entry-09\n")
	entries, err := os.ReadDir(filepath.Join(directory, "logs"))
	if err != nil || len(entries) != 4 {
		t.Fatalf("retained log files: %d, %v", len(entries), err)
	}
	for index := 0; index < 4; index++ {
		path := writer.path
		if index > 0 {
			path += "." + strconv.Itoa(index)
		}
		data, err := os.ReadFile(path)
		expected := fmt.Sprintf("entry-%02d\nentry-%02d\n", 8-index*2, 9-index*2)
		if err != nil || string(data) != expected {
			t.Fatalf("retained file %d: %q, %v; want %q", index, data, err, expected)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("log file must remain private: %v, %v", info, err)
			}
		}
	}
}

func TestDiagnosticLogDiskFailureKeepsConsoleAndBoundsWarning(t *testing.T) {
	var console bytes.Buffer
	writer, err := openDiagnosticLog(t.TempDir(), &console)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// A closed descriptor gives a deterministic write failure without changing host permissions.
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"first event\n", "second event\n"} {
		if n, err := io.WriteString(writer, message); err != nil || n != len(message) {
			t.Fatalf("console logging failed with persistent writer: %d, %v", n, err)
		}
	}
	if !strings.Contains(console.String(), "first event\n") || !strings.Contains(console.String(), "second event\n") {
		t.Fatalf("console lost original events: %q", console.String())
	}
	if strings.Count(console.String(), "event=log_write_failed") != 1 || strings.Contains(console.String(), writer.path) {
		t.Fatalf("persistent failure must be bounded and omit filesystem details: %q", console.String())
	}
}

func TestDiagnosticLogRejectsSymlinkDirectory(t *testing.T) {
	directory, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(directory, "logs")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if writer, err := openDiagnosticLog(directory, io.Discard); err == nil {
		_ = writer.Close()
		t.Fatal("diagnostics followed a symlink outside the data directory")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("diagnostics modified external directory: %v, %v", entries, err)
	}
}
