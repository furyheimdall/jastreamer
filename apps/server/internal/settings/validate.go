package settings

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func normalizeValues(values Values, allowedBases []string) (Values, error) {
	if strings.TrimSpace(values.DisplayName) != values.DisplayName || utf8.RuneCountInString(values.DisplayName) < 1 || utf8.RuneCountInString(values.DisplayName) > 80 {
		return Values{}, invalid("display_name", "must contain 1-80 characters without surrounding whitespace")
	}
	if values.PairingTTLSeconds < 60 || values.PairingTTLSeconds > 3600 {
		return Values{}, invalid("pairing_ttl_seconds", "must be between 60 and 3600")
	}
	roots, err := normalizeRoots(values.CatalogRoots, allowedBases)
	if err != nil {
		return Values{}, err
	}
	origins, err := normalizeOrigins(values.ControlOrigins)
	if err != nil {
		return Values{}, err
	}
	interfaces, err := normalizeInterfaces(values.UPnPInterfaces)
	if err != nil {
		return Values{}, err
	}
	if err := validateK17HTTP(values.K17HTTP); err != nil {
		return Values{}, err
	}
	if values.FFmpegPath != "" && !filepath.IsAbs(values.FFmpegPath) {
		return Values{}, invalid("ffmpeg_path", "must be an explicit absolute path")
	}
	values.CatalogRoots = roots
	values.ControlOrigins = origins
	values.UPnPInterfaces = interfaces
	values.FFmpegPath = filepath.Clean(values.FFmpegPath)
	if values.FFmpegPath == "." {
		values.FFmpegPath = ""
	}
	return values, nil
}

func normalizeRoots(roots []CatalogRoot, allowedBases []string) ([]CatalogRoot, error) {
	if len(roots) > 32 {
		return nil, invalid("catalog_roots", "must contain at most 32 roots")
	}
	result := make([]CatalogRoot, len(roots))
	ids := make(map[string]struct{}, len(roots))
	paths := make(map[string]struct{}, len(roots))
	for index, root := range roots {
		prefix := "catalog_roots[" + strconv.Itoa(index) + "]"
		if !validIdentifier(root.ID) {
			return nil, invalid(prefix+".id", "must use 1-64 letters, digits, underscores, or hyphens")
		}
		if _, exists := ids[root.ID]; exists {
			return nil, invalid(prefix+".id", "must be unique")
		}
		if strings.TrimSpace(root.DisplayName) != root.DisplayName || utf8.RuneCountInString(root.DisplayName) < 1 || utf8.RuneCountInString(root.DisplayName) > 80 {
			return nil, invalid(prefix+".display_name", "must contain 1-80 characters")
		}
		resolved, err := filepath.EvalSymlinks(root.Path)
		if err != nil {
			return nil, invalid(prefix+".path", "must name an existing path")
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("absolute catalog root: %w", err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return nil, invalid(prefix+".path", "must name an existing directory")
		}
		contained := false
		for _, base := range allowedBases {
			relative, relErr := filepath.Rel(base, resolved)
			if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				contained = true
				break
			}
		}
		if !contained {
			return nil, invalid(prefix+".path", "must remain beneath an allowed catalog base")
		}
		if _, exists := paths[resolved]; exists {
			return nil, invalid(prefix+".path", "must be unique")
		}
		ids[root.ID] = struct{}{}
		paths[resolved] = struct{}{}
		result[index] = CatalogRoot{ID: root.ID, DisplayName: root.DisplayName, Path: resolved}
	}
	return result, nil
}

func normalizeOrigins(origins []string) ([]string, error) {
	result := slices.Clone(origins)
	seen := make(map[string]struct{}, len(origins))
	for index, raw := range origins {
		parsed, err := url.Parse(raw)
		valid := err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
		if !valid || strings.Contains(raw, "*") || parsed.String() != raw {
			return nil, invalid("control_origins["+strconv.Itoa(index)+"]", "must be an exact HTTPS origin")
		}
		if _, exists := seen[raw]; exists {
			return nil, invalid("control_origins["+strconv.Itoa(index)+"]", "must be unique")
		}
		seen[raw] = struct{}{}
	}
	return result, nil
}

func normalizeInterfaces(interfaces []string) ([]string, error) {
	result := slices.Clone(interfaces)
	seen := make(map[string]struct{}, len(interfaces))
	for index, name := range interfaces {
		if strings.TrimSpace(name) != name || name == "" || len(name) > 64 || strings.ContainsAny(name, "*?,\x00\r\n") {
			return nil, invalid("upnp_interfaces["+strconv.Itoa(index)+"]", "must be an exact interface name")
		}
		if _, exists := seen[name]; exists {
			return nil, invalid("upnp_interfaces["+strconv.Itoa(index)+"]", "must be unique")
		}
		seen[name] = struct{}{}
	}
	return result, nil
}

func validateK17HTTP(value K17HTTP) error {
	if !value.Enabled && value.ListenerAddress == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(value.ListenerAddress)
	if err != nil {
		return invalid("k17_http.listener_address", "must contain a private IP address and port")
	}
	ip := net.ParseIP(host)
	parsedPort, portErr := strconv.ParseUint(port, 10, 16)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) || portErr != nil || parsedPort == 0 {
		return invalid("k17_http.listener_address", "must contain a private IP address and non-zero port")
	}
	if value.Enabled && value.ListenerAddress == "" {
		return invalid("k17_http.listener_address", "is required when K17 HTTP compatibility is enabled")
	}
	return nil
}

func validIdentifier(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func invalid(field, rule string) error { return &ValidationError{Field: field, Rule: rule} }
