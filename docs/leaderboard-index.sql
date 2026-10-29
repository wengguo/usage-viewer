-- Run with psql autocommit enabled as the table owner or a DBA.
-- CREATE INDEX CONCURRENTLY and DROP INDEX CONCURRENTLY cannot run inside an
-- explicit transaction block.

-- Inspect the current usage_logs indexes before making a change.
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public'
  AND tablename = 'usage_logs'
ORDER BY indexname;

-- Cover the leaderboard's 30-day time range scan and per-key aggregation.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_leaderboard_cover
ON public.usage_logs (created_at, api_key_id)
INCLUDE (actual_cost);

ANALYZE public.usage_logs;

-- This query returns a row while the concurrent index build is active.
SELECT pid,
       phase,
       lockers_total,
       lockers_done,
       blocks_total,
       blocks_done,
       tuples_total,
       tuples_done
FROM pg_stat_progress_create_index
WHERE index_relid = 'public.idx_usage_logs_leaderboard_cover'::regclass;

-- Rollback command. Run separately only when rollback is required:
-- DROP INDEX CONCURRENTLY IF EXISTS public.idx_usage_logs_leaderboard_cover;
