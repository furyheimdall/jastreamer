package config

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version          = 1
	maximumFileBytes = 1 << 20
)

type HTTP struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
}

type HTTPS struct {
	Enabled         bool   `json:"enabled"`
	Address         string `json:"address"`
	CertificateFile string `json:"certificate_file"`
	PrivateKeyFile  string `json:"private_key_file"`
}

type Root struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type Network struct {
	Interfaces               []string `json:"interfaces"`
	DiscoveryIntervalSeconds int      `json:"discovery_interval_seconds"`
	PollIntervalSeconds      int      `json:"poll_interval_seconds"`
	AllowedCIDRs             []string `json:"allowed_cidrs"`
}

type Media struct {
	BaseURL    string `json:"base_url"`
	FFmpegPath string `json:"ffmpeg_path"`
	Transcode  bool   `json:"transcode"`
}

type AirPlay struct {
	Enabled    bool   `json:"enabled"`
	HelperPath string `json:"helper_path"`
}

type Config struct {
	Version      int     `json:"version"`
	DataDir      string  `json:"data_dir"`
	ServerName   string  `json:"server_name"`
	HTTP         HTTP    `json:"http"`
	HTTPS        HTTPS   `json:"https"`
	LibraryRoots []Root  `json:"library_roots"`
	Network      Network `json:"network"`
	Media        Media   `json:"media"`
	AirPlay      AirPlay `json:"airplay"`
}

func Default() Config {
	return Config{
		Version:      1,
		DataDir:      "/var/lib/jastreamer",
		HTTP:         HTTP{Enabled: true, Address: ":8080"},
		HTTPS:        HTTPS{Enabled: false, Address: ":8443"},
		LibraryRoots: []Root{},
		Network: Network{
			Interfaces:               []string{},
			DiscoveryIntervalSeconds: 30,
			PollIntervalSeconds:      1,
			AllowedCIDRs:             []string{},
		},
		Media:   Media{},
		AirPlay: AirPlay{},
	}
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open server config: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("inspect server config: %w", err)
	}
	if info.Size() > maximumFileBytes {
		return Config{}, fmt.Errorf("server config exceeds %d bytes", maximumFileBytes)
	}

	decoder := json.NewDecoder(io.LimitReader(file, maximumFileBytes))
	decoder.DisallowUnknownFields()
	var value Config
	if err := decoder.Decode(&value); err != nil {
		return Config{}, fmt.Errorf("decode server config: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode server config: exactly one JSON object is required")
	}
	if err := Validate(value); err != nil {
		return Config{}, err
	}
	return value, nil
}

func Save(path string, value Config) error {
	if path == "" || strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("config path is required")
	}
	if err := Validate(value); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode server config: %w", err)
	}
	encoded = append(encoded, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".server-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary config: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install server config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect server config: %w", err)
	}
	if runtime.GOOS != "windows" {
		parent, err := os.Open(directory)
		if err != nil {
			return fmt.Errorf("open config directory: %w", err)
		}
		if err := parent.Sync(); err != nil {
			_ = parent.Close()
			return fmt.Errorf("sync config directory: %w", err)
		}
		if err := parent.Close(); err != nil {
			return fmt.Errorf("close config directory: %w", err)
		}
	}
	return nil
}

func Validate(value Config) error {
	if value.Version != Version {
		return invalid("version", fmt.Sprintf("must be %d", Version))
	}
	if !value.HTTP.Enabled && !value.HTTPS.Enabled {
		return invalid("http", "HTTP and HTTPS cannot both be disabled")
	}
	if err := validateAbsolutePath("data_dir", value.DataDir, false); err != nil {
		return err
	}
	if value.ServerName != "" {
		if err := validateDisplayText("server_name", value.ServerName, 64); err != nil {
			return err
		}
	}
	if err := validateListener("http.address", value.HTTP.Enabled, value.HTTP.Address); err != nil {
		return err
	}
	if err := validateListener("https.address", value.HTTPS.Enabled, value.HTTPS.Address); err != nil {
		return err
	}
	if err := validateHTTPS(value.HTTPS); err != nil {
		return err
	}
	if err := validateRoots(value.LibraryRoots); err != nil {
		return err
	}
	if err := validateNetwork(value.Network); err != nil {
		return err
	}
	if err := validateMedia(value.Media); err != nil {
		return err
	}
	if err := validateAirPlay(value.AirPlay, value.Media); err != nil {
		return err
	}
	return nil
}

