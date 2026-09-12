package httpapi

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func (service *server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		writeError(w, fault.New(405, "METHOD_NOT_ALLOWED", "이 경로는 읽기만 지원합니다."))
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if name == "flutter_service_worker.js" {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "HEAD" {
			_, _ = w.Write([]byte("self.addEventListener('install',()=>self.skipWaiting());self.addEventListener('activate',event=>event.waitUntil(self.registration.unregister().then(()=>self.clients.claim())));"))
		}
		return
	}
	if strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
		http.NotFound(w, r)
		return
	}
	info, err := fs.Stat(service.options.UI, name)
	if err != nil || info.IsDir() {
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}
	if name == "index.html" {
		if len(service.index) == 0 {
			writeError(w, fault.New(503, "WEB_BUILD_MISSING", "Web 빌드가 포함되지 않았습니다."))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(service.index))
		return
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	service.assets.ServeHTTP(w, r)
}
