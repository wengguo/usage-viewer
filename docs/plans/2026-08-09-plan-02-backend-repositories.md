# Module 2: Backend Repositories (`internal/postgres`)

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). Requires Module 1 complete (imports `search.LeaderboardEntry`, `search.LeaderboardWindow`, `search.SelfResult`, `search.MaskKey`).

**Prerequisite already done:** the integration test harness (`test_harness_integration_test.go`) and two of its consumers (`preflight_integration_test.go`, `search_integration_test.go`) were out of date against the current `api_keys`/`groups`/`usage_logs` schema, and `catalog.go`'s admission SQL had a real bug (`pg_catalog.unnest(...)` doesn't exist as a catalog function — the multi-array zip form only works unqualified). Both were found and fixed as separate commits (`9d4f20c`, `896670d`) before this module was written. If you're executing this plan on a checkout that doesn't have those commits, stop and apply them first — `go test -tags integration ./internal/postgres/...` must pass cleanly before you add anything here.

**Every file and every assertion value in this module was written to disk and run against a real Postgres container (via testcontainers) before being committed to this plan.** One assertion in the original draft was wrong (see Task 2) — it was caught by actually running the test, not by re-reading the SQL, which is the entire point of verifying before writing plans down instead of after.

---

### Task 1: Leaderboard SQL and repository — nil-guard unit test

**Files:**
- Create: `internal/postgres/leaderboard.go`
- Create: `internal/postgres/leaderboard_test.go`

**Step 1: Write the failing test**

```go
// internal/postgres/leaderboard_test.go
package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestLeaderboardRepositoryRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name       string
		repository *LeaderboardRepository
		limit      int
	}{
		{name: "nil repository", repository: nil, limit: 10},
		{name: "nil pool", repository: NewLeaderboardRepository(nil, time.Second), limit: 10},
		{name: "invalid timeout", repository: NewLeaderboardRepository(nil, 0), limit: 10},
		{name: "invalid limit", repository: NewLeaderboardRepository(nil, time.Second), limit: 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotErr := tt.repository.Top(context.Background(), tt.limit)
			if !errors.Is(gotErr, ErrLeaderboardUnavailable) {
				t.Fatalf("Top() error = %v, want ErrLeaderboardUnavailable", gotErr)
			}
		})
	}
}

func TestLeaderboardWindowSQLIsStaticBoundedAndExcludesDeletedKeys(t *testing.T) {
	for _, window := range search.AllLeaderboardWindows() {
		statement, ok := leaderboardSQLByWindow[window]
		if !ok {
			t.Fatalf("no SQL registered for window %q", window)
		}
		normalized := strings.ToLower(statement)
		for _, required := range []string{
			"select ",
			"from public.usage_logs",
			"join public.api_keys",
			"left join public.groups",
			"api_key.deleted_at is null",
			"group by",
			"order by",
			"limit $",
		} {
			if !strings.Contains(normalized, required) {
				t.Fatalf("window %q SQL missing %q: %s", window, required, statement)
			}
		}
		for _, forbidden := range []string{"select *", " insert ", " update ", " delete "} {
			if strings.Contains(" "+normalized+" ", forbidden) {
				t.Fatalf("window %q SQL contains forbidden fragment %q", window, forbidden)
			}
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres/... -run TestLeaderboard -v`
Expected: `FAIL` — compile error (`LeaderboardRepository`, `NewLeaderboardRepository`, `ErrLeaderboardUnavailable`, `leaderboardSQLByWindow` undefined).

**Step 3: Write minimal implementation**

```go
// internal/postgres/leaderboard.go
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

// leaderboardSQLPrefix computes the total actual_cost per non-deleted key
// within a window whose start bound is bound as $1, ranked highest cost
// first with a stable id-descending tiebreak. $2 is the caller-supplied
// Top N limit. All four windows share this exact statement — only the
// start bound they're called with differs.
const leaderboardSQLPrefix = `
SELECT api_key.id, api_key.key, api_key.name, COALESCE(grp.name, '')::text,
       pg_catalog.sum(ul.actual_cost)::text
