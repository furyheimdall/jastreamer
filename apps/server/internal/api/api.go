package api

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/settings"
	"github.com/jastreamer/jastreamer-server/web/admin"
)

const contractRevision = "control-api-v3"

type Config struct {
	Security                *security.Manager
	Queue                   *playback.Store
	Catalog                 catalog.Snapshot
	CertificateFingerprint  string
	ProductVersion          string
	SourceRevision          string
	Portal                  fs.FS
	Scan                    func(context.Context, catalog.Snapshot) (catalog.Snapshot, error)
	LoadCatalog             func(context.Context) (catalog.Snapshot, error)
	AllowedOrigins          []string
	Settings                *settings.Store
	CatalogCoordinator      *catalog.Coordinator
	CatalogSnapshot         func(context.Context) catalog.Snapshot
	Context                 context.Context
	EventEpoch              uint64
	Media                   *media.Service
	ServerHTTPSOrigin       ServerHTTPSOrigin
	UPnP                    UPnPScanner
	K17HTTPEnabled          bool
	K17MediaBaseURL         string
	K17MediaListenerAddress string
}

type policy struct {
	Mode            string `json:"mode"`
	ArtistGap       int    `json:"artist_gap"`
	AlbumGap        int    `json:"album_gap"`
	Revision        int64  `json:"revision"`
	SessionOverride string `json:"session_override,omitempty"`
}

type server struct {
	config         Config
	catalog        catalog.Snapshot
	catalogMu      sync.RWMutex
	eventHub       *eventBroker
	catalogRoutes  *CatalogHandlers
	rendererRoutes *RendererZoneAPI
}

