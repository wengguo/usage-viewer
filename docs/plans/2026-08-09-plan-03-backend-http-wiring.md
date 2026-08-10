# Module 3: HTTP Wiring (`internal/httpapi` + `cmd/viewer/main.go`)

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). Requires Modules 1 and 2 complete (imports `LeaderboardRepository`, `SelfLookupRepository`, and their `search` package dependencies).

**Every change in this module was applied to the real codebase, built, vetted, unit-tested, and then smoke-tested over real HTTP against a real Postgres container end to end** (built the actual `dist/sub2api-usage-viewer` binary, started it, hit `/api/leaderboard` and `/api/self-lookup` with `curl`) before being written into this plan. This module deliberately does **not** touch frontend routing (`serveAsset`'s HTML file switch) — that belongs to Module 4, which needs to add the embedded assets first. Adding page routes here before the assets exist would just produce silent 404s with no way to notice until Module 4.

---

### Task 1: Extend `handler` with a non-breaking constructor chain

**Files:**
- Modify: `internal/httpapi/handler.go`

**Step 1: Write the failing test**

Add to `internal/httpapi/handler_test.go` (append; don't touch existing tests in the file):

```go
func TestNewFullHandlerRoutesAllFourAPIEndpoints(t *testing.T) {
	application := NewFullHandler(nil, nil, nil, nil)
	for _, path := range []string{"/api/search", "/api/key-usage", "/api/leaderboard", "/api/self-lookup"} {
		response := serveRequest(application, http.MethodPost, path, `{}`, "application/json", "")
		if response.Code == http.StatusNotFound {
			t.Fatalf("path %q is not routed", path)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestNewFullHandlerRoutesAllFourAPIEndpoints -v`
Expected: `FAIL` — compile error (`NewFullHandler` undefined).

**Step 3: Write minimal implementation**

In `internal/httpapi/handler.go`, replace the `handler` struct and its two constructors:

```go
type handler struct {
	search      SearchService
	dailyUsage  DailyUsageService
	leaderboard LeaderboardService
	selfLookup  SelfLookupService
}

func NewHandler(searchService SearchService) http.Handler {
	return NewHandlerWithDailyUsage(searchService, nil)
}

func NewHandlerWithDailyUsage(searchService SearchService, dailyUsageService DailyUsageService) http.Handler {
	return NewFullHandler(searchService, dailyUsageService, nil, nil)
}

func NewFullHandler(searchService SearchService, dailyUsageService DailyUsageService, leaderboardService LeaderboardService, selfLookupService SelfLookupService) http.Handler {
	application := &handler{search: searchService, dailyUsage: dailyUsageService, leaderboard: leaderboardService, selfLookup: selfLookupService}
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", serveHealth)
	mux.HandleFunc("/readyz", serveHealth)
	mux.HandleFunc("/api/search", application.serveSearch)
	mux.HandleFunc("/api/key-usage", application.serveDailyUsage)
	mux.HandleFunc("/api/leaderboard", application.serveLeaderboard)
	mux.HandleFunc("/api/self-lookup", application.serveSelfLookup)
	mux.HandleFunc("/", application.serveAsset)
	return securityHeaders(mux)
}
```

`NewHandler` and `NewHandlerWithDailyUsage` keep their existing signatures and behavior — every existing caller and test (`NewHandler(service)`, `NewHandler(nil)`, `NewHandlerWithDailyUsage(nil, service)`, etc.) keeps compiling and passing unchanged. They just delegate to `NewFullHandler` with the two new services as `nil`, which `serveLeaderboard`/`serveSelfLookup` already treat as "service unavailable" (see Task 2 and Task 3 — this is the same nil-check pattern `serveSearch` and `serveDailyUsage` already use for their own services).

`serveLeaderboard` and `serveSelfLookup` don't exist yet — Task 2 and Task 3 add them. This step alone won't compile in isolation; that's expected. Do Task 2 and Task 3 before re-running the test.

**Step 4: Run test to verify it passes**

Once Task 2 and Task 3 are also done:
Run: `go test ./internal/httpapi/... -run TestNewFullHandlerRoutesAllFourAPIEndpoints -v`
Expected: `PASS`.

Then run every existing httpapi test to confirm nothing broke:
Run: `go test ./internal/httpapi/... -v`
Expected: every test — old and new — passes. This was verified for real: all pre-existing tests (`TestSearchEndpoint...`, `TestSecurityHeaders...`, `TestEmbeddedApplicationAssets...`, `TestHealthEndpoints...`, etc.) pass unchanged against the extended `handler` struct.

**Step 5: Commit**

```bash
git add internal/httpapi/handler.go internal/httpapi/handler_test.go
git commit -m "feat: add NewFullHandler wiring leaderboard and self-lookup services"
```

---

### Task 2: `POST /api/leaderboard`

**Files:**
- Create: `internal/httpapi/leaderboard.go`
- Create: `internal/httpapi/leaderboard_test.go`

**Step 1: Write the failing test**

```go
// internal/httpapi/leaderboard_test.go
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestLeaderboardEndpoint -v`
Expected: `FAIL` — compile error (`LeaderboardService`, `serveLeaderboard` undefined).

**Step 3: Write minimal implementation**

```go
// internal/httpapi/leaderboard.go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

type LeaderboardService interface {
	Top(context.Context, int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error)
}

type leaderboardRequest struct {
	Limit int `json:"limit"`
}

type leaderboardResponse struct {
	Limit   int                                                    `json:"limit"`
	Windows map[search.LeaderboardWindow][]search.LeaderboardEntry `json:"windows"`
}

func (application *handler) serveLeaderboard(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for leaderboard requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for leaderboard requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload leaderboardRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}

	limit, err := search.ValidateLimit(payload.Limit)
	if err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_LIMIT", "Limit must be 10, 20, or 50.")
		return
	}
	if application.leaderboard == nil {
		writeError(response, http.StatusServiceUnavailable, "LEADERBOARD_UNAVAILABLE", "Leaderboard is temporarily unavailable.")
		return
	}

	windows, err := application.leaderboard.Top(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "LEADERBOARD_UNAVAILABLE", "Leaderboard is temporarily unavailable.")
		return
	}
	writeJSON(response, http.StatusOK, leaderboardResponse{Limit: limit, Windows: windows})
}
```

Run `gofmt -l internal/httpapi/leaderboard.go` after creating this file — the struct field alignment in `leaderboardResponse` needs `gofmt -w` to line up correctly (this was caught and fixed during verification; the code above already reflects the gofmt-corrected alignment).

**Step 4: Run test to verify it passes**

Run: `go test ./internal/httpapi/... -run TestLeaderboardEndpoint -v`
Expected: `PASS` for all three test functions.

**Step 5: Commit**

```bash
gofmt -w internal/httpapi/leaderboard.go
git add internal/httpapi/leaderboard.go internal/httpapi/leaderboard_test.go
git commit -m "feat: add POST /api/leaderboard endpoint"
```

---

### Task 3: `POST /api/self-lookup`

**Files:**
- Create: `internal/httpapi/selflookup.go`
- Create: `internal/httpapi/selflookup_test.go`

**Step 1: Write the failing test**

```go
// internal/httpapi/selflookup_test.go
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestSelfLookupEndpoint -v`
Expected: `FAIL` — compile error (`SelfLookupService`, `serveSelfLookup` undefined).

**Step 3: Write minimal implementation**

```go
// internal/httpapi/selflookup.go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

// SelfLookupService returns the caller's own key detail and today's cost for
// an exact key-or-name credential match, plus the internal id (never
// returned to the client) so the handler can attach daily usage in the same
// request without a second round trip through the credential match.
type SelfLookupService interface {
	Lookup(ctx context.Context, credential string) (result search.SelfResult, id int64, ok bool, err error)
}

const selfLookupDailyUsageDays = 30

type selfLookupRequest struct {
	Credential string `json:"credential"`
}

func (application *handler) serveSelfLookup(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for self-lookup requests.")
		return
	}
	if !sameOrigin(request) {
		writeError(response, http.StatusForbidden, "ORIGIN_REJECTED", "The request origin is not allowed.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED", "Use application/json for self-lookup requests.")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maximumSearchBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload selfLookupRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(response, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
		return
	}

	if application.selfLookup == nil {
		writeError(response, http.StatusServiceUnavailable, "SELF_LOOKUP_UNAVAILABLE", "Self-lookup is temporarily unavailable.")
		return
	}

	result, id, ok, err := application.selfLookup.Lookup(request.Context(), payload.Credential)
	if err != nil {
		// Validation failures and infrastructure failures both surface as
		// this one generic response — the caller must not be able to tell
		// "your credential was malformed" apart from "nothing matched" or
		// "the database is down", since any of those distinctions could be
		// used to probe for valid keys.
		writeError(response, http.StatusServiceUnavailable, "SELF_LOOKUP_UNAVAILABLE", "Self-lookup is temporarily unavailable.")
		return
	}
	if !ok {
		writeError(response, http.StatusNotFound, "NOT_FOUND", "No key matched the provided credential.")
		return
	}

	result.DailyUsage = []search.DailyUsagePoint{}
	if application.dailyUsage != nil {
		if points, dailyErr := application.dailyUsage.DailyUsage(request.Context(), id, selfLookupDailyUsageDays); dailyErr == nil {
			result.DailyUsage = points
		}
		// A daily-usage failure here does not fail the whole lookup — the
		// caller still gets their key detail and today's cost; the chart
		// just renders as empty. The id used for this call is discarded
		// after this line and never reaches the response body.
	}
	writeJSON(response, http.StatusOK, result)
}
```

Note `Lookup`'s second return value (`id`) is used here — but only internally, to call `application.dailyUsage.DailyUsage(ctx, id, 30)` and attach the result to `result.DailyUsage` before the response is written. It is never itself written to the response body; the design intentionally keeps `SelfLookupRepository.Lookup` (Module 2, Task 3) unaware of daily usage — that concern lives entirely in this handler, which already had a `DailyUsageService` dependency from Module 3's Task 1 constructor change. `result.DailyUsage` defaults to an empty (not nil) slice so the JSON field is always `[]`, never `null`, even if `application.dailyUsage` is unset or the daily-usage call fails.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/httpapi/... -run TestSelfLookupEndpoint -v`
Expected: `PASS` for all seven test functions.

**Step 5: Commit**

```bash
git add internal/httpapi/selflookup.go internal/httpapi/selflookup_test.go
git commit -m "feat: add POST /api/self-lookup endpoint"
```

---

### Task 4: Wire real repositories in `cmd/viewer/main.go`

**Files:**
- Modify: `cmd/viewer/main.go`

**Step 1: Write the failing test**

`main_test.go`'s existing `TestRunWithLoadsOnceWiresDependenciesAndLogsOneFailure` already calls `deps.NewHandler(&pgxpool.Pool{}, config.Config{DatabaseQueryTimeout: time.Second})` and asserts on `deps.NewHandler == nil` — it doesn't need a new test to exercise this wiring; the existing test already exercises `NewHandler` as a whole. Instead, verify manually that `main.go` compiles and its existing test still passes after wiring in the two new repositories — there's no new *behavior* here to unit test in isolation (it's four lines of dependency construction), just a compile-time and integration-level checkpoint.

Run: `go build ./cmd/viewer/... 2>&1`
Expected (before Step 2): `FAIL` if Tasks 1–3 aren't done, otherwise this already compiles since nothing here has changed yet — the "failing" state is really "hasn't been changed yet," which is why Step 2 below is the meaningful change.

**Step 2: Apply the change**

In `cmd/viewer/main.go`, inside `runWith`'s `dependencies := app.Dependencies{...}` literal, replace the `NewHandler` field:

```go
		NewHandler: func(pool *pgxpool.Pool, cfg config.Config) http.Handler {
			resolver := concurrency.NewResolver(cfg)
			searchRepository := postgres.NewSearchRepository(pool, cfg.DatabaseQueryTimeout, resolver)
			dailyRepository := postgres.NewDailyUsageRepository(pool, cfg.DatabaseQueryTimeout)
			leaderboardRepository := postgres.NewLeaderboardRepository(pool, cfg.DatabaseQueryTimeout)
			selfLookupRepository := postgres.NewSelfLookupRepository(pool, cfg.DatabaseQueryTimeout)
			return httpapi.NewFullHandler(searchRepository, dailyRepository, leaderboardRepository, selfLookupRepository)
		},
```

This is the only change to `main.go`. No other lines move.

**Step 3: Run test to verify it passes**

Run: `go build ./... && go vet ./...`
Expected: no errors.

Run: `go test ./cmd/viewer/... -v`
Expected: `PASS` — `TestRunWithLoadsOnceWiresDependenciesAndLogsOneFailure` and every other existing test in the package pass unchanged.

**Step 4: Full-suite checkpoint**

Run: `go test ./... -count=1`
Expected: `ok` for every package.

**Step 5: Commit**

```bash
git add cmd/viewer/main.go
git commit -m "feat: wire leaderboard and self-lookup repositories into the server"
```

---

### Task 5: End-to-end smoke test (manual, not automated)

This step was already performed once while writing this plan, against a real Postgres container with a real built binary — do it again here as a checkpoint, not because there's doubt it works, but because "the plan says it works" and "the binary on disk right now works" are different claims, and only the second one matters before moving to Module 4.

**Step 1: Build and start against a throwaway Postgres**

```bash
docker run -d --name plan03-smoke -e POSTGRES_USER=sub2api -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=sub2api -p 15988:5432 postgres:16-alpine
# wait for readiness:
until docker exec plan03-smoke pg_isready -U sub2api >/dev/null 2>&1; do sleep 1; done
docker exec -i plan03-smoke psql -U sub2api -d sub2api <<'SQL'
CREATE TABLE public.groups (id bigint PRIMARY KEY, name varchar NOT NULL);
CREATE TABLE public.api_keys (
  id bigint PRIMARY KEY, key varchar NOT NULL, name varchar NOT NULL,
  group_id bigint, quota numeric NOT NULL, quota_used numeric NOT NULL,
  last_used_at timestamptz, expires_at timestamptz, status varchar NOT NULL,
  created_at timestamptz NOT NULL, deleted_at timestamptz
);
CREATE TABLE public.usage_logs (
  id bigint PRIMARY KEY, api_key_id bigint, actual_cost numeric NOT NULL, created_at timestamptz NOT NULL
);
INSERT INTO public.groups VALUES (1, 'demo-group');
INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at) VALUES
  (1, 'sk-e2e-verify-alpha001', 'e2e-alpha', 1, 100, 0, 'active', now()),
  (2, 'sk-e2e-verify-beta0002', 'e2e-beta', 1, 100, 0, 'active', now());
INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
  (1, 1, 4.20, now()),
  (2, 2, 9.90, now());
SQL

go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
DATABASE_HOST=127.0.0.1 DATABASE_PORT=15988 DATABASE_USER=sub2api DATABASE_PASSWORD=testpass DATABASE_DBNAME=sub2api DATABASE_SSLMODE=disable \
  ./dist/sub2api-usage-viewer &
sleep 1.5
```

Expected startup log: `{"...,"event":"ready","address_class":"loopback"}`.

If instead you see `{"...,"event":"failure","code":"UV-SERVER-001",...}`, check for a stale process already bound to port 8081 first (`lsof -iTCP:8081 -sTCP:LISTEN -n -P`) — that's what caused this exact failure while writing this plan, not a code defect. Kill the stale process and retry.

**Step 2: Hit the new endpoints**

```bash
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/leaderboard \
  -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{}'

curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/self-lookup \
  -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"credential":"sk-e2e-verify-alpha001"}'
```

Expected: leaderboard response with `e2e-beta` ranked above `e2e-alpha` (9.90 > 4.20) in every window, both keys masked (`sk-e2e***...`, never the full value); self-lookup response with `"keyMasked":"sk-e2e***pha001"` and `"todayCost":"4.20"`.

**Step 3: Tear down**

```bash
kill %1  # or the PID printed by `jobs`/`ps aux | grep sub2api-usage-viewer`
docker rm -f plan03-smoke
git checkout -- dist/sub2api-usage-viewer  # discard the rebuilt binary; it is a committed artifact, not meant to be rebuilt casually
```

---

### Module 3 checkpoint

```bash
go build ./... && go vet ./...
gofmt -l internal/httpapi/*.go cmd/viewer/*.go  # only pre-existing, unrelated files (if any) should print
go test ./... -count=1
```
Expected: everything passes; `gofmt -l` prints nothing for any file this module touched or created.

Next: [`2026-08-09-plan-04-frontend-foundations.md`](2026-08-09-plan-04-frontend-foundations.md)
