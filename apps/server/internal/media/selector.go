package media

import (
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func Select(format catalog.Format, capabilities []string, pcmAvailable bool) (Representation, error) {
	mime, ok := mimeFor(format)
	if !ok {
		return "", ErrNoRepresentation
	}
	if supports(capabilities, mime) {
		return Original, nil
	}
	if pcmAvailable && supports(capabilities, "audio/L16") {
		return L16, nil
	}
	return "", ErrNoRepresentation
}

func MimeType(format catalog.Format, representation Representation) (string, bool) {
	if representation == L16 {
		return "audio/L16", true
	}
	if representation != Original {
		return "", false
	}
	return mimeFor(format)
}

func mimeFor(format catalog.Format) (string, bool) {
	switch format {
	case catalog.FormatFLAC:
		return "audio/flac", true
	case catalog.FormatMP3:
		return "audio/mpeg", true
	case catalog.FormatOggVorbis, catalog.FormatOpus:
		return "audio/ogg", true
	case catalog.FormatPCMWAV:
		return "audio/wav", true
	default:
		return "", false
	}
}

func supports(capabilities []string, mime string) bool {
	for _, capability := range capabilities {
		for value := range strings.SplitSeq(capability, ",") {
			fields := strings.Split(strings.TrimSpace(value), ":")
			candidate := strings.TrimSpace(value)
			if len(fields) == 4 {
				if !strings.EqualFold(fields[0], "http-get") && fields[0] != "*" {
					continue
				}
				candidate = fields[2]
			}
			if mediaTypeMatches(candidate, mime) {
				return true
			}
		}
	}
	return false
}

func mediaTypeMatches(candidate, mime string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "*" || candidate == "*/*" {
		return true
	}
	if strings.HasSuffix(candidate, "/*") {
		return strings.EqualFold(strings.TrimSuffix(candidate, "*"), strings.SplitN(mime, "/", 2)[0]+"/")
	}
	return strings.EqualFold(candidate, mime)
}
