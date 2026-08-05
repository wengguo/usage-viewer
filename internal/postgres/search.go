package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/concurrency"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

const searchResultLimit = 20

// keyByTextSQL returns the key identity row plus a 30-day actual-cost total
// and today's actual-cost total, mirroring the Sub2API dashboard aggregation
// (usage_logs.actual_cost summed over the last 30 days and since today).
const keyByTextSQL = `
SELECT
    api_key.id,
    api_key.name,
    COALESCE(grp.name, ''),
    COALESCE(api_key.quota, 0)::text,
    COALESCE(api_key.quota_used, 0)::text,
    COALESCE(api_key.expires_at::text, ''),
    COALESCE(api_key.last_used_at::text, ''),
    api_key.status,
    api_key.created_at::text,
    COALESCE((
        SELECT pg_catalog.sum(COALESCE(ul.actual_cost, 0))::text
        FROM public.usage_logs AS ul
        WHERE ul.api_key_id = api_key.id
          AND ul.created_at >= $3
          AND ul.created_at < $5
    ), '0'),
    COALESCE((
        SELECT pg_catalog.sum(COALESCE(ul.actual_cost, 0))::text
        FROM public.usage_logs AS ul
        WHERE ul.api_key_id = api_key.id
          AND ul.created_at >= $4
    ), '0')
FROM public.api_keys AS api_key
LEFT JOIN public.groups AS grp ON grp.id = api_key.group_id
WHERE api_key.deleted_at IS NULL
  AND (
      api_key.name ILIKE '%' || $1 || '%' ESCAPE '\'
      OR api_key.key ILIKE '%' || $1 || '%' ESCAPE '\'
  )
ORDER BY
    CASE
        WHEN pg_catalog.lower(api_key.name) = pg_catalog.lower($2) THEN 0
        WHEN pg_catalog.lower(api_key.key) = pg_catalog.lower($2) THEN 0
        ELSE 1
    END,
    api_key.id ASC
LIMIT 20
`

var ErrSearchUnavailable = errors.New("search is temporarily unavailable")

type SearchRepository struct {
	pool           *pgxpool.Pool
	timeout        time.Duration
	concurrencyRes *concurrency.Resolver
}

func NewSearchRepository(pool *pgxpool.Pool, timeout time.Duration, resolver *concurrency.Resolver) *SearchRepository {
	return &SearchRepository{pool: pool, timeout: timeout, concurrencyRes: resolver}
}

func (repository *SearchRepository) Search(ctx context.Context, query search.Query) (search.Results, error) {
	if repository == nil || repository.pool == nil || repository.timeout <= 0 || !query.Valid() {
		return search.Results{}, ErrSearchUnavailable
	}

	// Subquery boundaries: today (start of local day in Asia/Shanghai by
	// default) and the last 30 days. The sub2api dashboard uses the server
	// timezone; here we use the database session timezone (UTC unless the
	// deployment sets a timezone) for the "today" boundary.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	thirtyDaysAgo := today.AddDate(0, 0, -30)

	var results search.Results
	err := withReadOnlyTx(ctx, repository.pool, repository.timeout, func(queryCtx context.Context, tx pgx.Tx) error {
		var err error
		results.Keys, err = searchKeys(queryCtx, tx, query, today, thirtyDaysAgo)
		return err
	})
	if err != nil {
		return search.Results{}, ErrSearchUnavailable
	}

	// Resolve current concurrency from Redis (best-effort; 0 when unavailable).
	ids := make([]int64, 0, len(results.Keys))
	for _, key := range results.Keys {
		ids = append(ids, key.ID)
	}
	concurrencyByID := repository.concurrencyRes.Resolve(ctx, ids)
	for index := range results.Keys {
		results.Keys[index].CurrentConcurrency = concurrencyByID[results.Keys[index].ID]
	}
	return results, nil
}

func searchKeys(ctx context.Context, tx pgx.Tx, query search.Query, today, thirtyDaysAgo time.Time) ([]search.KeyResult, error) {
	escaped := escapeLikeText(query.Text())
	return collectKeyResults(ctx, tx, keyByTextSQL, escaped, query.Text(), thirtyDaysAgo, today, time.Now().UTC())
}

func collectKeyResults(ctx context.Context, tx pgx.Tx, statement string, arguments ...any) ([]search.KeyResult, error) {
	rows, err := tx.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]search.KeyResult, 0, searchResultLimit)
	for rows.Next() {
		var result search.KeyResult
		if err := rows.Scan(
			&result.ID,
			&result.Name,
			&result.GroupName,
			&result.Quota,
			&result.QuotaUsed,
			&result.ExpiresAt,
			&result.LastUsedAt,
			&result.Status,
			&result.CreatedAt,
			&result.Total30dCost,
			&result.TodayCost,
		); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func escapeLikeText(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
