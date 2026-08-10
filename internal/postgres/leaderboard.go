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
