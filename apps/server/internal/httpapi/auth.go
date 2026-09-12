package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/fault"
)

const sessionCookie = "jastreamer_session"

func sessionID(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (service *server) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := service.options.Auth.Validate(r.Context(), sessionID(r)); err != nil {
			writeError(w, err)
			return
		}
		next(w, r)
	}
}

func (service *server) setupState(w http.ResponseWriter, r *http.Request) {
	required, err := service.options.Auth.NeedsSetup(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"required": required})
}

func (service *server) session(w http.ResponseWriter, r *http.Request) {
	id := sessionID(r)
	if id == "" {
		reply(w, 200, map[string]bool{"authenticated": false})
		return
	}
	user, err := service.options.Auth.Validate(r.Context(), id)
	if err != nil {
		var known *fault.Error
		if !errors.As(err, &known) || known.Status != http.StatusUnauthorized {
			writeError(w, err)
			return
		}
		expireCookie(w, r)
		reply(w, 200, map[string]bool{"authenticated": false})
		return
	}
	reply(w, 200, map[string]any{"authenticated": true, "user": user})
}

func (service *server) setup(w http.ResponseWriter, r *http.Request) { service.signIn(w, r, true) }
func (service *server) login(w http.ResponseWriter, r *http.Request) { service.signIn(w, r, false) }

func (service *server) signIn(w http.ResponseWriter, r *http.Request, setup bool) {
	if !service.throttle.allow(remoteIP(r).String()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, fault.New(429, "LOGIN_RATE_LIMIT", "잠시 기다린 뒤 다시 로그인하세요."))
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	var result auth.Session
	var err error
	if setup {
		result, err = service.options.Auth.Setup(r.Context(), body.Username, body.Password)
	} else {
		result, err = service.options.Auth.Login(r.Context(), body.Username, body.Password)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if previous := sessionID(r); previous != "" {
		if err = service.options.Auth.Logout(r.Context(), previous); err != nil {
			var known *fault.Error
			if !errors.As(err, &known) || known.Status != http.StatusUnauthorized {
				_ = service.options.Auth.Logout(r.Context(), result.ID)
				writeError(w, err)
				return
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: result.ID, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: result.ExpiresAt, MaxAge: int(time.Until(result.ExpiresAt).Seconds())})
	status := http.StatusOK
	if setup {
		status = http.StatusCreated
	}
	reply(w, status, map[string]any{"user": result.User})
}

func expireCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func (service *server) logout(w http.ResponseWriter, r *http.Request) {
	if err := service.options.Auth.Logout(r.Context(), sessionID(r)); err != nil {
		writeError(w, err)
		return
	}
	expireCookie(w, r)
	service.options.Events.Publish("session")
	reply(w, 204, nil)
}

func (service *server) password(w http.ResponseWriter, r *http.Request) {
	if !service.throttle.allow(remoteIP(r).String()) {
		writeError(w, fault.New(429, "LOGIN_RATE_LIMIT", "잠시 기다린 뒤 다시 시도하세요."))
		return
	}
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := service.options.Auth.ChangePassword(r.Context(), sessionID(r), body.Current, body.New); err != nil {
		writeError(w, err)
		return
	}
	expireCookie(w, r)
	service.options.Events.Publish("session")
	reply(w, 204, nil)
}