func validateHTTPS(value HTTPS) error {
	if (value.CertificateFile == "") != (value.PrivateKeyFile == "") {
		return invalid("https", "certificate_file and private_key_file must be configured together")
	}
	if value.CertificateFile != "" {
		if err := validateAbsolutePath("https.certificate_file", value.CertificateFile, false); err != nil {
			return err
		}
		if err := validateAbsolutePath("https.private_key_file", value.PrivateKeyFile, false); err != nil {
			return err
		}
	}
	if !value.Enabled {
		return nil
	}
	if value.CertificateFile == "" {
		return invalid("https", "an enabled HTTPS listener requires a certificate and private key")
	}
	if _, err := tls.LoadX509KeyPair(value.CertificateFile, value.PrivateKeyFile); err != nil {
		return invalid("https", "certificate_file and private_key_file must contain a valid PEM key pair")
	}
	return nil
}

func validateRoots(roots []Root) error {
	if len(roots) > 64 {
		return invalid("library_roots", "must contain at most 64 roots")
	}
	ids := make(map[string]struct{}, len(roots))
	paths := make(map[string]struct{}, len(roots))
	for index, root := range roots {
		prefix := "library_roots[" + strconv.Itoa(index) + "]"
		if !validIdentifier(root.ID) {
			return invalid(prefix+".id", "must contain 1-64 letters, digits, hyphens, or underscores")
		}
		if _, exists := ids[root.ID]; exists {
			return invalid(prefix+".id", "must be unique")
		}
		if err := validateDisplayText(prefix+".name", root.Name, 128); err != nil {
			return err
		}
		if err := validateAbsolutePath(prefix+".path", root.Path, false); err != nil {
			return err
		}
		cleaned := filepath.Clean(root.Path)
		if _, exists := paths[cleaned]; exists {
			return invalid(prefix+".path", "must be unique")
		}
		ids[root.ID] = struct{}{}
		paths[cleaned] = struct{}{}
	}
	return nil
}

func validateNetwork(value Network) error {
	if value.DiscoveryIntervalSeconds < 5 || value.DiscoveryIntervalSeconds > 3600 {
		return invalid("network.discovery_interval_seconds", "must be between 5 and 3600")
	}
	if value.PollIntervalSeconds < 1 || value.PollIntervalSeconds > 300 {
		return invalid("network.poll_interval_seconds", "must be between 1 and 300")
	}
	if len(value.Interfaces) > 64 {
		return invalid("network.interfaces", "must contain at most 64 interfaces")
	}
	seenInterfaces := make(map[string]struct{}, len(value.Interfaces))
	for index, name := range value.Interfaces {
		if strings.TrimSpace(name) != name || name == "" || len(name) > 64 || strings.ContainsAny(name, "*?,/\\\x00\r\n") {
			return invalid("network.interfaces["+strconv.Itoa(index)+"]", "must be an exact interface name of at most 64 bytes")
		}
		if _, exists := seenInterfaces[name]; exists {
			return invalid("network.interfaces["+strconv.Itoa(index)+"]", "must be unique")
		}
		seenInterfaces[name] = struct{}{}
	}
	if len(value.AllowedCIDRs) > 64 {
		return invalid("network.allowed_cidrs", "must contain at most 64 networks")
	}
	seenCIDRs := make(map[netip.Prefix]struct{}, len(value.AllowedCIDRs))
	for index, raw := range value.AllowedCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix != prefix.Masked() || !privatePrefix(prefix) {
			return invalid("network.allowed_cidrs["+strconv.Itoa(index)+"]", "must be a canonical private or loopback CIDR")
		}
		if _, exists := seenCIDRs[prefix]; exists {
			return invalid("network.allowed_cidrs["+strconv.Itoa(index)+"]", "must be unique")
		}
		seenCIDRs[prefix] = struct{}{}
	}
	return nil
}

