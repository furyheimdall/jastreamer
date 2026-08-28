package settings

import (
	"errors"
	"fmt"
)

const CurrentSchemaVersion = 1

var (
	ErrRevisionMismatch    = errors.New("settings: revision mismatch")
	ErrRevisionExhausted   = errors.New("settings: revision exhausted")
	ErrIdempotencyConflict = errors.New("settings: idempotency conflict")
)

type CatalogRoot struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
}

type K17HTTP struct {
	Enabled         bool   `json:"enabled"`
	ListenerAddress string `json:"listener_address"`
}

type Values struct {
	DisplayName       string        `json:"display_name"`
	CatalogRoots      []CatalogRoot `json:"catalog_roots"`
	ControlOrigins    []string      `json:"control_origins"`
	PairingTTLSeconds int           `json:"pairing_ttl_seconds"`
	UPnPInterfaces    []string      `json:"upnp_interfaces"`
	K17HTTP           K17HTTP       `json:"k17_http"`
	FFmpegPath        string        `json:"ffmpeg_path"`
}

type Locks struct {
	ListenAddress           string   `json:"listen_address"`
	CertificateFingerprint  string   `json:"certificate_fingerprint"`
	CertificateSANs         []string `json:"certificate_sans"`
	DataDirectory           string   `json:"data_directory"`
	AllowedCatalogBases     []string `json:"allowed_catalog_bases"`
	Environment             string   `json:"environment"`
	EnvironmentLockedFields []string `json:"environment_locked_fields"`
}

type Config struct {
	Path     string
	Defaults Values
	Locks    Locks
}

type Update struct {
	DisplayName       *string
	CatalogRoots      *[]CatalogRoot
	ControlOrigins    *[]string
	PairingTTLSeconds *int
	UPnPInterfaces    *[]string
	K17HTTP           *K17HTTP
	FFmpegPath        *string
}

type Mutation struct {
	ExpectedRevision uint64
	IdempotencyKey   string
	Update           Update
}

type MutationResult struct {
	Snapshot Snapshot
	Replayed bool
}

type FFmpegDiagnostic struct {
	ConfiguredPath string `json:"-"`
	Status         string `json:"status"`
	ExecutableSHA  string `json:"executable_sha256,omitempty"`
	Version        string `json:"version,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

type Diagnostics struct {
	FFmpeg FFmpegDiagnostic `json:"ffmpeg"`
}

type Snapshot struct {
	SchemaVersion   int         `json:"schema_version"`
	Revision        uint64      `json:"revision"`
	Settings        Values      `json:"settings"`
	Locks           Locks       `json:"locks"`
	Diagnostics     Diagnostics `json:"diagnostics"`
	RestartRequired bool        `json:"restart_required"`
	RestartFields   []string    `json:"restart_fields"`
}

type Document struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Settings      Values `json:"settings"`
}

type ValidationError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
}

func (value *ValidationError) Error() string {
	return fmt.Sprintf("settings: field %s failed %s", value.Field, value.Rule)
}

type LockedFieldError struct{ Field string }

func (value *LockedFieldError) Error() string {
	return fmt.Sprintf("settings: field %s is locked", value.Field)
}
