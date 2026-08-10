package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

type fakeLeaderboardService struct {
	windows map[search.LeaderboardWindow][]search.LeaderboardEntry
	err     error
	calls   int
	limit   int
}

func (service *fakeLeaderboardService) Top(_ context.Context, limit int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error) {
	service.calls++
	service.limit = limit
	return service.windows, service.err
}

func TestLeaderboardEndpointReturnsWindowsWithDefaultLimit(t *testing.T) {
	service := &fakeLeaderboardService{windows: map[search.LeaderboardWindow][]search.LeaderboardEntry{
		search.Window1Day: {{Rank: 1, KeyMasked: "sk-abc***xyz123", Name: "top-key", GroupName: "default", ActualCost: "12.50"}},
	}}
	application := NewFullHandler(nil, nil, service, nil)
	response := serveRequest(application, http.MethodPost, "/api/leaderboard", `{}`, "application/json", "")
	if response.Code != http.StatusOK || service.calls != 1 || service.limit != 10 {
		t.Fatalf("status=%d calls=%d limit=%d body=%q", response.Code, service.calls, service.limit, response.Body.String())
	}
	wantBody := `{"limit":10,"windows":{"1d":[{"rank":1,"keyMasked":"sk-abc***xyz123","name":"top-key","groupName":"default","actualCost":"12.50"}]}}` + "\n"
	if response.Body.String() != wantBody {
		t.Fatalf("body=%q", response.Body.String())
	}
	assertSecurityHeaders(t, response.Result())
}

func TestLeaderboardEndpointRejectsInvalidRequests(t *testing.T) {
	const sentinel = "leaderboard-secret-sentinel"
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
		{name: "cross origin", method: http.MethodPost, body: `{}`, contentType: "application/json", origin: "https://other.invalid", wantStatus: http.StatusForbidden},
		{name: "malformed", method: http.MethodPost, body: `{`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: `{"limit":10,"extra":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "invalid limit", method: http.MethodPost, body: `{"limit":15}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeLeaderboardService{}
			application := NewFullHandler(nil, nil, service, nil)
			response := serveRequest(application, tt.method, "/api/leaderboard", tt.body, tt.contentType, tt.origin)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestLeaderboardEndpointWithoutServiceIsUnavailable(t *testing.T) {
	application := NewFullHandler(nil, nil, nil, nil)
	response := serveRequest(application, http.MethodPost, "/api/leaderboard", `{}`, "application/json", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