FROM public.usage_logs AS ul
JOIN public.api_keys AS api_key ON api_key.id = ul.api_key_id
LEFT JOIN public.groups AS grp ON grp.id = api_key.group_id
WHERE ul.created_at >= $1
  AND api_key.deleted_at IS NULL
GROUP BY api_key.id, api_key.key, api_key.name, grp.name
ORDER BY pg_catalog.sum(ul.actual_cost) DESC, api_key.id DESC
LIMIT $2
`

var leaderboardSQLByWindow = map[search.LeaderboardWindow]string{
	search.Window1Day:  leaderboardSQLPrefix,
	search.Window3Day:  leaderboardSQLPrefix,
	search.Window7Day:  leaderboardSQLPrefix,
	search.Window30Day: leaderboardSQLPrefix,
}

var ErrLeaderboardUnavailable = errors.New("leaderboard is temporarily unavailable")

type LeaderboardRepository struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

func NewLeaderboardRepository(pool *pgxpool.Pool, timeout time.Duration) *LeaderboardRepository {
	return &LeaderboardRepository{pool: pool, timeout: timeout}
}

// Top returns the Top N highest-cost keys for each of the four fixed
// windows, keyed by window. Every entry's key is pre-masked — callers never
// see the raw api_keys.key value.
func (repository *LeaderboardRepository) Top(ctx context.Context, limit int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error) {
	if repository == nil || repository.pool == nil || repository.timeout <= 0 {
		return nil, ErrLeaderboardUnavailable
	}
	normalizedLimit, err := search.ValidateLimit(limit)
	if err != nil {
		return nil, ErrLeaderboardUnavailable
	}

	now := time.Now().UTC()
	results := make(map[search.LeaderboardWindow][]search.LeaderboardEntry, len(leaderboardSQLByWindow))
	err = withReadOnlyTx(ctx, repository.pool, repository.timeout, func(queryCtx context.Context, tx pgx.Tx) error {
		for _, window := range search.AllLeaderboardWindows() {
			start := windowStart(now, window)
			entries, err := queryLeaderboardWindow(queryCtx, tx, window, start, normalizedLimit)
			if err != nil {
				return err
			}
			results[window] = entries
		}
		return nil
	})
	if err != nil {
		return nil, ErrLeaderboardUnavailable
	}
	return results, nil
}

// windowStart mirrors the today/thirtyDaysAgo pattern already used by
// search.go's Search method: truncate to midnight UTC, then step back
// (days-1) so a 1-day window starts at today's midnight (i.e. "today so
// far"), not yesterday's.
func windowStart(now time.Time, window search.LeaderboardWindow) time.Time {
	days := search.WindowDays(window)
	return now.Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
}

func queryLeaderboardWindow(ctx context.Context, tx pgx.Tx, window search.LeaderboardWindow, start time.Time, limit int) ([]search.LeaderboardEntry, error) {
	statement := leaderboardSQLByWindow[window]
	rows, err := tx.Query(ctx, statement, start, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]search.LeaderboardEntry, 0, limit)
	for rows.Next() {
		var id int64
		var rawKey, name, groupName, actualCost string
		if err := rows.Scan(&id, &rawKey, &name, &groupName, &actualCost); err != nil {
			return nil, err
		}
		entries = append(entries, search.LeaderboardEntry{
			Rank:       len(entries) + 1,
			KeyMasked:  search.MaskKey(rawKey),
			Name:       name,
			GroupName:  groupName,
			ActualCost: actualCost,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres/... -run TestLeaderboard -v`
Expected: `PASS` for both test functions.

**Step 5: Commit**

```bash
git add internal/postgres/leaderboard.go internal/postgres/leaderboard_test.go
git commit -m "feat: add LeaderboardRepository with masked-key window queries"
```

---

### Task 2: Leaderboard integration test against real Postgres

**Files:**
- Create: `internal/postgres/leaderboard_integration_test.go`

**Step 1: Write the failing test**

