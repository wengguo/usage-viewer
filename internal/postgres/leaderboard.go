package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

// leaderboardSQL scans the 30-day range once, computes all four window totals
// per key, and ranks each window independently. The four UTC window starts are
// bound as $1 through $4; $5 is the validated Top N limit.
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

// windowStart mirrors the today/thirtyDaysAgo pattern already used by
// search.go's Search method: truncate to midnight UTC, then step back
// (days-1) so a 1-day window starts at today's midnight (i.e. "today so
// far"), not yesterday's.
func windowStart(now time.Time, window search.LeaderboardWindow) time.Time {
	days := search.WindowDays(window)
	return now.Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
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
