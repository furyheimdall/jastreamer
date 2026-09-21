package httpapi

import (
	"errors"
	"log"
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
			operation, id := protectedOperation(r)
			status, reason := diagnosticFault(err)
			service.logRequestRejection(operation, id, status, reason)
			writeError(w, err)
			return
		}
		next(w, r)
	}
}

func (service *server) setupState(w http.ResponseWriter, r *http.Request) {
	required, err := service.options.Auth.NeedsSetup(r.Context())
	if err != nil {
		status, reason := diagnosticFault(err)
		service.logRequestRejection("setup_state", "", status, reason)
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
			status, reason := diagnosticFault(err)
			service.logRequestRejection("session", "", status, reason)
			writeError(w, err)
			return
		}
		service.logSessionValidationFailure(known.Status, http.StatusOK, known.Code)
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
		service.logRequestRejection(signInOperation(setup), "", http.StatusTooManyRequests, "LOGIN_RATE_LIMIT")
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
		status, reason := diagnosticFault(err)
		service.logRequestRejection(signInOperation(setup), "", status, reason)
		writeError(w, err)
		return
	}
	if previous := sessionID(r); previous != "" {
		if err = service.options.Auth.Logout(r.Context(), previous); err != nil {
			var known *fault.Error
			if !errors.As(err, &known) || known.Status != http.StatusUnauthorized {
				_ = service.options.Auth.Logout(r.Context(), result.ID)
				status, reason := diagnosticFault(err)
				service.logRequestRejection(signInOperation(setup), "", status, reason)
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
		status, reason := diagnosticFault(err)
		service.logRequestRejection("logout", "", status, reason)
		writeError(w, err)
		return
	}
	expireCookie(w, r)
	service.options.Events.Publish("session")
	reply(w, 204, nil)
}

func (service *server) password(w http.ResponseWriter, r *http.Request) {
	if !service.throttle.allow(remoteIP(r).String()) {
		service.logRequestRejection("change_password", "", http.StatusTooManyRequests, "LOGIN_RATE_LIMIT")
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
		status, reason := diagnosticFault(err)
		service.logRequestRejection("change_password", "", status, reason)
		writeError(w, err)
		return
	}
	expireCookie(w, r)
	service.options.Events.Publish("session")
	reply(w, 204, nil)
}

func diagnosticFault(err error) (int, string) {
	var known *fault.Error
	if errors.As(err, &known) {
		return known.Status, known.Code
	}
	return http.StatusInternalServerError, "INTERNAL_ERROR"
}

func signInOperation(setup bool) string {
	if setup {
		return "setup"
	}
	return "login"
}

func (service *server) logSessionValidationFailure(status, responseStatus int, reason string) {
	suppressed, allowed := service.diagnostics.allow("session_validation:" + reason)
	if !allowed {
		return
	}
	log.Printf("diagnostic component=httpapi event=session_validation_failed operation=%q status=%d response_status=%d reason=%q suppressed=%d", "session", status, responseStatus, reason, suppressed)
}
