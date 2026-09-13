package httpapi

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

const restartPreparationTimeout = 30 * time.Second

type RestartRequest struct {
	Config   config.Config
	Active   config.Config
	Revision string
}

type RestartHooks struct {
	Prepare func(context.Context, config.Config) error
	Commit  func(RestartRequest)
}

type mutationGate struct {
	mu       sync.Mutex
	draining bool
	active   int
	idle     chan struct{}
}

func (gate *mutationGate) enter() bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.draining {
		return false
	}
	gate.active++
	return true
}

func (gate *mutationGate) leave() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.active--
	if gate.draining && gate.active == 0 && gate.idle != nil {
		close(gate.idle)
		gate.idle = nil
	}
}

func (gate *mutationGate) begin(ctx context.Context) error {
	gate.mu.Lock()
	if gate.draining {
		gate.mu.Unlock()
		return fault.New(http.StatusConflict, "RESTART_IN_PROGRESS", "A server restart is already in progress.")
	}
	gate.draining = true
	if gate.active == 0 {
		gate.mu.Unlock()
		return nil
	}
	gate.idle = make(chan struct{})
	idle := gate.idle
	gate.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		gate.abort()
		return fault.New(http.StatusConflict, "RESTART_BUSY", fmt.Sprintf("Active mutations did not finish before restart preparation timed out: %v", ctx.Err()))
	}
}

func (gate *mutationGate) abort() {
	gate.mu.Lock()
	gate.draining = false
	gate.idle = nil
	gate.mu.Unlock()
}

func (service *server) mutating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !service.mutations.enter() {
			writeError(w, fault.New(http.StatusConflict, "RESTART_IN_PROGRESS", "A server restart is already in progress."))
			return
		}
		defer service.mutations.leave()
		next(w, r)
	}
}

func (service *server) restart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	if service.options.Restart == nil || service.options.Restart.Prepare == nil || service.options.Restart.Commit == nil {
		writeError(w, fault.New(http.StatusConflict, "RESTART_UNSUPPORTED", "This server cannot restart itself in its current hosting environment."))
		return
	}
	prepareCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), restartPreparationTimeout)
	defer cancel()
	if err := service.mutations.begin(prepareCtx); err != nil {
		writeError(w, err)
		return
	}
	accepted := false
	defer func() {
		if !accepted {
			service.mutations.abort()
		}
	}()

	service.configMu.Lock()
	defer service.configMu.Unlock()
	target, err := config.Load(service.options.ConfigPath)
	if err != nil {
		writeError(w, fault.New(http.StatusConflict, "INVALID_CONFIG", err.Error()))
		return
	}
	revision := configRevision(target)
	active := service.activeConfigValue()
	if body.Revision == "" || body.Revision != revision {
		writeError(w, fault.New(http.StatusConflict, "CONFIG_CONFLICT", "The saved configuration changed. Reload it before restarting."))
		return
	}
	if target.DataDir != active.DataDir {
		writeError(w, fault.New(http.StatusConflict, "DATA_DIR_MIGRATION_REQUIRED", "Changing data_dir requires a manual data migration and process restart."))
		return
	}
	if !restartRequired(active, target) {
		writeError(w, fault.New(http.StatusConflict, "RESTART_NOT_REQUIRED", "The saved configuration is already active."))
		return
	}
	if err = service.options.Restart.Prepare(prepareCtx, target); err != nil {
		writeError(w, err)
		return
	}
	current, err := config.Load(service.options.ConfigPath)
	if err != nil {
		writeError(w, fault.New(http.StatusConflict, "CONFIG_CONFLICT", fmt.Sprintf("The saved configuration could not be reloaded after restart preparation: %v", err)))
		return
	}
	if configRevision(current) != revision {
		writeError(w, fault.New(http.StatusConflict, "CONFIG_CONFLICT", "The saved configuration changed while the restart was being prepared."))
		return
	}
	reconnectURL, err := restartURL(r, active, target)
	if err != nil {
		writeError(w, fault.New(http.StatusConflict, "RESTART_TARGET_INVALID", err.Error()))
		return
	}
	w.Header().Set("Connection", "close")
	reply(w, http.StatusAccepted, map[string]string{"runtime_id": service.options.RuntimeID, "reconnect_url": reconnectURL})
	if err = http.NewResponseController(w).Flush(); err != nil {
		log.Printf("Restart acknowledgement could not be flushed: %v", err)
		return
	}
	accepted = true
	service.options.Restart.Commit(RestartRequest{Config: target, Active: active, Revision: revision})
}

func restartURL(r *http.Request, active, target config.Config) (string, error) {
	secure := r.TLS != nil
	if secure && !target.HTTPS.Enabled {
		secure = false
	} else if !secure && !target.HTTP.Enabled {
		secure = true
	}
	targetAddress, oldAddress := target.HTTP.Address, active.HTTP.Address
	oldEnabled := active.HTTP.Enabled
	if secure {
		targetAddress, oldAddress = target.HTTPS.Address, active.HTTPS.Address
		oldEnabled = active.HTTPS.Enabled
	}
	targetHost, targetPort, err := net.SplitHostPort(targetAddress)
	if err != nil {
		return "", fmt.Errorf("target listener address is invalid: %w", err)
	}
	requestHost := r.Host
	if host, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
		requestHost = host
	}
	requestHost = strings.Trim(requestHost, "[]")
	if requestHost == "" || wildcardHost(requestHost) {
		return "", fmt.Errorf("request host cannot be used for reconnection")
	}
	oldHost, _, oldErr := net.SplitHostPort(oldAddress)
	if !wildcardHost(targetHost) && (!oldEnabled || oldErr != nil || !strings.EqualFold(targetHost, oldHost)) {
		requestHost = targetHost
	}
	scheme := "http"
	if secure {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(requestHost, targetPort), nil
}

func wildcardHost(host string) bool {
	host = strings.Trim(host, "[]")
	return host == "" || host == "0.0.0.0" || host == "::"
}
