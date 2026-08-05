package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

type fakeDailyUsageService struct {
	items []search.DailyUsagePoint
	err   error
	calls int
}

func (service *fakeDailyUsageService) DailyUsage(_ context.Context, _ int64, _ int) ([]search.DailyUsagePoint, error) {
	service.calls++
	return service.items, service.err
}

func TestDailyUsageEndpointReturnsItems(t *testing.T) {
	service := &fakeDailyUsageService{items: []search.DailyUsagePoint{
		{Date: "2026-08-01", ActualCost: "1.25"},
		{Date: "2026-08-02", ActualCost: "2.50"},
	}}
	application := NewHandlerWithDailyUsage(nil, service)
	response := serveRequest(application, http.MethodPost, "/api/key-usage",
		`{"keyId":17,"days":30}`, "application/json", "")
	wantBody := `{"items":[{"date":"2026-08-01","actualCost":"1.25"},{"date":"2026-08-02","actualCost":"2.50"}],"days":30}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != wantBody || service.calls != 1 {
		t.Fatalf("status=%d body=%q calls=%d", response.Code, response.Body.String(), service.calls)
	}
	assertSecurityHeaders(t, response.Result())
}

func TestDailyUsageEndpointRejectsInvalidRequests(t *testing.T) {
	const sentinel = "daily-secret-sentinel"
	tests := []struct {
		name       string
		method     string
		body       string
		contentType string
		origin     string
		wantStatus int
	}{
		{name: "method", method: http.MethodGet, contentType: "application/json", wantStatus: http.StatusMethodNotAllowed},
		{name: "missing content type", method: http.MethodPost, body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "cross origin", method: http.MethodPost, body: `{"keyId":1,"days":30}`, contentType: "application/json", origin: "https://other.invalid", wantStatus: http.StatusForbidden},
		{name: "malformed", method: http.MethodPost, body: `{`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: `{"keyId":1,"days":30,"extra":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "zero key id", method: http.MethodPost, body: `{"keyId":0,"days":30}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "negative key id", method: http.MethodPost, body: `{"keyId":-1,"days":30}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "days too small", method: http.MethodPost, body: `{"keyId":1,"days":0}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "days too large", method: http.MethodPost, body: `{"keyId":1,"days":91}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeDailyUsageService{}
			application := NewHandlerWithDailyUsage(nil, service)
			response := serveRequest(application, tt.method, "/api/key-usage", tt.body, tt.contentType, tt.origin)
			if response.Code != tt.wantStatus || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%q", response.Code, service.calls, response.Body.String())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS header must remain absent")
			}
		})
	}
}

func TestDailyUsageEndpointSanitizesServiceFailures(t *testing.T) {
	service := &fakeDailyUsageService{err: errors.New("daily-db-raw-secret-sentinel")}
	application := NewHandlerWithDailyUsage(nil, service)
	response := serveRequest(application, http.MethodPost, "/api/key-usage",
		`{"keyId":17,"days":30}`, "application/json", "")
	if response.Code != http.StatusServiceUnavailable || service.calls != 1 {
		t.Fatalf("status=%d calls=%d", response.Code, service.calls)
	}
	if response.Body.String() != `{"error":{"code":"DAILY_USAGE_UNAVAILABLE","message":"Daily usage is temporarily unavailable."}}`+"\n" {
		t.Fatalf("unsafe error body: %q", response.Body.String())
	}
}
