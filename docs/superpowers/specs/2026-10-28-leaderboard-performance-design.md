# Leaderboard Performance Optimization Design

## Context

`POST /api/leaderboard` currently executes the same aggregate query four times,
once for each 1, 3, 7, and 30-day window. The queries run serially inside one
read-only transaction and share the default five-second database timeout.

Production observations on October 28, 2026 showed:

- `/readyz` returned HTTP 200 in about 0.60 seconds.
- `/api/search` returned HTTP 200 in about 5.27 seconds.
- `/api/leaderboard` returned HTTP 503 in about 6.18 seconds with
  `LEADERBOARD_UNAVAILABLE`.

The service and database are available, but the leaderboard repeats overlapping
log scans until the shared transaction deadline is exceeded.

## Goals

- Preserve the existing HTTP request and response contracts.
- Preserve the exact 1, 3, 7, and 30 calendar-day window semantics.
- Preserve ranking by cost descending and API key ID descending.
- Reduce `usage_logs` access from four aggregate scans to one 30-day scan.
- Achieve production leaderboard P95 below two seconds.
- Keep the existing five-second database query timeout.
- Support limits 10, 20, and 50 without dynamic SQL.

## Non-Goals

- No new tables, materialized views, or scheduled aggregation jobs.
- No application or Redis cache in this change.
- No changes to search, self-lookup, authentication, or frontend behavior.
- No automatic schema mutation from the application process.

## Chosen Approach

Replace the four serial leaderboard statements with one static SQL statement.
The statement scans the last 30 days once, groups by API key, and calculates all
four totals with PostgreSQL aggregate `FILTER` clauses. It then expands each key
into four window rows, ranks each window independently, and returns only the
requested Top N rows.

Add a production database covering index for the range scan:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS
    idx_usage_logs_leaderboard_cover
ON public.usage_logs (created_at, api_key_id)
INCLUDE (actual_cost);
```

The existing API-key-first index, if present, remains in place because other
queries use `api_key_id` as their leading predicate.

## Query Design

The Go repository calculates four UTC midnight boundaries from one `now` value
and binds them as query parameters. The earliest boundary is the 30-day start,
which bounds the only raw-log scan.

```sql
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
    SELECT key_costs.api_key_id, costs.window, costs.actual_cost
    FROM key_costs
    CROSS JOIN LATERAL (
        VALUES
            ('1d', key_costs.cost_1d),
            ('3d', key_costs.cost_3d),
            ('7d', key_costs.cost_7d),
            ('30d', key_costs.cost_30d)
    ) AS costs(window, actual_cost)
    WHERE costs.actual_cost IS NOT NULL
),
ranked AS (
    SELECT
        window_costs.window,
        api_key.id,
        api_key.key,
        api_key.name,
        COALESCE(grp.name, '')::text AS group_name,
        window_costs.actual_cost,
        pg_catalog.row_number() OVER (
            PARTITION BY window_costs.window
            ORDER BY window_costs.actual_cost DESC, api_key.id DESC
        ) AS rank
    FROM window_costs
    JOIN public.api_keys AS api_key
      ON api_key.id = window_costs.api_key_id
    LEFT JOIN public.groups AS grp
      ON grp.id = api_key.group_id
    WHERE api_key.deleted_at IS NULL
)
SELECT window, rank, id, key, name, group_name, actual_cost::text
FROM ranked
WHERE rank <= $5
ORDER BY window, rank;
```

Joining `api_keys` and `groups` after the raw-log aggregation avoids joining
every log row to metadata. Excluding null window totals preserves the current
behavior: a key without usage in a window does not appear in that window.

## Repository Changes

`LeaderboardRepository.Top` continues to validate dependencies and limits and
continues to run through the existing read-only transaction helper. It executes
one query and maps each returned row into the existing result map.

The row mapper must:

- Reject unknown window values returned by the database.
- Preserve the database-computed rank.
- Mask the raw key before constructing `LeaderboardEntry`.
- Initialize all four map entries to non-nil empty slices so JSON behavior is
  stable when a window has no usage.
- Return `ErrLeaderboardUnavailable` for database or row-decoding failures,
  preserving the existing public error contract.

## Index Deployment

The index is an operational migration, not application startup behavior.
Create it with `CONCURRENTLY` to avoid blocking normal writes for the duration
of the index build. The migration operator must run it outside an explicit
transaction and monitor progress through `pg_stat_progress_create_index`.

Before creation, capture existing indexes:

```sql
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public' AND tablename = 'usage_logs';
```

After creation, run `ANALYZE public.usage_logs;` if normal autovacuum analysis
has not refreshed statistics.

Rollback removes only the new index:

```sql
DROP INDEX CONCURRENTLY IF EXISTS public.idx_usage_logs_leaderboard_cover;
```

## Verification

### Automated Tests

- SQL contract test confirms one bounded `usage_logs` statement with aggregate
  filters, ranking, bound limit, and no write statements.
- Repository integration test covers all four windows, deleted keys, stable
  tie-breaking, limits, and empty-window slices.
- Add a parity test that compares the optimized result with independently
  calculated expected totals at UTC day boundaries.
- Run the complete Go test suite and `go vet ./...`.

### Production Query Plan

Run the optimized statement with `EXPLAIN (ANALYZE, BUFFERS)` using production-
representative parameters. Acceptance requires:

- One scan of `usage_logs`, not four scans.
- No unexpected nested loop that rescans `usage_logs` per API key.
- No excessive temporary-file spill during aggregation or ranking.
- Execution time comfortably below two seconds before network overhead.

### HTTP Performance

Measure at least 30 authenticated requests after one warm-up request. Record
status, time to first byte, and total time. Acceptance requires:

- No HTTP 503 responses.
- P95 total response time below two seconds.
- Response parity for limits 10, 20, and 50.

## Rollout

1. Capture the current query plan and index inventory.
2. Create the covering index concurrently and analyze the table if needed.
3. Deploy the optimized application query.
4. Verify response parity and collect the 30-request latency sample.
5. Roll back the application first if correctness differs; drop the index only
   if it causes measurable write or storage problems.

Temporarily increasing `SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT` is not part of
the target design. It may be used only as an emergency operational workaround
while the optimized release is being deployed.
