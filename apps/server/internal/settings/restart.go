package settings

import "reflect"

func restartFields(applied, desired Values) []string {
	fields := make([]string, 0, 5)
	if !reflect.DeepEqual(applied.ControlOrigins, desired.ControlOrigins) {
		fields = append(fields, "control_origins")
	}
	if applied.PairingTTLSeconds != desired.PairingTTLSeconds {
		fields = append(fields, "pairing_ttl_seconds")
	}
	if !reflect.DeepEqual(applied.UPnPInterfaces, desired.UPnPInterfaces) {
		fields = append(fields, "upnp_interfaces")
	}
	if applied.K17HTTP != desired.K17HTTP {
		fields = append(fields, "k17_http")
	}
	if applied.FFmpegPath != desired.FFmpegPath {
		fields = append(fields, "ffmpeg_path")
	}
	return fields
}

func updateFields(update Update) []string {
	fields := make([]string, 0, 7)
	if update.DisplayName != nil {
		fields = append(fields, "display_name")
	}
	if update.CatalogRoots != nil {
		fields = append(fields, "catalog_roots")
	}
	if update.ControlOrigins != nil {
		fields = append(fields, "control_origins")
	}
	if update.PairingTTLSeconds != nil {
		fields = append(fields, "pairing_ttl_seconds")
	}
	if update.UPnPInterfaces != nil {
		fields = append(fields, "upnp_interfaces")
	}
	if update.K17HTTP != nil {
		fields = append(fields, "k17_http")
	}
	if update.FFmpegPath != nil {
		fields = append(fields, "ffmpeg_path")
	}
	return fields
}
