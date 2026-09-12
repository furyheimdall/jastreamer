package stream

import (
	"fmt"
	"mime"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

const l16Mime = "audio/L16;rate=44100;channels=2"

type representation struct {
	mime        string
	transformed bool
}

type sourceSpec struct {
	format    string
	canonical string
	aliases   []string
}

func selectRepresentation(track library.Track, protocolInfo []string, transcode bool) (representation, error) {
	spec, err := trackSourceSpec(track)
	if err != nil {
		return representation{}, err
	}
	if selected, ok := supportedOriginal(spec, protocolInfo); ok {
		return representation{mime: selected}, nil
	}
	if transcode && supportsL16(protocolInfo) {
		return representation{mime: l16Mime, transformed: true}, nil
	}
	return representation{}, fmt.Errorf("%w: renderer advertises no compatible sink for %s", ErrUnsupportedMedia, spec.canonical)
}

func trackSourceSpec(track library.Track) (sourceSpec, error) {
	format := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(track.Format), "."))
	var spec sourceSpec
	switch format {
	case "flac":
		spec = sourceSpec{format: "flac", canonical: "audio/flac", aliases: []string{"audio/flac", "audio/x-flac", "application/flac"}}
	case "mp3", "mpeg":
		spec = sourceSpec{format: "mp3", canonical: "audio/mpeg", aliases: []string{"audio/mpeg", "audio/mp3", "audio/x-mp3"}}
	case "wav", "wave", "pcm", "pcm_wav":
		spec = sourceSpec{format: "wav", canonical: "audio/wav", aliases: []string{"audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave"}}
	case "ogg", "vorbis", "ogg_vorbis":
		spec = sourceSpec{format: "ogg", canonical: "audio/ogg", aliases: []string{"audio/ogg", "application/ogg"}}
	case "opus":
		spec = sourceSpec{format: "opus", canonical: "audio/ogg", aliases: []string{"audio/ogg", "application/ogg", "audio/opus"}}
	case "m4a", "mp4", "aac":
		spec = sourceSpec{format: "m4a", canonical: "audio/mp4", aliases: []string{"audio/mp4", "audio/x-m4a", "audio/m4a"}}
	default:
		return sourceSpec{}, fmt.Errorf("%w: unrecognized source format", ErrUnsupportedMedia)
	}
	if track.Mime == "" {
		return sourceSpec{}, fmt.Errorf("%w: source MIME type is unavailable", ErrUnsupportedMedia)
	}
	mediaType, _, err := mime.ParseMediaType(track.Mime)
	if err != nil || !containsMediaType(spec.aliases, mediaType) {
		return sourceSpec{}, fmt.Errorf("%w: source format and MIME type disagree", ErrUnsupportedMedia)
	}
	spec.canonical = strings.ToLower(mediaType)
	return spec, nil
}

func supportedOriginal(spec sourceSpec, values []string) (string, bool) {
	for _, entry := range protocolEntries(values) {
		if entry.transport != "*" && !strings.EqualFold(entry.transport, "http-get") {
			continue
		}
		if !profileCompatible(spec.format, entry.profile) {
			continue
		}
		if !mediaParametersCompatible(spec.format, entry.parameters) {
			continue
		}
		if entry.mediaType == "*" || entry.mediaType == "*/*" || strings.EqualFold(entry.mediaType, "audio/*") {
			return spec.canonical, true
		}
		if containsMediaType(spec.aliases, entry.mediaType) {
			return entry.mediaType, true
		}
	}
	return "", false
}

func supportsL16(values []string) bool {
	for _, entry := range protocolEntries(values) {
		if entry.transport != "*" && !strings.EqualFold(entry.transport, "http-get") {
			continue
		}
		if entry.mediaType != "*" && entry.mediaType != "*/*" && !strings.EqualFold(entry.mediaType, "audio/*") && !strings.EqualFold(entry.mediaType, "audio/l16") {
			continue
		}
		if !l16ParametersCompatible(entry.parameters) {
			continue
		}
		if entry.profile == "" || entry.profile == "*" || strings.EqualFold(entry.profile, "LPCM") || strings.EqualFold(entry.profile, "LPCM_LOW") {
			return true
		}
	}
	return false
}

type protocolEntry struct {
	transport  string
	mediaType  string
	profile    string
	parameters map[string]string
}

func protocolEntries(values []string) []protocolEntry {
	result := make([]protocolEntry, 0, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			transport := "http-get"
			content := item
			additional := ""
			if fields := strings.SplitN(item, ":", 4); len(fields) == 4 {
				transport, content, additional = strings.TrimSpace(fields[0]), strings.TrimSpace(fields[2]), fields[3]
			}
			mediaType, parameters, err := mime.ParseMediaType(content)
			if err != nil {
				mediaType = strings.ToLower(strings.TrimSpace(content))
				parameters = nil
			}
			result = append(result, protocolEntry{
				transport:  strings.ToLower(transport),
				mediaType:  strings.ToLower(mediaType),
				profile:    dlnaProfile(additional),
				parameters: parameters,
			})
		}
	}
	return result
}

func l16ParametersCompatible(parameters map[string]string) bool {
	for key, value := range parameters {
		switch strings.ToLower(key) {
		case "rate":
			if value != "44100" {
				return false
			}
		case "channels":
			if value != "2" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func mediaParametersCompatible(format string, parameters map[string]string) bool {
	for key, value := range parameters {
		if !strings.EqualFold(key, "codecs") {
			return false
		}
		codec := strings.ToLower(strings.TrimSpace(value))
		switch format {
		case "flac":
			if codec != "flac" {
				return false
			}
		case "mp3":
			if codec != "mp3" && codec != "mp3x" {
				return false
			}
		case "ogg":
			if codec != "vorbis" {
				return false
			}
		case "opus":
			if codec != "opus" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func dlnaProfile(additional string) string {
	for _, field := range strings.Split(additional, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(field), "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "DLNA.ORG_PN") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

func profileCompatible(format, profile string) bool {
	if profile == "" || profile == "*" {
		return true
	}
	profile = strings.ToUpper(profile)
	switch format {
	case "flac":
		return profile == "FLAC"
	case "mp3":
		return profile == "MP3" || profile == "MP3X"
	case "wav":
		return profile == "WAV" || profile == "WAVE"
	case "ogg":
		return profile == "OGG" || strings.Contains(profile, "VORBIS")
	case "opus":
		return profile == "OGG" || strings.Contains(profile, "OPUS")
	case "m4a":
		// The library identifies the MP4 container but not its AAC/ALAC codec profile.
		return false
	default:
		return false
	}
}

func containsMediaType(values []string, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
