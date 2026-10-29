# Leaderboard Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace four overlapping leaderboard scans with one conditional aggregate query and document a concurrent covering index so authenticated leaderboard requests complete with production P95 below two seconds.

**Architecture:** Keep the HTTP and domain contracts unchanged. `LeaderboardRepository.Top` will execute one static PostgreSQL statement inside the existing read-only transaction, map database-ranked rows into the existing four-window result map, and preserve generic error handling. A DBA-run SQL file creates the covering index independently of application startup.

**Tech Stack:** Go 1.24, pgx v5, PostgreSQL aggregate `FILTER`, window functions, Docker-backed PostgreSQL integration tests.

---

## File Structure

- Modify `internal/postgres/leaderboard.go`: replace the per-window SQL registry and loop with one query and one row mapper.
- Modify `internal/postgres/leaderboard_test.go`: enforce the single-scan static SQL contract.
- Modify `internal/postgres/leaderboard_integration_test.go`: verify exact four-window totals, tie-breaking, deleted-key exclusion, masking, and non-nil empty slices.
- Create `docs/leaderboard-index.sql`: DBA-operated concurrent covering index creation and verification statements.
- Modify `README.md`: document index installation, plan verification, and rollback commands.
- Modify `tests/operation_docs_test.go`: lock the operational index contract to the documentation.

### Task 1: Replace Four SQL Statements With One Conditional Aggregate

**Files:**
- Modify: `internal/postgres/leaderboard_test.go`
- Modify: `internal/postgres/leaderboard.go`

- [ ] **Step 1: Replace the old per-window SQL contract test with a failing single-query contract test**

Replace `TestLeaderboardWindowSQLIsStaticBoundedAndExcludesDeletedKeys` with:

```go
func TestLeaderboardSQLScansUsageOnceAndRanksAllWindows(t *testing.T) {
	normalized := strings.ToLower(leaderboardSQL)
	for _, required := range []string{
		"with key_costs as materialized",
		"from public.usage_logs",
		"filter (where ul.created_at >= $1)",
		"filter (where ul.created_at >= $2)",
		"filter (where ul.created_at >= $3)",
		"where ul.created_at >= $4",
		"cross join lateral",
		"partition by window_costs.window_name",
		"join public.api_keys",
		"left join public.groups",
		"api_key.deleted_at is null",
		"rank <= $5",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("leaderboard SQL missing %q: %s", required, leaderboardSQL)
		}
	}
	if got := strings.Count(normalized, "from public.usage_logs"); got != 1 {
		t.Fatalf("usage_logs scan count = %d, want 1: %s", got, leaderboardSQL)
	}
	for _, forbidden := range []string{"select *", " insert ", " update ", " delete "} {
		if strings.Contains(" "+normalized+" ", forbidden) {
			t.Fatalf("leaderboard SQL contains forbidden fragment %q", forbidden)
		}
	}
}
```

- [ ] **Step 2: Run the focused unit test and confirm the expected failure**

Run:

```bash
go test ./internal/postgres -run TestLeaderboardSQLScansUsageOnceAndRanksAllWindows -count=1
```

Expected: compilation fails because `leaderboardSQL` does not exist.

- [ ] **Step 3: Replace the SQL registry with one static conditional aggregate statement**

In `internal/postgres/leaderboard.go`, replace `leaderboardSQLPrefix` and `leaderboardSQLByWindow` with:

```go
const leaderboardSQL = `
WITH key_costs AS MATERIALIZED (
    SELECT
        ul.api_key_id,
        pg_catalog.sum(ul.actual_cost)
            FILTER (WHERE ul.created_at >= $1) AS cost_1d,
        pg_catalog.sum(ul.actual_cost)
            FILTER (WHERE ul.created_at >= $2) AS cost_3d,
        pg_catalog.sum(ul.actual_cost)
            FILTER (WHERE ul.created_at >= $3) AS cost_7d,
        pg_catalog.sum(ul.actual_cost) AS cost_30d
    FROM public.usage_logs AS ul
    WHERE ul.created_at >= $4
    GROUP BY ul.api_key_id
),
window_costs AS (
    SELECT key_costs.api_key_id, costs.window_name, costs.actual_cost
    FROM key_costs
    CROSS JOIN LATERAL (
        VALUES
            ('1d', key_costs.cost_1d),
            ('3d', key_costs.cost_3d),
            ('7d', key_costs.cost_7d),
            ('30d', key_costs.cost_30d)
    ) AS costs(window_name, actual_cost)
    WHERE costs.actual_cost IS NOT NULL
),
ranked AS (
    SELECT
        window_costs.window_name,
        api_key.id,
        api_key.key,
        api_key.name,
        COALESCE(grp.name, '')::text AS group_name,
        window_costs.actual_cost,
        pg_catalog.row_number() OVER (
            PARTITION BY window_costs.window_name
            ORDER BY window_costs.actual_cost DESC, api_key.id DESC
        ) AS rank
    FROM window_costs
    JOIN public.api_keys AS api_key ON api_key.id = window_costs.api_key_id
    LEFT JOIN public.groups AS grp ON grp.id = api_key.group_id
    WHERE api_key.deleted_at IS NULL
)
SELECT window_name, rank, id, key, name, group_name, actual_cost::text
FROM ranked
WHERE rank <= $5
ORDER BY window_name, rank
`
```