```go
// internal/postgres/leaderboard_integration_test.go
//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9601, 'Leaderboard Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at, deleted_at) VALUES
            (9701, 'sk-leaderboard-alpha-01', 'Leaderboard Alpha', 9601, 100, 0, 'active', pg_catalog.now(), NULL),
            (9702, 'sk-leaderboard-beta-002', 'Leaderboard Beta', 9601, 100, 0, 'active', pg_catalog.now(), NULL),
            (9703, 'sk-leaderboard-deleted1', 'Leaderboard Deleted', 9601, 100, 0, 'active', pg_catalog.now(), pg_catalog.now())`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9801, 9701, 5.00, pg_catalog.now()),
            (9802, 9701, 3.00, pg_catalog.now() - interval '2 days'),
            (9803, 9702, 10.00, pg_catalog.now()),
            (9804, 9703, 999.00, pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id IN (9801, 9802, 9803, 9804)`,
			`DELETE FROM public.api_keys WHERE id IN (9701, 9702, 9703)`,
			`DELETE FROM public.groups WHERE id = 9601`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))
	repository := NewLeaderboardRepository(pool, cfg.DatabaseQueryTimeout)

	windows, err := repository.Top(context.Background(), 10)
	require.NoError(t, err)

	// Alpha: $5 today + $3 two days ago = $8 total. Beta: $10 today only.
	// Deleted key: $999, must never surface in any window.
	oneDay := windows[search.Window1Day]
	require.Len(t, oneDay, 2, "1-day window excludes the 2-day-old usage row and the deleted key")
	require.Equal(t, "Leaderboard Beta", oneDay[0].Name)
	require.Equal(t, "10.00", oneDay[0].ActualCost)
	require.Equal(t, "Leaderboard Alpha", oneDay[1].Name)
	require.Equal(t, "5.00", oneDay[1].ActualCost)
	require.NotContains(t, oneDay[0].KeyMasked, "leaderboard")

	// 3-day window pulls in alpha's second usage row, but beta's single $10
	// row still outranks alpha's summed $8 — rank order does not flip.
	threeDay := windows[search.Window3Day]
	require.Len(t, threeDay, 2)
	require.Equal(t, "Leaderboard Beta", threeDay[0].Name)
	require.Equal(t, "10.00", threeDay[0].ActualCost)
	require.Equal(t, "Leaderboard Alpha", threeDay[1].Name)
	require.Equal(t, "8.00", threeDay[1].ActualCost, "3-day window sums both alpha usage rows (5.00 + 3.00)")

	for _, window := range windows {
		for _, entry := range window {
			require.NotEqual(t, "Leaderboard Deleted", entry.Name, "deleted key must never appear")
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/postgres/... -run TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted -v -timeout 60s`
Expected: `FAIL` — compile error if Task 1 wasn't done yet.

**Step 3: (No implementation step — Task 1 already provides it)**

This test exercises code already written in Task 1. If it fails for a reason other than "Task 1 doesn't exist yet," stop and debug — do not adjust the assertions to match unexpected output without first understanding why the output differs. (This exact thing happened while writing this plan: the first draft asserted alpha would lead the 3-day window, which was an arithmetic mistake in the assertion, not a bug in the query — running the test against real data caught it immediately.)

**Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/postgres/... -run TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted -v -timeout 60s -count=1`
Expected: `PASS`. Also run the full integration suite to confirm nothing else broke:
Run: `go test -tags integration ./internal/postgres/... -timeout 180s -count=1`
Expected: `ok`.

**Step 5: Commit**

```bash
git add internal/postgres/leaderboard_integration_test.go
git commit -m "test: verify LeaderboardRepository against real PostgreSQL"
```

---

### Task 3: Self-lookup SQL and repository — nil-guard unit test

**Files:**
- Create: `internal/postgres/selflookup.go`
- Create: `internal/postgres/selflookup_test.go`

**Step 1: Write the failing test**

```go
// internal/postgres/selflookup_test.go
package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelfLookupRepositoryRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name       string
		repository *SelfLookupRepository
		credential string
	}{
		{name: "nil repository", repository: nil, credential: "sk-example"},
		{name: "nil pool", repository: NewSelfLookupRepository(nil, time.Second), credential: "sk-example"},
		{name: "invalid timeout", repository: NewSelfLookupRepository(nil, 0), credential: "sk-example"},
		{name: "empty credential", repository: NewSelfLookupRepository(nil, time.Second), credential: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, gotErr := tt.repository.Lookup(context.Background(), tt.credential)
			if !errors.Is(gotErr, ErrSelfLookupUnavailable) {
				t.Fatalf("Lookup() error = %v, want ErrSelfLookupUnavailable", gotErr)
			}
		})
	}
}

