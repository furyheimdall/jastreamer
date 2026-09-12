package httpapi

import (
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

func (service *server) guard(next http.Handler) http.Handler {
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, host := range service.options.TrustedHosts {
		hosts[strings.ToLower(host)] = true
	}
	var networks []netip.Prefix
	for _, raw := range service.options.Config.Network.AllowedCIDRs {
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			networks = append(networks, prefix)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "microphone=(), camera=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		peer := remoteIP(r)
		if !peer.IsValid() || (!peer.IsPrivate() && !peer.IsLoopback() && !peer.IsLinkLocalUnicast()) {
			writeError(w, fault.New(403, "PRIVATE_NETWORK_REQUIRED", "사설망에서만 접속할 수 있습니다."))
			return
		}
		if len(networks) > 0 && !peer.IsLoopback() {
			allowed := false
			for _, prefix := range networks {
				if prefix.Contains(peer) {
					allowed = true
					break
				}
			}
			if !allowed {
				writeError(w, fault.New(403, "NETWORK_NOT_ALLOWED", "허용되지 않은 네트워크입니다."))
				return
			}
		}
		host := r.Host
		if value, _, err := net.SplitHostPort(host); err == nil {
			host = value
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if !hosts[host] {
			writeError(w, fault.New(403, "HOST_NOT_ALLOWED", "서버의 실제 주소로 접속하세요."))
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			parsed, err := url.Parse(origin)
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if err != nil || parsed.Scheme != scheme || !strings.EqualFold(parsed.Host, r.Host) || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
				writeError(w, fault.New(403, "ORIGIN_NOT_ALLOWED", "같은 서버의 Web 화면에서 요청해야 합니다."))
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, fault.New(403, "ORIGIN_NOT_ALLOWED", "외부 사이트의 요청은 허용되지 않습니다."))
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Method == "OPTIONS" {
				writeError(w, fault.New(403, "ORIGIN_NOT_ALLOWED", "교차 출처 API는 지원하지 않습니다."))
				return
			}
			if r.Header.Get("X-Jastreamer-Request") != "web" {
				writeError(w, fault.New(403, "REQUEST_HEADER_REQUIRED", "올바른 Web 요청이 필요합니다."))
				return
			}
			if r.Method != "DELETE" {
				kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || kind != "application/json" {
					writeError(w, fault.New(415, "JSON_REQUIRED", "application/json 요청이 필요합니다."))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func remoteIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, _ := netip.ParseAddr(host)
	return address.Unmap()
}

type loginWindow struct {
	start    time.Time
	attempts int
}
type loginThrottle struct {
	mu      sync.Mutex
	windows map[string]loginWindow
}

func (limiter *loginThrottle) allow(key string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	if limiter.windows == nil {
		limiter.windows = make(map[string]loginWindow)
	}
	if len(limiter.windows) > 1024 {
		for address, window := range limiter.windows {
			if now.Sub(window.start) >= time.Minute {
				delete(limiter.windows, address)
			}
		}
	}
	window, exists := limiter.windows[key]
	if !exists && len(limiter.windows) >= 4096 {
		return false
	}
	if window.start.IsZero() || now.Sub(window.start) >= time.Minute {
		window = loginWindow{start: now}
	}
	window.attempts++
	limiter.windows[key] = window
	return window.attempts <= 10
}
