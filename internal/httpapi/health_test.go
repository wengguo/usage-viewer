package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthEndpointsReturnFixedNonSensitiveStatus(t *testing.T) {
	handler := NewHandler(nil)
	for _, path := range []string{"/livez", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path+"?dsn=postgres-secret-sentinel", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			result := response.Result()
			defer result.Body.Close()
			body, err := io.ReadAll(result.Body)
			if err != nil {
				t.Fatal(err)
			}
			if result.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", result.StatusCode)
			}
			if result.Header.Get("Content-Type") != "application/json" || result.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("headers = %#v", result.Header)
			}
			if string(body) != `{"status":"ok"}` {
				t.Fatalf("body = %q", body)
			}
			if strings.Contains(string(body), "secret-sentinel") {
				t.Fatal("health response disclosed request data")
			}
		})
	}
}

func TestHealthHandlerRejectsEveryOtherMethodAndRoute(t *testing.T) {
	handler := NewHandler(nil)
	tests := []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodHead, path: "/livez", status: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/readyz", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/health", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/livez/extra", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		request := httptest.NewRequest(tt.method, tt.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tt.status {
			t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, response.Code, tt.status)
		}
		if response.Body.Len() != 0 && strings.Contains(response.Body.String(), "postgres") {
			t.Fatalf("rejection disclosed runtime data: %q", response.Body.String())
		}
	}
}