func TestSelfLookupSQLIsStaticBoundedAndExactMatchOnly(t *testing.T) {
	normalized := strings.ToLower(selfLookupSQL)
	for _, required := range []string{
		"select ",
		"from public.api_keys",
		"left join public.groups",
		"deleted_at is null",
		"api_key.key = $1",
		"api_key.name = $1",
		" or ",
		"order by",
		"limit 1",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("self-lookup SQL missing %q: %s", required, selfLookupSQL)
		}
	}
	for _, forbidden := range []string{"select *", " insert ", " update ", " delete ", "ilike"} {
		if strings.Contains(" "+normalized+" ", forbidden) {
			t.Fatalf("self-lookup SQL contains forbidden fragment %q (must be exact match, not fuzzy)", forbidden)
		}
	}
}
```

Note `Lookup` returns four values (`result, id, ok, err`) — the nil-guard test above discards the first three with `_, _, _,`. This matters because it's easy to miscount and write `_, gotErr :=` instead, which won't compile.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/postgres/... -run TestSelfLookup -v`
Expected: `FAIL` — compile error (`SelfLookupRepository`, `NewSelfLookupRepository`, `ErrSelfLookupUnavailable`, `selfLookupSQL` undefined).

**Step 3: Write minimal implementation**

```go
// internal/postgres/selflookup.go
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

// selfLookupSQL finds the single non-deleted key whose key value or name
// exactly equals the caller-supplied credential. Ties (duplicate names) are
// broken by the lowest id — the earliest-created match — so results are
// deterministic across repeated calls. Not ILIKE: a self-lookup credential
// must be proven exactly, never fuzzy-matched.
const selfLookupSQL = `
SELECT api_key.id, api_key.key, api_key.name, COALESCE(grp.name, '')::text,
       COALESCE(api_key.quota, 0)::text, COALESCE(api_key.quota_used, 0)::text,
       api_key.status, COALESCE(api_key.expires_at::text, '')
FROM public.api_keys AS api_key
LEFT JOIN public.groups AS grp ON grp.id = api_key.group_id
WHERE api_key.deleted_at IS NULL
  AND (api_key.key = $1 OR api_key.name = $1)
ORDER BY api_key.id ASC
LIMIT 1
`

const todayCostSQL = `
SELECT COALESCE(pg_catalog.sum(ul.actual_cost), 0)::text
FROM public.usage_logs AS ul
WHERE ul.api_key_id = $1
  AND ul.created_at >= $2
`

var ErrSelfLookupUnavailable = errors.New("self-lookup is temporarily unavailable")

type SelfLookupRepository struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

func NewSelfLookupRepository(pool *pgxpool.Pool, timeout time.Duration) *SelfLookupRepository {
	return &SelfLookupRepository{pool: pool, timeout: timeout}
}

// Lookup returns the caller's own key detail (with the key masked) and
// today's cost, or ok=false if no key matched. The internal id is never
// returned to httpapi callers beyond this function — it exists only so the
// caller can fetch daily usage within the same request without a second
// round trip through the credential match.
func (repository *SelfLookupRepository) Lookup(ctx context.Context, credential string) (result search.SelfResult, id int64, ok bool, err error) {
	if repository == nil || repository.pool == nil || repository.timeout <= 0 {
		return search.SelfResult{}, 0, false, ErrSelfLookupUnavailable
	}
	value, validateErr := search.ValidateCredential(credential)
	if validateErr != nil {
		return search.SelfResult{}, 0, false, ErrSelfLookupUnavailable
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	err = withReadOnlyTx(ctx, repository.pool, repository.timeout, func(queryCtx context.Context, tx pgx.Tx) error {
		var rawKey string
		row := tx.QueryRow(queryCtx, selfLookupSQL, value)
		scanErr := row.Scan(&id, &rawKey, &result.Name, &result.GroupName, &result.Quota, &result.QuotaUsed, &result.Status, &result.ExpiresAt)
		if scanErr != nil {
			if scanErr == pgx.ErrNoRows {
				ok = false
				return nil
			}
			return scanErr
		}
		result.KeyMasked = search.MaskKey(rawKey)
		ok = true

		var todayCost string
		costErr := tx.QueryRow(queryCtx, todayCostSQL, id, today).Scan(&todayCost)
		if costErr != nil {
			return costErr
		}
		result.TodayCost = todayCost
		return nil
	})
	if err != nil {
		return search.SelfResult{}, 0, false, ErrSelfLookupUnavailable
	}
	return result, id, ok, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/postgres/... -run TestSelfLookup -v`
