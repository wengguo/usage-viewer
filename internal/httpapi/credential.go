package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/creds"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/postgres"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/web"
)

type credentialHandler struct {
	cfg       *config.Config
	saveCreds func(string, creds.Entry) error
}

// NewCredentialHandler returns an HTTP handler that serves the credential
// collection form and the /api/connect endpoint.
func NewCredentialHandler(cfg *config.Config, saveCreds func(string, creds.Entry) error) http.Handler {
	if cfg == nil || saveCreds == nil {
		return nil
	}
	h := &credentialHandler{cfg: cfg, saveCreds: saveCreds}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/connect", h.serveConnect)
	mux.HandleFunc("/api/creds/status", h.serveCredsStatus)
	mux.HandleFunc("/", h.serveCredentialAsset)
	return securityHeaders(mux)
}

type connectRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

type connectResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (h *credentialHandler) serveConnect(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}

	var payload connectRequest
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 2048)).Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
		return
	}
	if payload.Host == "" || payload.User == "" || payload.DBName == "" {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "Host, user, and dbname are required.")
		return
	}
	if payload.Port <= 0 || payload.Port > 65535 {
		payload.Port = 5432
	}
	if payload.SSLMode == "" {
		payload.SSLMode = "disable"
	}

	entry := creds.Entry{
		Host:     payload.Host,
		Port:     payload.Port,
		User:     payload.User,
		Password: payload.Password,
		DBName:   payload.DBName,
		SSLMode:  payload.SSLMode,
	}

	// Test the connection, trying localhost fallbacks when the supplied host
	// is a Docker-internal service name (e.g. "postgres") that does not
	// resolve from the host machine.
	var lastErr error
	for _, candidate := range creds.ConnectionCandidates(entry) {
		ok := tryConnect(request, h.cfg, candidate)
		if !ok {
			lastErr = errors.New("connection failed for " + candidate.Host)
			continue
		}
		// Save the entry with the host that actually worked.
		if err := h.saveCreds(h.cfg.DataDir, candidate); err != nil {
			writeJSON(response, http.StatusOK, connectResponse{Success: false, Message: "Failed to save credentials: " + err.Error()})
			return
		}
		writeJSON(response, http.StatusOK, connectResponse{Success: true, Message: "Connected and saved."})
		return
	}
	if lastErr == nil {
		lastErr = errors.New("connection failed")
	}
	writeJSON(response, http.StatusOK, connectResponse{Success: false, Message: "Connection failed: " + lastErr.Error()})
}

type credsStatusResponse struct {
	HasSavedCreds bool   `json:"hasSavedCreds"`
	Source        string `json:"source"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user,omitempty"`
	DBName        string `json:"dbname,omitempty"`
	SSLMode       string `json:"sslmode,omitempty"`
}

func (h *credentialHandler) serveCredsStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET.")
		return
	}

	status := credsStatusResponse{}
	if _, err := creds.LoadSaved(h.cfg.DataDir); err == nil {
		status.HasSavedCreds = true
	}
	if h.cfg.CredentialSource != "" {
		status.Source = h.cfg.CredentialSource
	}
	// Include discovered non-secret fields so the form can be pre-filled.
	if entry, err := creds.LoadSaved(h.cfg.DataDir); err == nil {
		status.Host, status.Port, status.User, status.DBName, status.SSLMode = entry.Host, entry.Port, entry.User, entry.DBName, entry.SSLMode
	} else if h.cfg.DatabaseURL != "" {
		if entry, err := creds.FromURL(h.cfg.DatabaseURL); err == nil {
			status.Host, status.Port, status.User, status.DBName, status.SSLMode = entry.Host, entry.Port, entry.User, entry.DBName, entry.SSLMode
		}
	}
	writeJSON(response, http.StatusOK, status)
}

// tryConnect attempts to open a pool and run light preflight, returning true
// on success. The pool is always closed.
func tryConnect(request *http.Request, cfg *config.Config, entry creds.Entry) bool {
	testCfg := *cfg
	testCfg.DatabaseURL = entry.DSN()

	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()

	pool, err := postgres.OpenLightPool(ctx, testCfg)
	if err != nil || pool == nil {
		return false
	}
	defer pool.Close()
	return postgres.RunLightPreflight(ctx, pool, testCfg) == nil
}

func (h *credentialHandler) serveCredentialAsset(response http.ResponseWriter, request *http.Request) {
	name := ""
	switch request.URL.Path {
	case "/", "/credentials.html":
		name = "credentials.html"
	case "/credentials.css":
		name = "credentials.css"
	case "/credentials.js":
		name = "credentials.js"
	default:
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	asset, err := web.Read(name)
	if err != nil {
		// Fall back to index.html for the root path and other paths.
		if request.URL.Path == "/" {
			asset, err = web.Read("index.html")
			if err != nil {
				http.NotFound(response, request)
				return
			}
		} else {
			http.NotFound(response, request)
			return
		}
	}
	response.Header().Set("Content-Type", asset.ContentType)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(asset.Content)
}
