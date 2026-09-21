package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const diagnosticLogBytes int64 = 5 << 20
const diagnosticLogBackups = 3

// diagnosticLog keeps console output available even if persistent logging fails.
// Its files live inside the existing data directory, not the container filesystem.
type diagnosticLog struct {
	mu       sync.Mutex
	console  io.Writer
	path     string
	file     *os.File
	size     int64
	maxBytes int64
	failed   bool
	closed   bool
}

func startDiagnosticLogging(dataDir string) (func(), error) {
	previousOutput, previousFlags := log.Writer(), log.Flags()
	writer, err := openDiagnosticLog(dataDir, previousOutput)
	if err != nil {
		return nil, err
	}
	log.SetOutput(writer)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	log.Printf("diagnostic component=server event=diagnostics_started source_revision=%q max_file_bytes=%d retained_files=%d", resolvedSourceRevision(), diagnosticLogBytes, diagnosticLogBackups+1)
	return func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		if err := writer.Close(); err != nil {
			_, _ = fmt.Fprintf(previousOutput, "diagnostic component=server event=log_close_failed error_type=%T\n", err)
		}
	}, nil
}

func openDiagnosticLog(dataDir string, console io.Writer) (*diagnosticLog, error) {
	directory := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create diagnostic log directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("diagnostic log directory must be a real directory")
	}
	writer := &diagnosticLog{console: console, path: filepath.Join(directory, "server.log"), maxBytes: diagnosticLogBytes}
	if err := writer.open(); err != nil {
		return nil, fmt.Errorf("open diagnostic log: %w", err)
	}
	return writer, nil
}

func (writer *diagnosticLog) open() error {
	if info, err := os.Lstat(writer.path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("diagnostic log must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	writer.file, writer.size = file, info.Size()
	return nil
}

func (writer *diagnosticLog) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	n, consoleErr := writer.console.Write(data)
	persistentErr := writer.persist(data)
	if persistentErr != nil && !writer.failed {
		_, _ = fmt.Fprintf(writer.console, "diagnostic component=server event=log_write_failed error_type=%T console_logging_continues=true\n", persistentErr)
	} else if persistentErr == nil && writer.failed {
		_, _ = fmt.Fprintln(writer.console, "diagnostic component=server event=log_write_recovered")
	}
	writer.failed = persistentErr != nil
	return n, consoleErr
}

func (writer *diagnosticLog) persist(data []byte) error {
	if writer.closed {
		return os.ErrClosed
	}
	if int64(len(data)) > writer.maxBytes {
		return errors.New("diagnostic record exceeds file limit")
	}
	if writer.file == nil {
		if err := writer.open(); err != nil {
			return err
		}
	}
	if writer.size+int64(len(data)) > writer.maxBytes {
		if err := writer.rotate(); err != nil {
			return err
		}
	}
	n, err := writer.file.Write(data)
	writer.size += int64(n)
	return err
}

func (writer *diagnosticLog) rotate() error {
	err := writer.file.Close()
	writer.file = nil
	if err != nil {
		return err
	}
	oldest := writer.path + "." + strconv.Itoa(diagnosticLogBackups)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := diagnosticLogBackups - 1; index >= 0; index-- {
		source := writer.path
		if index > 0 {
			source += "." + strconv.Itoa(index)
		}
		if err := os.Rename(source, writer.path+"."+strconv.Itoa(index+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return writer.open()
}

func (writer *diagnosticLog) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.closed = true
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	return err
}
