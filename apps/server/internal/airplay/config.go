package airplay

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultDiscoveryInterval = 30 * time.Second
	discoveryWindow          = 2 * time.Second
)

var (
	ErrInvalidConfig       = errors.New("invalid AirPlay configuration")
	ErrUnsupportedPlatform = errors.New("AirPlay sending is unsupported on this platform")
)

type Config struct {
	Interfaces        []string
	DiscoveryInterval time.Duration
	FFmpegPath        string
	HelperPath        string
	DataDir           string
	Notify            func(string)
}

func validateConfig(config Config) (Config, error) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return Config{}, fmt.Errorf("airplay: %w (%s/%s)", ErrUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}
	if config.DiscoveryInterval < 0 {
		return Config{}, fmt.Errorf("airplay: discovery interval: %w", ErrInvalidConfig)
	}
	if config.DiscoveryInterval == 0 {
		config.DiscoveryInterval = defaultDiscoveryInterval
	}
	var err error
	config.FFmpegPath, err = executablePath(config.FFmpegPath, "FFmpeg")
	if err != nil {
		return Config{}, err
	}
	config.HelperPath, err = executablePath(config.HelperPath, "sender helper")
	if err != nil {
		return Config{}, err
	}
	if config.DataDir == "" || strings.ContainsRune(config.DataDir, '\x00') || !filepath.IsAbs(config.DataDir) {
		return Config{}, fmt.Errorf("airplay: data directory must be an absolute local path: %w", ErrInvalidConfig)
	}
	config.DataDir = filepath.Clean(config.DataDir)
	if filepath.Dir(config.DataDir) == config.DataDir {
		return Config{}, fmt.Errorf("airplay: filesystem root cannot be the data directory: %w", ErrInvalidConfig)
	}
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("airplay: create private data directory: %w", err)
	}
	before, err := os.Lstat(config.DataDir)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return Config{}, fmt.Errorf("airplay: private data directory is unavailable or unsafe: %w", ErrInvalidConfig)
	}
	if err := os.Chmod(config.DataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("airplay: protect private data directory: %w", err)
	}
	after, err := os.Lstat(config.DataDir)
	if err != nil || !after.IsDir() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) {
		return Config{}, fmt.Errorf("airplay: private data directory changed during validation: %w", ErrInvalidConfig)
	}
	if config.Notify == nil {
		config.Notify = func(string) {}
	}
	return config, nil
}

func executablePath(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("airplay: %s path is required: %w", label, ErrInvalidConfig)
	}
	resolved := value
	if !filepath.IsAbs(value) {
		var err error
		resolved, err = exec.LookPath(value)
		if err != nil {
			return "", fmt.Errorf("airplay: %s is unavailable: %w", label, err)
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("airplay: %s is unavailable: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("airplay: %s is not an executable regular file: %w", label, ErrInvalidConfig)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("airplay: resolve %s path: %w", label, err)
	}
	return absolute, nil
}