func New(config Config) http.Handler {
	if config.Context == nil {
		config.Context = context.Background()
	}
	eventHub := newEventBrokerContext(config.Context)
	if config.EventEpoch != 0 {
		eventHub.epoch = config.EventEpoch
	}
	service := &server{config: config, catalog: config.Catalog, eventHub: eventHub}
	if config.Security != nil {
		stopObserving := config.Security.ObserveRevocations(eventHub.revokeDevice)
		if done := config.Context.Done(); done != nil {
			go func() {
				<-done
				stopObserving()
			}()
		}
	}
	service.catalogRoutes = NewCatalogHandlers(service.catalogSnapshot, config.CatalogCoordinator)
	service.catalogRoutes.settings = config.Settings
	if config.CatalogCoordinator != nil {
		config.CatalogCoordinator.ObserveSnapshots(func(snapshot catalog.Snapshot) {
			service.publishState("catalog", snapshot.Revision)
			service.publishState("catalog_scan", snapshot.Generation)
		})
	}
	if config.Security != nil && config.Queue != nil {
		service.rendererRoutes = newRendererZoneAPI(config.Context, config.Security, config.Queue, func(resource string, revision playback.Revision) {
			service.publishState(resource, revision)
		})
		service.rendererRoutes.media = config.Media
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /api/v1/identity", service.identity)
	mux.HandleFunc("POST /api/v1/bootstrap", service.bootstrap)
	mux.HandleFunc("POST /api/v1/pairing-codes", service.pairingCode)
	mux.HandleFunc("POST /api/v1/pairings", service.pair)
	mux.HandleFunc("GET /api/v1/devices", service.devices)
	if service.rendererRoutes != nil {
		mux.HandleFunc("DELETE /api/v1/devices/{deviceID}", service.rendererRoutes.RevokeDevice)
	} else {
		mux.HandleFunc("DELETE /api/v1/devices/{deviceID}", service.revoke)
	}
	mux.HandleFunc("GET /api/v1/discovery", service.discovery)
	mux.HandleFunc("GET /api/v1/catalog/status", service.catalogStatus)
	mux.HandleFunc("GET /api/v1/catalog/tracks", service.controlRoute(service.catalogRoutes.Tracks))
	if config.CatalogCoordinator != nil {
		mux.HandleFunc("GET /api/v1/catalog/roots", service.adminRoute(service.catalogRoutes.Roots))
		mux.HandleFunc("POST /api/v1/catalog/roots", service.adminRoute(service.catalogRoutes.AddRoot))
		mux.HandleFunc("POST /api/v1/catalog/scans", service.adminRoute(service.catalogRoutes.StartScan))
		mux.HandleFunc("GET /api/v1/catalog/scans/{jobID}", service.adminRoute(service.catalogRoutes.ScanStatus))
		mux.HandleFunc("DELETE /api/v1/catalog/scans/{jobID}", service.adminRoute(service.catalogRoutes.CancelScan))
	} else {
		mux.HandleFunc("POST /api/v1/catalog/scans", service.scanCatalog)
	}
	if config.Settings != nil {
		configHandler := newConfigHandler(config.Settings, config.Security, func(revision uint64) {
			service.publishState("config", revision)
		})
		if config.CatalogCoordinator != nil {
			configHandler.reconcile = func(ctx context.Context, roots []settings.CatalogRoot) error {
				return config.CatalogCoordinator.ReconcileRoots(ctx, desiredCatalogRoots(roots))
			}
		}
		mux.Handle("GET /api/v1/config", configHandler)
		mux.Handle("PATCH /api/v1/config", configHandler)
	}
	if service.rendererRoutes != nil {
		mux.HandleFunc("POST /api/v1/zones", service.rendererRoutes.CreateZone)
		mux.HandleFunc("GET /api/v1/zones", service.rendererRoutes.ListZones)
		mux.HandleFunc("PUT /api/v1/zones/{zoneID}/renderer", service.rendererRoutes.AssignRenderer)
		mux.HandleFunc("GET /api/v1/renderers/{rendererID}/session", service.rendererRoutes.AuthorizeRendererSession)
		mux.HandleFunc("GET /api/v1/renderers/{rendererID}/media", service.rendererRoutes.AuthorizeRendererMedia)
		if config.Media != nil {
			mux.HandleFunc("GET /api/v1/renderers/{rendererID}/media/{token}", service.rendererMedia)
		}
	}
	if config.Media != nil {
		mux.Handle("GET /media/v1/{token}", config.Media.K17Handler())
	}
	if config.UPnP != nil {
		mux.HandleFunc("GET /api/v1/upnp/k17/discovery", service.lastK17Scan)
		mux.HandleFunc("POST /api/v1/upnp/k17/discovery", service.scanK17)
	}
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/queue", service.playbackState)
	mux.HandleFunc("POST /api/v1/zones/{zoneID}/queue", service.enqueue)
	mux.HandleFunc("POST /api/v1/zones/{zoneID}/transport", service.mutateTransport)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/playback-state", service.playbackState)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/continuation-policy", service.getPolicy)
	mux.HandleFunc("PATCH /api/v1/zones/{zoneID}/continuation-policy", service.patchPolicy)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/automatic-preview", service.preview)
	mux.HandleFunc("GET /api/v1/zones/{zoneID}/decision-explanation", service.explanation)
	mux.HandleFunc("POST /api/v1/event-tickets", service.eventTicket)
	mux.HandleFunc("GET /api/v1/events", service.events)
	if config.Portal != nil {
		mux.Handle("GET /pair/", http.StripPrefix("/pair/", http.FileServerFS(config.Portal)))
		mux.HandleFunc("GET /pair", func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "/pair/", http.StatusTemporaryRedirect)
		})
	}
	mux.Handle("GET /admin/", http.StripPrefix("/admin/", http.FileServerFS(admin.Assets)))
	mux.HandleFunc("GET /admin", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/admin/", http.StatusTemporaryRedirect)
	})
	return secureHeaders(mux, config.AllowedOrigins)
}

func (service *server) controlRoute(handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if _, ok := service.authenticate(writer, request); ok {
			handler(writer, request)
		}
	}
}

func (service *server) adminRoute(handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if service.requireAdmin(writer, request) {
			handler(writer, request)
		}
	}
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
