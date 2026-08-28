package upnp

import (
	"bytes"
	"encoding/xml"
	"fmt"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func didlMetadata(resource playback.MediaResource) (string, error) {
	if resource.URL == "" || resource.MimeType == "" || resource.TrackID == "" || !validMediaRepresentation(resource.Representation) {
		return "", ErrInvalidConfig
	}
	title := resource.Title
	if title == "" {
		title = string(resource.TrackID)
	}
	var result bytes.Buffer
	result.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:jas="urn:jas-streamer:metadata-1-0/"><item id="`)
	if err := xml.EscapeText(&result, []byte(resource.TrackID)); err != nil {
		return "", fmt.Errorf("escape DIDL track identity: %w", err)
	}
	result.WriteString(`" parentID="0" restricted="1"><dc:title>`)
	if err := xml.EscapeText(&result, []byte(title)); err != nil {
		return "", fmt.Errorf("escape DIDL title: %w", err)
	}
	result.WriteString(`</dc:title><upnp:class>object.item.audioItem.musicTrack</upnp:class><res protocolInfo="http-get:*:`)
	if err := xml.EscapeText(&result, []byte(resource.MimeType)); err != nil {
		return "", fmt.Errorf("escape DIDL MIME type: %w", err)
	}
	result.WriteString(`:*" jas:representation="`)
	if err := xml.EscapeText(&result, []byte(resource.Representation)); err != nil {
		return "", fmt.Errorf("escape DIDL representation: %w", err)
	}
	result.WriteString(`">`)
	if err := xml.EscapeText(&result, []byte(resource.URL)); err != nil {
		return "", fmt.Errorf("escape DIDL resource URL: %w", err)
	}
	result.WriteString(`</res></item></DIDL-Lite>`)
	return result.String(), nil
}

func validMediaRepresentation(value playback.MediaRepresentation) bool {
	return value == playback.MediaOriginal || value == playback.MediaL16
}
