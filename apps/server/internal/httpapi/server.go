package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/discovery"
	"github.com/jastreamer/jastreamer-server/internal/events"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
	"github.com/jastreamer/jastreamer-server/internal/stream"
)

type Options struct {
	Context      context.Context
	ConfigPath   string
	Config       config.Config
	Auth         *auth.Service
	Discovery    *discovery.Service
	Library      *library.Service
	Devices      *output.Manager
	Player       *player.Service
	Stream       *stream.Service
	Events       *events.Hub
	UI           fs.FS
	TrustedHosts []string
}

type server struct {
	options  Options
	configMu sync.Mutex
	throttle loginThrottle
	index    []byte
	assets   http.Handler
}

func New(options Options) http.Handler {
	service := &server{options: options}
	service.index, _ = fs.ReadFile(options.UI, "index.html")
	service.assets = http.FileServer(http.FS(options.UI))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v1/discovery", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, options.Discovery.Metadata())
	})
	mux.HandleFunc("GET /api/v1/setup", service.setupState)
	mux.HandleFunc("POST /api/v1/setup", service.setup)
	mux.HandleFunc("GET /api/v1/session", service.session)
	mux.HandleFunc("POST /api/v1/login", service.login)
	mux.HandleFunc("POST /api/v1/logout", service.require(service.logout))
	mux.HandleFunc("POST /api/v1/account/password", service.require(service.password))
	mux.HandleFunc("GET /api/v1/config", service.require(service.getConfig))
	mux.HandleFunc("PUT /api/v1/config", service.require(service.putConfig))
	for _, kind := range []string{"tracks", "albums", "artists", "genres", "folders"} {
		mux.HandleFunc("GET /api/v1/library/"+kind, service.require(service.browse(kind)))
	}
	mux.HandleFunc("GET /api/v1/library/tracks/{id}", service.require(service.track))
	mux.HandleFunc("GET /api/v1/artwork/{id}", service.require(service.artwork))
	mux.HandleFunc("GET /api/v1/library/scans", service.require(service.scans))
	mux.HandleFunc("POST /api/v1/library/scans", service.require(service.startScan))
	mux.HandleFunc("DELETE /api/v1/library/scans/{id}", service.require(service.cancelScan))
	mux.HandleFunc("GET /api/v1/playlists", service.require(service.playlists))
	mux.HandleFunc("POST /api/v1/playlists", service.require(service.savePlaylist))
	mux.HandleFunc("GET /api/v1/playlists/{id}", service.require(service.playlist))
	mux.HandleFunc("PUT /api/v1/playlists/{id}", service.require(service.savePlaylist))
	mux.HandleFunc("DELETE /api/v1/playlists/{id}", service.require(service.deletePlaylist))
	mux.HandleFunc("GET /api/v1/renderers", service.require(service.renderers))
	mux.HandleFunc("POST /api/v1/renderers/refresh", service.require(service.refreshRenderers))
	mux.HandleFunc("POST /api/v1/renderers/{id}/pairing", service.require(service.pairRenderer))
	mux.HandleFunc("GET /api/v1/player", service.require(service.playerState))
	mux.HandleFunc("POST /api/v1/player", service.require(service.command))
	mux.HandleFunc("PUT /api/v1/player/output", service.require(service.output))
	mux.HandleFunc("GET /api/v1/queue", service.require(service.queue))
	mux.HandleFunc("POST /api/v1/queue", service.require(service.mutateQueue))
	mux.HandleFunc("GET /api/v1/events", service.require(service.live))
	mux.Handle("/media/", options.Stream.Handler())
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, fault.New(404, "NOT_FOUND", "요청한 API가 없습니다."))
	})
	mux.HandleFunc("/", service.static)
	return service.guard(mux)
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if status != http.StatusNoContent {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeError(w http.ResponseWriter, err error) {
	var known *fault.Error
	if errors.As(err, &known) {
		reply(w, known.Status, map[string]any{"error": map[string]string{"code": known.Code, "message": known.Message}})
		return
	}
	log.Printf("HTTP operation failed: %T", err)
	reply(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "INTERNAL_ERROR", "message": "서버 작업을 완료하지 못했습니다."}})
}

const requestBodyTimeout = 5 * time.Second

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(requestBodyTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, err)
		return false
	}
	complete := false
	defer func() {
		if complete {
			_ = controller.SetReadDeadline(time.Time{})
		} else {
			r.Close = true
		}
	}()
	fail := func(err error) bool {
		r.Close = true
		w.Header().Set("Connection", "close")
		var timeout net.Error
		var oversized *http.MaxBytesError
		switch {
		case errors.As(err, &timeout) && timeout.Timeout():
			writeError(w, fault.New(408, "REQUEST_TIMEOUT", "요청 본문을 보내는 시간이 초과되었습니다."))
		case errors.As(err, &oversized):
			writeError(w, fault.New(413, "REQUEST_TOO_LARGE", "요청 본문이 너무 큽니다."))
		default:
			writeError(w, fault.New(400, "INVALID_REQUEST", "하나의 올바른 JSON 객체가 필요합니다."))
		}
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fail(err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fail(err)
	}
	complete = true
	return true
}

func integer(r *http.Request, key string, fallback, minimum, maximum int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fault.New(400, "INVALID_QUERY", "조회 범위가 올바르지 않습니다.")
	}
	return value, nil
}