- [ ] **Step 4: Replace the serial loop with one query and defensive row mapping**

Replace `Top` and `queryLeaderboardWindow` with the following implementation, retaining `windowStart` unchanged:

```go
func (repository *LeaderboardRepository) Top(ctx context.Context, limit int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error) {
	if repository == nil || repository.pool == nil || repository.timeout <= 0 {
		return nil, ErrLeaderboardUnavailable
	}
	normalizedLimit, err := search.ValidateLimit(limit)
	if err != nil {
		return nil, ErrLeaderboardUnavailable
	}

	now := time.Now().UTC()
	var results map[search.LeaderboardWindow][]search.LeaderboardEntry
	err = withReadOnlyTx(ctx, repository.pool, repository.timeout, func(queryCtx context.Context, tx pgx.Tx) error {
		var queryErr error
		results, queryErr = queryLeaderboard(queryCtx, tx, now, normalizedLimit)
		return queryErr
	})
	if err != nil {
		return nil, ErrLeaderboardUnavailable
	}
	return results, nil
}

func queryLeaderboard(ctx context.Context, tx pgx.Tx, now time.Time, limit int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error) {
	results := make(map[search.LeaderboardWindow][]search.LeaderboardEntry, len(search.AllLeaderboardWindows()))
	for _, window := range search.AllLeaderboardWindows() {
		results[window] = []search.LeaderboardEntry{}
	}

	rows, err := tx.Query(
		ctx,
		leaderboardSQL,
		windowStart(now, search.Window1Day),
		windowStart(now, search.Window3Day),
		windowStart(now, search.Window7Day),
		windowStart(now, search.Window30Day),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var windowText string
		var rank int64
		var id int64
		var rawKey, name, groupName, actualCost string
		if err := rows.Scan(&windowText, &rank, &id, &rawKey, &name, &groupName, &actualCost); err != nil {
			return nil, err
		}
		window := search.LeaderboardWindow(windowText)
		if search.WindowDays(window) == 0 || rank < 1 || rank > int64(limit) {
			return nil, errors.New("leaderboard query returned invalid ranking data")
		}
		results[window] = append(results[window], search.LeaderboardEntry{
			Rank:       int(rank),
			KeyMasked:  search.MaskKey(rawKey),
			Name:       name,
			GroupName:  groupName,
			ActualCost: actualCost,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
```

The scanned `id` is intentionally retained in the fixed safe projection even though ordering has already been resolved by PostgreSQL. Passing it to `rows.Scan` counts as use; no extra assignment is needed.

- [ ] **Step 5: Format and run repository unit tests**

Run:

```bash
gofmt -w internal/postgres/leaderboard.go internal/postgres/leaderboard_test.go
go test ./internal/postgres -run 'TestLeaderboard' -count=1
```

Expected: all non-integration leaderboard tests pass.

- [ ] **Step 6: Commit the single-query repository change**

```bash
git add internal/postgres/leaderboard.go internal/postgres/leaderboard_test.go
git commit -m "perf: aggregate leaderboard windows in one query"
```

### Task 2: Strengthen Four-Window Integration Coverage

**Files:**
- Modify: `internal/postgres/leaderboard_integration_test.go`

- [ ] **Step 1: Expand the integration fixture to cover ties and all time windows**

Replace `TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted` with:

