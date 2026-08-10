package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestSearchEndpointReturnsOnlyKeyTargetSpecificSafeProjection(t *testing.T) {
	body := `{"targetType":"key","query":"weng"}`
	results := search.Results{Page: 1, PageSize: search.PageSize, Total: 1, Keys: []search.KeyResult{
		{ID: 17, Name: "wengguo", GroupName: "default", CurrentConcurrency: 2, TodayCost: "1.25", Total30dCost: "99.5", Quota: "5000", QuotaUsed: "10", LastUsedAt: "2026-08-04T00:00:00Z", Status: "active", CreatedAt: "2026-08-05T00:00:00Z"},
	}}
	wantBody := `{"targetType":"key","results":[{"id":17,"name":"wengguo","groupName":"default","currentConcurrency":2,"todayCost":"1.25","total30dCost":"99.5","quota":"5000","quotaUsed":"10","lastUsedAt":"2026-08-04T00:00:00Z","expiresAt":"","status":"active","createdAt":"2026-08-05T00:00:00Z"}],"page":1,"pageSize":20,"total":1}` + "\n"

	service := &fakeSearchService{results: results}
	response := serveRequest(NewHandler(service), http.MethodPost, "/api/search", body, "application/json", "")
	if response.Code != http.StatusOK || response.Body.String() != wantBody || service.calls != 1 {
		t.Fatalf("status=%d body=%q calls=%d", response.Code, response.Body.String(), service.calls)
	}
	assertSecurityHeaders(t, response.Result())
	if strings.Contains(strings.ToLower(response.Body.String()), "password") {
		t.Fatalf("response disclosed forbidden field: %q", response.Body.String())
	}
}

func TestSearchEndpointRejectsUnsafeRequestShapesBeforeService(t *testing.T) {
	const sentinel = "request-secret-sentinel"
	tests := []struct {
		name        string
		method      string
		body        string
		contentType string
		origin      string
		wantStatus  int
	}{
		{name: "method", method: http.MethodGet, contentType: "application/json", wantStatus: http.StatusMethodNotAllowed},
		{name: "missing content type", method: http.MethodPost, body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "wrong content type", method: http.MethodPost, body: `{}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		{name: "cross origin", method: http.MethodPost, body: `{}`, contentType: "application/json", origin: "https://other.invalid", wantStatus: http.StatusForbidden},
		{name: "malformed", method: http.MethodPost, body: `{`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: `{"targetType":"key","query":"ok","extra":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "trailing value", method: http.MethodPost, body: `{"targetType":"key","query":"ok"}{}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unsupported target", method: http.MethodPost, body: `{"targetType":"crafted","query":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "invalid page", method: http.MethodPost, body: `{"targetType":"key","page":0}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "invalid sort field", method: http.MethodPost, body: `{"targetType":"key","sortBy":"name"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "invalid sort direction", method: http.MethodPost, body: `{"targetType":"key","sortDirection":"sideways"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "oversized", method: http.MethodPost, body: `{"targetType":"key","query":"` + strings.Repeat("x", 1100) + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeSearchService{}
			response := serveRequest(NewHandler(service), tt.method, "/api/search", tt.body, tt.contentType, tt.origin)
			if response.Code != tt.wantStatus || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%q", response.Code, service.calls, response.Body.String())
			}
			if strings.Contains(response.Body.String(), sentinel) {
				t.Fatalf("rejection echoed request input: %q", response.Body.String())
			}
			assertSecurityHeaders(t, response.Result())
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS header must remain absent")
			}
		})
	}
}

func TestSearchEndpointDefaultsToFirstUnfilteredPageAndAcceptsPagination(t *testing.T) {
	service := &fakeSearchService{results: search.Results{Page: 2, PageSize: search.PageSize, Total: 23}}
	response := serveRequest(NewHandler(service), http.MethodPost, "/api/search", `{"targetType":"key","page":2,"sortBy":"todayCost","sortDirection":"asc"}`, "application/json", "")
	if response.Code != http.StatusOK || service.calls != 1 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
	if service.query.Mode() != search.QueryModeBrowse || service.query.Page() != 2 ||
		service.query.SortBy() != search.SortByTodayCost || service.query.SortDirection() != search.SortDirectionAscending {
		t.Fatalf("query = %#v", service.query)
	}
	if response.Body.String() != `{"targetType":"key","results":[],"page":2,"pageSize":20,"total":23}`+"\n" {
		t.Fatalf("body=%q", response.Body.String())
	}
}

func TestSearchEndpointAcceptsSameOriginAndSanitizesServiceFailures(t *testing.T) {
	service := &fakeSearchService{err: errors.New("database-raw-secret-sentinel")}
	response := serveRequest(
		NewHandler(service),
		http.MethodPost,
		"/api/search",
		`{"targetType":"key","query":"valid name"}`,
		"application/json; charset=utf-8",
		"http://example.com",
	)
	if response.Code != http.StatusServiceUnavailable || service.calls != 1 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
	if strings.Contains(response.Body.String(), "raw-secret") || response.Body.String() != `{"error":{"code":"SEARCH_UNAVAILABLE","message":"Search is temporarily unavailable."}}`+"\n" {
		t.Fatalf("unsafe error body: %q", response.Body.String())
	}
}

func TestSecurityHeadersCoverHealthAndNotFound(t *testing.T) {
	application := NewHandler(nil)
	for _, path := range []string{"/livez", "/missing"} {
		response := serveRequest(application, http.MethodGet, path, "", "", "")
		assertSecurityHeaders(t, response.Result())
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS header must remain absent")
		}
	}
}

func TestEmbeddedApplicationAssetsUseExactGetOnlyRoutes(t *testing.T) {
	application := NewHandler(nil)
	tests := []struct {
		method      string
		path        string
		wantStatus  int
		contentType string
		contains    string
	}{
		{method: http.MethodGet, path: "/", wantStatus: http.StatusOK, contentType: "text/html; charset=utf-8", contains: "用量查询"},
		{method: http.MethodGet, path: "/app.css", wantStatus: http.StatusOK, contentType: "text/css; charset=utf-8", contains: ".spinner"},
		{method: http.MethodGet, path: "/app.js", wantStatus: http.StatusOK, contentType: "text/javascript; charset=utf-8", contains: "fetch('/api/search'"},
		{method: http.MethodHead, path: "/", wantStatus: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound},
		{method: http.MethodGet, path: "/app.js/extra", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			response := serveRequest(application, tt.method, tt.path, "", "", "")
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if tt.contentType != "" && response.Header().Get("Content-Type") != tt.contentType {
				t.Fatalf("Content-Type=%q", response.Header().Get("Content-Type"))
			}
			if tt.contains != "" && !strings.Contains(response.Body.String(), tt.contains) {
				t.Fatalf("body does not contain %q", tt.contains)
			}
			assertSecurityHeaders(t, response.Result())
		})
	}
}

