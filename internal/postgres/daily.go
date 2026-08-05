package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

// dailyUsageSQL returns the daily actual cost for a key, aggregated by local
// calendar date. Timezone: the viewer session timezone (UTC unless the
// deployment configures one), consistent with the search aggregation.
const dailyUsageSQL = `
SELECT
    TO_CHAR(ul.created_at, 'YYYY-MM-DD') AS usage_date,
    COALESCE(pg_catalog.sum(ul.actual_cost), 0)::text
FROM public.usage_logs AS ul
WHERE ul.api_key_id = $1
  AND ul.created_at >= $2
  AND ul.created_at < $3
GROUP BY usage_date
ORDER BY usage_date ASC
`

var ErrDailyUsageUnavailable = errors.New("daily usage is temporarily unavailable")

type DailyUsageRepository struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

func NewDailyUsageRepository(pool *pgxpool.Pool, timeout time.Duration) *DailyUsageRepository {
	return &DailyUsageRepository{pool: pool, timeout: timeout}
}

// DailyUsage returns actual-cost points for the key across the last days
// window, oldest day first.
func (repository *DailyUsageRepository) DailyUsage(ctx context.Context, keyID int64, days int) ([]search.DailyUsagePoint, error) {
	if repository == nil || repository.pool == nil || repository.timeout <= 0 || keyID <= 0 {
		return nil, ErrDailyUsageUnavailable
	}
	normalizedDays, err := search.ValidateDays(days)
	if err != nil {
		return nil, ErrDailyUsageUnavailable
	}

	// Window: [start, now) spanning the requested number of calendar days.
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -(normalizedDays - 1)).Truncate(24 * time.Hour)
	// Reuse the read-only transaction helper for consistency with search.
	// A read-only transaction also guarantees no accidental writes.
	// Initialize a non-nil slice so the JSON response is [] rather than null.
	points := make([]search.DailyUsagePoint, 0)
	err = withReadOnlyTx(ctx, repository.pool, repository.timeout, func(queryCtx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(queryCtx, dailyUsageSQL, keyID, start, now)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var point search.DailyUsagePoint
			if err := rows.Scan(&point.Date, &point.ActualCost); err != nil {
				return err
			}
			points = append(points, point)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, ErrDailyUsageUnavailable
	}
	return points, nil
}
