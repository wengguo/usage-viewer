package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/web"
)

const maximumSearchBodyBytes int64 = 1024

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

type SearchService interface {
	Search(context.Context, search.Query) (search.Results, error)
}

type handler struct {
	search      SearchService
	dailyUsage  DailyUsageService
	leaderboard LeaderboardService
	selfLookup  SelfLookupService
	sessions    *sessionStore
}

func NewHandler(searchService SearchService) http.Handler {
	return NewHandlerWithDailyUsage(searchService, nil)
}

func NewHandlerWithDailyUsage(searchService SearchService, dailyUsageService DailyUsageService) http.Handler {
	return NewFullHandler(searchService, dailyUsageService, nil, nil)
}

func NewFullHandler(searchService SearchService, dailyUsageService DailyUsageService, leaderboardService LeaderboardService, selfLookupService SelfLookupService) http.Handler {
	application := &handler{search: searchService, dailyUsage: dailyUsageService, leaderboard: leaderboardService, selfLookup: selfLookupService, sessions: newSessionStore()}
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", serveHealth)
	mux.HandleFunc("/readyz", serveHealth)
	mux.HandleFunc("/api/login", application.serveLogin)
	mux.HandleFunc("/api/logout", application.serveLogout)
	mux.HandleFunc("/api/auth/status", application.serveAuthStatus)
	mux.HandleFunc("/api/search", application.requireAuthAPI(application.serveSearch))
	mux.HandleFunc("/api/key-usage", application.requireAuthAPI(application.serveDailyUsage))
	mux.HandleFunc("/api/leaderboard", application.requireAuthAPI(application.serveLeaderboard))
	mux.HandleFunc("/api/self-lookup", application.serveSelfLookup)
	mux.HandleFunc("/", application.serveAsset)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(response, request)
	})
}

type searchRequest struct {
	TargetType    search.TargetType     `json:"targetType"`
	Query         string                `json:"query"`
	Page          *int                  `json:"page"`
	SortBy        *search.SortBy        `json:"sortBy"`
	SortDirection *search.SortDirection `json:"sortDirection"`
}

type keySearchResponse struct {
	TargetType search.TargetType  `json:"targetType"`
	Results    []search.KeyResult `json:"results"`
	Page       int                `json:"page"`
	PageSize   int                `json:"pageSize"`
	Total      int64              `json:"total"`
}

type errorEnvelope struct {
	Error publicError `json:"error"`
}

type publicError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (application *handler) serveSearch(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for search requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for search requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload searchRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The search request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The search request is invalid.")
		return
	}

	searchRequest := search.Request{TargetType: payload.TargetType, Query: payload.Query}
	if payload.Page != nil {
		searchRequest.Page = *payload.Page
		if searchRequest.Page < 1 {
			writeError(response, http.StatusBadRequest, "INVALID_SEARCH", "The search criteria are invalid.")
			return
		}
	}
	if payload.SortBy != nil {
		searchRequest.SortBy = *payload.SortBy
	}
	if payload.SortDirection != nil {
		searchRequest.SortDirection = *payload.SortDirection
	}
	query, err := search.Validate(searchRequest)
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_SEARCH", "The search criteria are invalid.")
		return
	}
	if application.search == nil {
		writeError(response, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "Search is temporarily unavailable.")
		return
	}
	results, err := application.search.Search(request.Context(), query)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "Search is temporarily unavailable.")
		return
	}

	keyResults := results.Keys
	if keyResults == nil {
		keyResults = []search.KeyResult{}
	}
	writeJSON(response, http.StatusOK, keySearchResponse{TargetType: search.TargetKey, Results: keyResults, Page: query.Page(), PageSize: search.PageSize, Total: results.Total})
}

// protectedPages maps a page path to true when it requires an authenticated
// session to view. Every other path in serveAsset's switch is public.
var protectedPages = map[string]bool{
	"/keys":        true,
	"/leaderboard": true,
}

func (application *handler) serveAsset(response http.ResponseWriter, request *http.Request) {
	name := ""
	switch request.URL.Path {
	case "/", "/self":
		name = "self.html"
	case "/app.css":
		name = "app.css"
	case "/app.js":
		name = "app.js"
	case "/credentials", "/credentials.html":
		name = "credentials.html"
	case "/credentials.css":
		name = "credentials.css"
	case "/credentials.js":
		name = "credentials.js"
	case "/favicon.svg":
		name = "favicon.svg"
	case "/keys":
		name = "index.html"
	case "/leaderboard":
		name = "leaderboard.html"
	case "/leaderboard.js":
		name = "leaderboard.js"
	case "/login":
		name = "login.html"
	case "/login.js":
		name = "login.js"
	case "/nav.js":
		name = "nav.js"
	case "/self.js":
		name = "self.js"
	case "/theme-init.js":
		name = "theme-init.js"
	case "/theme.js":
		name = "theme.js"
	default:
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if protectedPages[request.URL.Path] && !application.isAuthenticated(request) {
		http.Redirect(response, request, "/login?next="+url.QueryEscape(request.URL.Path), http.StatusFound)
		return
	}
	asset, err := web.Read(name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", asset.ContentType)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(asset.Content)
}

func sameOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	expectedScheme := "http"
	if request.TLS != nil {
		expectedScheme = "https"
	}
	// Check X-Forwarded-Proto header when behind a reverse proxy
	if forwardedProto := request.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		expectedScheme = forwardedProto
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme) && strings.EqualFold(parsed.Host, request.Host)
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, errorEnvelope{Error: publicError{Code: code, Message: message}})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
