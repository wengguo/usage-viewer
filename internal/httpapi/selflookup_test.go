package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

type fakeSelfLookupService struct {
	result     search.SelfResult
	id         int64
	ok         bool
	err        error
	calls      int
	credential string
}

func (service *fakeSelfLookupService) Lookup(_ context.Context, credential string) (search.SelfResult, int64, bool, error) {
	service.calls++
	service.credential = credential
	return service.result, service.id, service.ok, service.err
}

type fakeDailyUsageServiceForSelfLookup struct {
	points []search.DailyUsagePoint
	err    error
	calls  int
	keyID  int64
	days   int
}

func (service *fakeDailyUsageServiceForSelfLookup) DailyUsage(_ context.Context, keyID int64, days int) ([]search.DailyUsagePoint, error) {
	service.calls++
	service.keyID = keyID
	service.days = days
	return service.points, service.err
}

func TestSelfLookupEndpointReturnsResultWithDailyUsageOnMatch(t *testing.T) {
	selfService := &fakeSelfLookupService{
		ok: true,
		id: 42,
		result: search.SelfResult{
			KeyMasked: "sk-abc***xyz123", Name: "my-key", GroupName: "default",
			Quota: "100.00", QuotaUsed: "12.50", Status: "active", ExpiresAt: "", TodayCost: "1.25",
		},
	}
	dailyService := &fakeDailyUsageServiceForSelfLookup{points: []search.DailyUsagePoint{
		{Date: "2026-08-08", ActualCost: "1.10"},
		{Date: "2026-08-09", ActualCost: "1.25"},
	}}
	application := NewFullHandler(nil, dailyService, nil, selfService)
	response := serveRequest(application, http.MethodPost, "/api/self-lookup", `{"credential":"sk-abc123xyz123"}`, "application/json", "")
	if response.Code != http.StatusOK || selfService.calls != 1 || dailyService.calls != 1 {
		t.Fatalf("status=%d selfCalls=%d dailyCalls=%d body=%q", response.Code, selfService.calls, dailyService.calls, response.Body.String())
	}
	if dailyService.keyID != 42 || dailyService.days != 30 {
		t.Fatalf("daily usage called with keyID=%d days=%d, want 42, 30", dailyService.keyID, dailyService.days)
	}
	wantBody := `{"keyMasked":"sk-abc***xyz123","name":"my-key","groupName":"default","quota":"100.00","quotaUsed":"12.50","status":"active","expiresAt":"","todayCost":"1.25","dailyUsage":[{"date":"2026-08-08","actualCost":"1.10"},{"date":"2026-08-09","actualCost":"1.25"}]}` + "\n"
	if response.Body.String() != wantBody {
		t.Fatalf("body=%q", response.Body.String())
	}
	assertSecurityHeaders(t, response.Result())
	if strings.Contains(response.Body.String(), "42") {
		t.Fatalf("response leaked the internal id: %q", response.Body.String())
	}
}

func TestSelfLookupEndpointReturnsEmptyDailyUsageWhenServiceMissing(t *testing.T) {
	selfService := &fakeSelfLookupService{ok: true, id: 7, result: search.SelfResult{KeyMasked: "sk-abc***xyz123", Name: "my-key", Status: "active"}}
	application := NewFullHandler(nil, nil, nil, selfService)
	response := serveRequest(application, http.MethodPost, "/api/self-lookup", `{"credential":"anything"}`, "application/json", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	wantBody := `{"keyMasked":"sk-abc***xyz123","name":"my-key","groupName":"","quota":"","quotaUsed":"","status":"active","expiresAt":"","todayCost":"","dailyUsage":[]}` + "\n"
	if response.Body.String() != wantBody {
		t.Fatalf("body=%q", response.Body.String())
	}
}

func TestSelfLookupEndpointReturnsNotFoundOnMiss(t *testing.T) {
	service := &fakeSelfLookupService{ok: false}
	application := NewFullHandler(nil, nil, nil, service)
	response := serveRequest(application, http.MethodPost, "/api/self-lookup", `{"credential":"does-not-exist"}`, "application/json", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Body.String() != `{"error":{"code":"NOT_FOUND","message":"No key matched the provided credential."}}`+"\n" {
		t.Fatalf("body=%q", response.Body.String())
	}
}

// A service-level failure (e.g. a database error, or the repository's own
// input validation rejecting the credential) must produce the exact same
// response as any other failure — SELF_LOOKUP_UNAVAILABLE, never a code that
// would let a caller distinguish "your credential was malformed" from
// "nothing matched" from "the database is down". Any such distinction could
// be used to probe for valid keys.
func TestSelfLookupEndpointTreatsServiceFailureAsGenericUnavailable(t *testing.T) {
	service := &fakeSelfLookupService{err: errors.New("self-lookup-service-failure-secret-sentinel")}
	application := NewFullHandler(nil, nil, nil, service)
	response := serveRequest(application, http.MethodPost, "/api/self-lookup", `{"credential":"anything"}`, "application/json", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	wantBody := `{"error":{"code":"SELF_LOOKUP_UNAVAILABLE","message":"Self-lookup is temporarily unavailable."}}` + "\n"
	if response.Body.String() != wantBody {
		t.Fatalf("body=%q", response.Body.String())
	}
}

func TestSelfLookupEndpointRejectsInvalidRequests(t *testing.T) {
	const sentinel = "self-lookup-secret-sentinel"
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
		{name: "cross origin", method: http.MethodPost, body: `{"credential":"x"}`, contentType: "application/json", origin: "https://other.invalid", wantStatus: http.StatusForbidden},
		{name: "malformed", method: http.MethodPost, body: `{`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: `{"credential":"x","extra":"` + sentinel + `"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeSelfLookupService{}
			application := NewFullHandler(nil, nil, nil, service)
			response := serveRequest(application, tt.method, "/api/self-lookup", tt.body, tt.contentType, tt.origin)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
		})
	}
}

func TestSelfLookupEndpointWithoutServiceIsUnavailable(t *testing.T) {
	application := NewFullHandler(nil, nil, nil, nil)
	response := serveRequest(application, http.MethodPost, "/api/self-lookup", `{"credential":"x"}`, "application/json", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