func TestSearchRequestContextReachesService(t *testing.T) {
	service := &fakeSearchService{}
	request := httptest.NewRequest(http.MethodPost, "http://example.com/api/search", strings.NewReader(`{"targetType":"key","query":"person"}`))
	request.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, request)
	if service.calls != 1 || !errors.Is(service.contextErr, context.Canceled) {
		t.Fatalf("calls=%d context error=%v", service.calls, service.contextErr)
	}
}

func serveRequest(handler http.Handler, method, path, body, contentType, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertSecurityHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	required := map[string]string{
		"Cache-Control":                "no-store",
		"Content-Security-Policy":      contentSecurityPolicy,
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cross-Origin-Opener-Policy":   "same-origin",
	}
	for name, want := range required {
		if got := response.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if response.Header.Get("Permissions-Policy") == "" {
		t.Error("Permissions-Policy is missing")
	}
}

type fakeSearchService struct {
	results    search.Results
	err        error
	calls      int
	contextErr error
	query      search.Query
}

func (service *fakeSearchService) Search(ctx context.Context, query search.Query) (search.Results, error) {
	service.calls++
	service.contextErr = ctx.Err()
	service.query = query
	return service.results, service.err
}

func TestNewFullHandlerRoutesAllFourAPIEndpoints(t *testing.T) {
	application := NewFullHandler(nil, nil, nil, nil)
	for _, path := range []string{"/api/search", "/api/key-usage", "/api/leaderboard", "/api/self-lookup"} {
		response := serveRequest(application, http.MethodPost, path, `{}`, "application/json", "")
		if response.Code == http.StatusNotFound {
			t.Fatalf("path %q is not routed", path)
		}
	}
}