Expected: `PASS` for both test functions.

**Step 5: Commit**

```bash
git add internal/postgres/selflookup.go internal/postgres/selflookup_test.go
git commit -m "feat: add SelfLookupRepository with exact-match masked-key lookup"
```

---

### Task 4: Self-lookup integration test against real Postgres

**Files:**
- Create: `internal/postgres/selflookup_integration_test.go`

**Step 1: Write the failing test**

```go
// internal/postgres/selflookup_integration_test.go
//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelfLookupRepositoryExactMatchesKeyOrNameWithStableTiebreak(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9901, 'Self Lookup Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at, expires_at) VALUES
            (9911, 'sk-self-lookup-first-01', 'shared-name', 9901, 100, 25, 'active', pg_catalog.now(), NULL),
            (9912, 'sk-self-lookup-second-2', 'shared-name', 9901, 100, 10, 'active', pg_catalog.now(), NULL),
            (9913, 'sk-self-lookup-deleted01', 'deleted-self-lookup', 9901, 100, 0, 'active', pg_catalog.now(), NULL)`,
		`UPDATE public.api_keys SET deleted_at = pg_catalog.now() WHERE id = 9913`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9921, 9911, 2.50, pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id = 9921`,
			`DELETE FROM public.api_keys WHERE id IN (9911, 9912, 9913)`,
			`DELETE FROM public.groups WHERE id = 9901`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))
	repository := NewSelfLookupRepository(pool, cfg.DatabaseQueryTimeout)

	result, id, ok, err := repository.Lookup(context.Background(), "sk-self-lookup-first-01")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(9911), id)
	require.Equal(t, "2.50", result.TodayCost)
	require.Equal(t, "25", result.QuotaUsed)
	require.NotContains(t, result.KeyMasked, "self-lookup-first")

	result, id, ok, err = repository.Lookup(context.Background(), "shared-name")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(9911), id, "duplicate name resolves to the lowest id, not 9912")

	_, _, ok, err = repository.Lookup(context.Background(), "sk-self-lookup-deleted01")
	require.NoError(t, err)
	require.False(t, ok, "deleted key must not be found")

	_, _, ok, err = repository.Lookup(context.Background(), "does-not-exist-sentinel")
	require.NoError(t, err)
	require.False(t, ok)
}
```

**Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/postgres/... -run TestSelfLookupRepositoryExactMatchesKeyOrNameWithStableTiebreak -v -timeout 60s`
Expected: `FAIL` — compile error if Task 3 isn't done yet.

**Step 3: (No implementation step — Task 3 already provides it)**

**Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/postgres/... -run TestSelfLookupRepositoryExactMatchesKeyOrNameWithStableTiebreak -v -timeout 60s -count=1`
Expected: `PASS`.

Then run the full integration suite one more time:
Run: `go test -tags integration ./internal/postgres/... -timeout 180s -count=1`
Expected: `ok`.

**Step 5: Commit**

```bash
git add internal/postgres/selflookup_integration_test.go
git commit -m "test: verify SelfLookupRepository against real PostgreSQL"
```

---

### Module 2 checkpoint

```bash
go build ./... && go vet ./...
go test ./... -count=1
go test -tags integration ./internal/postgres/... -timeout 180s -count=1
```
Expected: everything passes, including the pre-existing suites. Run the integration command **twice** to confirm determinism (no test order or timing flakiness) before moving on.

Next: [`2026-08-09-plan-03-backend-http-wiring.md`](2026-08-09-plan-03-backend-http-wiring.md)