```go
func TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9601, 'Leaderboard Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at, deleted_at) VALUES
            (9701, 'sk-leaderboard-alpha-01', 'Leaderboard Alpha', 9601, 100, 0, 'active', pg_catalog.now(), NULL),
            (9702, 'sk-leaderboard-beta-002', 'Leaderboard Beta', 9601, 100, 0, 'active', pg_catalog.now(), NULL),
            (9703, 'sk-leaderboard-deleted1', 'Leaderboard Deleted', 9601, 100, 0, 'active', pg_catalog.now(), pg_catalog.now()),
            (9704, 'sk-leaderboard-tie-0004', 'Leaderboard Tie', 9601, 100, 0, 'active', pg_catalog.now(), NULL)`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9801, 9701, 5.00, pg_catalog.now()),
            (9802, 9701, 3.00, pg_catalog.now() - interval '2 days'),
            (9803, 9701, 4.00, pg_catalog.now() - interval '6 days'),
            (9804, 9701, 2.00, pg_catalog.now() - interval '29 days'),
            (9805, 9702, 10.00, pg_catalog.now()),
            (9806, 9703, 999.00, pg_catalog.now()),
            (9807, 9704, 10.00, pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id BETWEEN 9801 AND 9807`,
			`DELETE FROM public.api_keys WHERE id BETWEEN 9701 AND 9704`,
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

	require.Equal(t, []string{"Leaderboard Tie", "Leaderboard Beta", "Leaderboard Alpha"}, leaderboardNames(windows[search.Window1Day]))
	require.Equal(t, []string{"10.00", "10.00", "5.00"}, leaderboardCosts(windows[search.Window1Day]))
	require.Equal(t, []string{"Leaderboard Tie", "Leaderboard Beta", "Leaderboard Alpha"}, leaderboardNames(windows[search.Window3Day]))
	require.Equal(t, []string{"10.00", "10.00", "8.00"}, leaderboardCosts(windows[search.Window3Day]))
	require.Equal(t, []string{"Leaderboard Alpha", "Leaderboard Tie", "Leaderboard Beta"}, leaderboardNames(windows[search.Window7Day]))
	require.Equal(t, []string{"12.00", "10.00", "10.00"}, leaderboardCosts(windows[search.Window7Day]))
	require.Equal(t, []string{"Leaderboard Alpha", "Leaderboard Tie", "Leaderboard Beta"}, leaderboardNames(windows[search.Window30Day]))
	require.Equal(t, []string{"14.00", "10.00", "10.00"}, leaderboardCosts(windows[search.Window30Day]))

	for _, window := range search.AllLeaderboardWindows() {
		entries := windows[window]
		require.NotNil(t, entries)
		for index, entry := range entries {
			require.Equal(t, index+1, entry.Rank)
			require.NotContains(t, entry.KeyMasked, "leaderboard")
			require.NotEqual(t, "Leaderboard Deleted", entry.Name)
		}
	}
}
```

- [ ] **Step 2: Add exact assertions and focused projection helpers**

Add these helpers at file scope:

```go
func leaderboardNames(entries []search.LeaderboardEntry) []string {
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry.Name)
	}
	return values
}

func leaderboardCosts(entries []search.LeaderboardEntry) []string {
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry.ActualCost)
	}
	return values
}
```

- [ ] **Step 3: Run the PostgreSQL integration test**

Run:

```bash
go test -tags integration ./internal/postgres -run TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted -count=1 -v
```

Expected: PASS using the disposable PostgreSQL harness. If Docker is unavailable outside CI, the test reports SKIP; run it in CI or on a Docker-capable host before deployment.

- [ ] **Step 4: Commit the integration coverage**

```bash
git add internal/postgres/leaderboard_integration_test.go
git commit -m "test: cover optimized leaderboard windows"
```

### Task 3: Add the Concurrent Covering Index Runbook

**Files:**
- Create: `docs/leaderboard-index.sql`
- Modify: `README.md`
- Modify: `tests/operation_docs_test.go`

- [ ] **Step 1: Add a failing documentation contract test**

Add this test to `tests/operation_docs_test.go`:

```go
func TestLeaderboardIndexRunbookUsesConcurrentCoveringIndex(t *testing.T) {
	content, err := os.ReadFile("../docs/leaderboard-index.sql")
	if err != nil {
		t.Fatalf("read leaderboard index runbook: %v", err)
	}
	normalized := strings.ToLower(string(content))
	for _, required := range []string{
		"create index concurrently if not exists idx_usage_logs_leaderboard_cover",
		"on public.usage_logs (created_at, api_key_id)",
		"include (actual_cost)",
		"analyze public.usage_logs",
		"pg_stat_progress_create_index",
		"drop index concurrently if exists public.idx_usage_logs_leaderboard_cover",
	} {
		if !strings.Contains(normalized, required) {
			t.Errorf("leaderboard index runbook missing %q", required)
		}
	}
	for _, forbidden := range []string{"begin;", "start transaction"} {
		if strings.Contains(normalized, forbidden) {
			t.Errorf("leaderboard index runbook must not wrap concurrent DDL in a transaction: %q", forbidden)
		}
	}
}
```

The file already imports `os` and `strings`; reuse those imports rather than adding duplicates.

- [ ] **Step 2: Run the documentation test and confirm the expected failure**

Run:

```bash
go test ./tests -run TestLeaderboardIndexRunbookUsesConcurrentCoveringIndex -count=1
```

Expected: FAIL because `docs/leaderboard-index.sql` does not exist.

- [ ] **Step 3: Create the DBA-run index file**

Create `docs/leaderboard-index.sql` with:

```sql
-- Run with psql autocommit enabled as the table owner or a DBA.
-- CREATE/DROP INDEX CONCURRENTLY cannot run inside an explicit transaction.

SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public'
  AND tablename = 'usage_logs'
ORDER BY indexname;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_leaderboard_cover
ON public.usage_logs (created_at, api_key_id)
INCLUDE (actual_cost);

ANALYZE public.usage_logs;

SELECT pid, phase, lockers_total, lockers_done, blocks_total, blocks_done,
       tuples_total, tuples_done
FROM pg_stat_progress_create_index
WHERE index_relid = 'public.idx_usage_logs_leaderboard_cover'::regclass;

-- Rollback command; run separately only when rollback is required:
-- DROP INDEX CONCURRENTLY IF EXISTS public.idx_usage_logs_leaderboard_cover;
```

- [ ] **Step 4: Document installation, verification, and rollback in README**

Add a `### 排行榜性能索引` subsection after the database prerequisites. State that the viewer never creates schema objects at startup. Show:

```bash
psql "$DATABASE_URL" -f docs/leaderboard-index.sql
```

Also document index inventory and `DROP INDEX CONCURRENTLY IF EXISTS public.idx_usage_logs_leaderboard_cover;`, including the explicit-transaction restriction.

- [ ] **Step 5: Run documentation tests**

Run:

```bash
gofmt -w tests/operation_docs_test.go
go test ./tests -run 'TestLeaderboardIndexRunbook|TestRoleGuide' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the operational index documentation**

```bash
git add docs/leaderboard-index.sql README.md tests/operation_docs_test.go
git commit -m "docs: add leaderboard covering index runbook"
```

### Task 4: Full Verification and Production Handoff

**Files:**
- Verify only; no planned source changes.

- [ ] **Step 1: Run formatting and static analysis**

```bash
gofmt -w internal/postgres/leaderboard.go internal/postgres/leaderboard_test.go internal/postgres/leaderboard_integration_test.go tests/operation_docs_test.go
go vet ./...
```

Expected: no output and exit status 0.

- [ ] **Step 2: Run the complete unit and runtime test suite**

```bash
go test ./... -count=1
```

Expected: all packages pass.

- [ ] **Step 3: Run PostgreSQL integration tests**

```bash
go test -tags integration ./internal/postgres/... -count=1
```

Expected: PASS on a Docker-capable host; no leaderboard test failure is acceptable.

- [ ] **Step 4: Build the production binary**

```bash
mkdir -p dist
go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
```

Expected: binary created successfully.

- [ ] **Step 5: Verify the production query plan after index installation**

Run the optimized statement through `EXPLAIN (ANALYZE, BUFFERS)` with the four UTC boundaries and limit 10. Confirm one `usage_logs` scan, no per-key rescan, no large temporary-file spill, and database execution below two seconds.

- [ ] **Step 6: Verify authenticated HTTP latency after deployment**

After one warm-up request, issue at least 30 authenticated requests for each limit. Record HTTP status and total time. Acceptance is zero 503 responses and P95 below two seconds for limits 10, 20, and 50.

- [ ] **Step 7: Inspect the final diff and working tree**

```bash
git status --short
git log --oneline -5
```

Expected: no uncommitted implementation files and three focused implementation commits after the design and plan commits.
