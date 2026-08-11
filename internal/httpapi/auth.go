package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Login credentials default to a placeholder account and can be overridden
// once at startup via ConfigureAuth (see internal/config for the env vars
// that feed it). They are not safe to change concurrently with request
// handling, so ConfigureAuth must run before the server starts serving.
var (
	authUsername = "admin"
	authPassword = "usage-viewer-2026"
)

// ConfigureAuth overrides the login username/password. Empty arguments leave
// the corresponding default in place. Call once during startup, before the
// HTTP server begins accepting requests.
func ConfigureAuth(username, password string) {
	if strings.TrimSpace(username) != "" {
		authUsername = username
	}
	if password != "" {
		authPassword = password
	}
}

const (
	sessionCookieName = "session"
	sessionDuration   = 24 * time.Hour
	sessionTokenBytes = 32
)

// sessionStore is an in-memory, process-lifetime session table. It holds no
// persistence by design, matching the rest of this codebase: a restart logs
// every operator out.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]time.Time)}
}

func (store *sessionStore) create() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	store.mu.Lock()
	store.sessions[token] = time.Now().Add(sessionDuration)
	store.mu.Unlock()
	return token, nil
}

func (store *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	expiry, ok := store.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(store.sessions, token)
		return false
	}
	return true
}

func (store *sessionStore) revoke(token string) {
	store.mu.Lock()
	delete(store.sessions, token)
	store.mu.Unlock()
}

func (application *handler) isAuthenticated(request *http.Request) bool {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return application.sessions.valid(cookie.Value)
}

// requireAuthAPI protects a JSON API route: an unauthenticated caller gets a
// generic 401 envelope, never a redirect.
func (application *handler) requireAuthAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !application.isAuthenticated(request) {
			writeError(response, http.StatusUnauthorized, "UNAUTHENTICATED", "Login is required.")
			return
		}
		next(response, request)
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (application *handler) serveLogin(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for login requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for login requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload loginRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The login request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The login request is invalid.")
		return
	}

	usernameMatch := subtle.ConstantTimeCompare([]byte(payload.Username), []byte(authUsername)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(payload.Password), []byte(authPassword)) == 1
	if !usernameMatch || !passwordMatch {
		writeError(response, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Username or password is incorrect.")
		return
	}

	token, err := application.sessions.create()
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "LOGIN_UNAVAILABLE", "Login is temporarily unavailable.")
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (application *handler) serveLogout(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for logout requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		application.sessions.revoke(cookie.Value)
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(response, http.StatusOK, map[string]bool{"ok": true})
}

func (application *handler) serveAuthStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET for auth status requests.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"authenticated": application.isAuthenticated(request)})
}
