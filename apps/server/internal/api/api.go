package api

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/playback"
	"github.com/jakestreamer/jstreamer-server/internal/security"
)

const contractRevision = "control-api-v2"

type Config struct {
	Security               *security.Manager
	Queue                  *playback.Store
	Catalog                catalog.Snapshot
	CertificateFingerprint string
	ProductVersion         string
	SourceRevision         string
	Portal                 fs.FS
	Scan                   func(context.Context, catalog.Snapshot) (catalog.Snapshot, error)
	LoadCatalog            func(context.Context) (catalog.Snapshot, error)
	AllowedOrigins         []string
}

type policy struct {
	Mode            string `json:"mode"`
	ArtistGap       int    `json:"artist_gap"`
	AlbumGap        int    `json:"album_gap"`
	Revision        int64  `json:"revision"`
	SessionOverride string `json:"session_override,omitempty"`
}

type server struct {
	config    Config
	catalog   catalog.Snapshot
	catalogMu sync.RWMutex
	eventHub  *eventBroker
}

func New(config Config) http.Handler {
	service := &server{config: config, catalog: config.Catalog, eventHub: newEventBroker()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /api/v1/identity", service.identity)
	mux.HandleFunc("POST /api/v1/bootstrap", service.bootstrap)
	mux.HandleFunc("POST /api/v1/pairing-codes", service.pairingCode)
	mux.HandleFunc("POST /api/v1/pairings", service.pair)
	mux.HandleFunc("GET /api/v1/devices", service.devices)
	mux.HandleFunc("DELETE /api/v1/devices/{deviceID}", service.revoke)
	mux.HandleFunc("GET /api/v1/discovery", service.discovery)
	mux.HandleFunc("GET /api/v1/catalog/status", service.catalogStatus)
	mux.HandleFunc("POST /api/v1/catalog/scans", service.scanCatalog)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/queue", service.playbackState)
	mux.HandleFunc("POST /api/v1/zones/{zoneID}/queue", service.enqueue)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/playback-state", service.playbackState)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/continuation-policy", service.getPolicy)
	mux.HandleFunc("PATCH /api/v1/zones/{zoneID}/continuation-policy", service.patchPolicy)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/automatic-preview", service.preview)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/decision-explanation", service.explanation)
	mux.HandleFunc("GET /api/v1/events", service.events)
	if config.Portal != nil {
		mux.Handle("GET /pair/", http.StripPrefix("/pair/", http.FileServerFS(config.Portal)))
		mux.HandleFunc("GET /pair", func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "/pair/", http.StatusTemporaryRedirect)
		})
	}
	return secureHeaders(mux, config.AllowedOrigins)
}

func secureHeaders(next http.Handler, allowedOrigins []string) http.Handler {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' wss:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writer.Header().Set("Cache-Control", "no-store")
		}
		origin := request.Header.Get("Origin")
		_, configuredOrigin := origins[origin]
		sameOrigin := origin == "https://"+request.Host || origin == "http://"+request.Host
		allowed := configuredOrigin || sameOrigin
		if origin != "" {
			writer.Header().Add("Vary", "Origin")
		}
		if strings.HasPrefix(request.URL.Path, "/api/") && origin != "" && !allowed {
			invalid(writer, "ORIGIN_FORBIDDEN", "request origin is not allowed", http.StatusForbidden)
			return
		}
		if allowed {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, Idempotency-Key, X-Jake-Protocol-Major, X-Jake-Supported-Protocol-Majors")
		}
		if request.Method == http.MethodOptions {
			if !allowed {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
