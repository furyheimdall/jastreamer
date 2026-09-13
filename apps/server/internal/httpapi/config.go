package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
)

func configRevision(value config.Config) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func roots(value config.Config) []library.Root {
	result := make([]library.Root, 0, len(value.LibraryRoots))
	for _, root := range value.LibraryRoots {
		result = append(result, library.Root{ID: root.ID, Name: root.Name, Path: root.Path})
	}
	return result
}

func restartRequired(active, saved config.Config) bool {
	return !reflect.DeepEqual(active, saved)
}

func (service *server) activeConfigValue() config.Config {
	service.activeMu.RLock()
	defer service.activeMu.RUnlock()
	return service.activeConfig
}

func (service *server) setActiveRoots(value []config.Root) {
	service.activeMu.Lock()
	if value == nil {
		service.activeConfig.LibraryRoots = nil
	} else {
		service.activeConfig.LibraryRoots = append(make([]config.Root, 0, len(value)), value...)
	}
	service.activeMu.Unlock()
}

func (service *server) configResponse(value config.Config) map[string]any {
	result := map[string]any{
		"config":            value,
		"revision":          configRevision(value),
		"restart_required":  restartRequired(service.activeConfigValue(), value),
		"restart_supported": service.options.Restart != nil && service.options.Restart.Prepare != nil && service.options.Restart.Commit != nil,
		"runtime_id":        service.options.RuntimeID,
	}
	if service.options.RestartError != "" {
		result["restart_error"] = service.options.RestartError
	}
	return result
}

func (service *server) getConfig(w http.ResponseWriter, r *http.Request) {
	service.configMu.Lock()
	defer service.configMu.Unlock()
	value, err := config.Load(service.options.ConfigPath)
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, service.configResponse(value))
}

func (service *server) putConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config   config.Config `json:"config"`
		Revision string        `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := config.Validate(body.Config); err != nil {
		writeError(w, fault.New(400, "INVALID_CONFIG", err.Error()))
		return
	}
	service.configMu.Lock()
	defer service.configMu.Unlock()
	previous, err := config.Load(service.options.ConfigPath)
	if err != nil {
		writeError(w, err)
		return
	}
	if body.Revision == "" || body.Revision != configRevision(previous) {
		writeError(w, fault.New(409, "CONFIG_CONFLICT", "설정이 다른 곳에서 변경되었습니다. 새로 불러오세요."))
		return
	}
	if err = config.Save(service.options.ConfigPath, body.Config); err != nil {
		writeError(w, err)
		return
	}
	if !reflect.DeepEqual(service.activeConfigValue().LibraryRoots, body.Config.LibraryRoots) {
		if err = service.options.Library.SetRoots(roots(body.Config)); err != nil {
			if restoreErr := config.Save(service.options.ConfigPath, previous); restoreErr != nil {
				writeError(w, fault.New(500, "CONFIG_RESTART_REQUIRED", "설정은 저장되었으나 라이브러리에 적용하지 못했습니다. 서버를 재시작하세요."))
				return
			}
			writeError(w, err)
			return
		}
	}
	service.setActiveRoots(body.Config.LibraryRoots)
	service.options.Events.Publish("config")
	reply(w, http.StatusOK, service.configResponse(body.Config))
}