func validateMedia(value Media) error {
	if value.BaseURL != "" {
		parsed, err := url.Parse(value.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return invalid("media.base_url", "must be an HTTP or HTTPS origin without a path, credentials, query, or fragment")
		}
	}
	if value.FFmpegPath != "" {
		if err := validateAbsolutePath("media.ffmpeg_path", value.FFmpegPath, false); err != nil {
			return err
		}
	}
	if value.Transcode && value.FFmpegPath == "" {
		return invalid("media.ffmpeg_path", "is required when transcoding is enabled")
	}
	return nil
}

func validateAirPlay(value AirPlay, media Media) error {
	if value.HelperPath != "" {
		if err := validateAbsolutePath("airplay.helper_path", value.HelperPath, false); err != nil {
			return err
		}
	}
	if !value.Enabled {
		return nil
	}
	if runtime.GOOS != "linux" {
		return invalid("airplay.enabled", "AirPlay sending is supported only on Linux")
	}
	if value.HelperPath == "" {
		return invalid("airplay.helper_path", "is required when AirPlay is enabled")
	}
	if media.FFmpegPath == "" {
		return invalid("media.ffmpeg_path", "is required when AirPlay is enabled")
	}
	if err := validateExecutable("airplay.helper_path", value.HelperPath); err != nil {
		return err
	}
	if err := validateExecutable("media.ffmpeg_path", media.FFmpegPath); err != nil {
		return err
	}
	return nil
}

func validateExecutable(field, path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return invalid(field, "must identify an executable regular file")
	}
	return nil
}

func validateListener(field string, enabled bool, address string) error {
	if !enabled && address == "" {
		return nil
	}
	if enabled && address == "" {
		return invalid(field, "is required when the listener is enabled")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return invalid(field, "must contain an IP address and numeric port")
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 || parsedPort > 65535 {
		return invalid(field, "must contain a numeric port between 1 and 65535")
	}
	if host == "" || host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsUnspecified() && !ip.IsPrivate() && !ip.IsLoopback()) {
		return invalid(field, "must listen on an unspecified, private, or loopback IP address")
	}
	return nil
}

func validateAbsolutePath(field, path string, allowFilesystemRoot bool) error {
	if path == "" || strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) {
		return invalid(field, "must be an absolute path")
	}
	cleaned := filepath.Clean(path)
	if cleaned != path {
		return invalid(field, "must be a clean absolute path")
	}
	if !allowFilesystemRoot && cleaned == filepath.VolumeName(cleaned)+string(filepath.Separator) {
		return invalid(field, "must not be a filesystem root")
	}
	return nil
}

func validateDisplayText(field, value string, maximum int) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return invalid(field, "must be valid text without surrounding whitespace")
	}
	count := 0
	for _, character := range value {
		if unicode.IsControl(character) {
			return invalid(field, "must not contain control characters")
		}
		count++
	}
	if count < 1 || count > maximum {
		return invalid(field, fmt.Sprintf("must contain 1-%d characters", maximum))
	}
	return nil
}

func validIdentifier(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func privatePrefix(prefix netip.Prefix) bool {
	address := prefix.Addr().Unmap()
	bits := prefix.Bits()
	if prefix.Addr().Is4In6() {
		bits -= 96
	}
	prefix = netip.PrefixFrom(address, bits).Masked()
	for _, raw := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "fc00::/7", "::1/128"} {
		allowed := netip.MustParsePrefix(raw)
		if allowed.Bits() <= prefix.Bits() && allowed.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func invalid(field, rule string) error {
	return fmt.Errorf("invalid config field %s: %s", field, rule)
}
