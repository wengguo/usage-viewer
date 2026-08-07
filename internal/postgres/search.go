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

const searchResultLimit = search.PageSize

// Each listing statement is a compile-time constant chosen from the validated
// sort enum. Cost totals remain numeric in the CTE so SQL never orders their
// text display representation.
const keyBrowseSQLPrefix = `
WITH key_costs AS (
    SELECT
        api_key.id,
        api_key.name,
        COALESCE(grp.name, '') AS group_name,
        COALESCE(api_key.quota, 0)::text AS quota,
        COALESCE(api_key.quota_used, 0)::text AS quota_used,
        COALESCE(api_key.expires_at::text, '') AS expires_at,
        COALESCE(api_key.last_used_at::text, '') AS last_used_at,
        api_key.status,
        api_key.created_at::text AS created_at,
        COALESCE((
            SELECT pg_catalog.sum(COALESCE(ul.actual_cost, 0))
            FROM public.usage_logs AS ul
            WHERE ul.api_key_id = api_key.id
              AND ul.created_at >= $1
              AND ul.created_at < $3
        ), 0) AS total_30d_cost,
        COALESCE((
            SELECT pg_catalog.sum(COALESCE(ul.actual_cost, 0))
            FROM public.usage_logs AS ul
            WHERE ul.api_key_id = api_key.id
              AND ul.created_at >= $2
        ), 0) AS today_cost
    FROM public.api_keys AS api_key
    LEFT JOIN public.groups AS grp ON grp.id = api_key.group_id
    WHERE api_key.deleted_at IS NULL
)
SELECT id, name, group_name, quota, quota_used, expires_at, last_used_at, status, created_at,
       total_30d_cost::text, today_cost::text
FROM key_costs
`

const keyTextSQLPrefix = `
WITH key_costs AS (
    SELECT
        api_key.id,
        api_key.name,
        COALESCE(grp.name, '') AS group_name,
        COALESCE(api_key.quota, 0)::text AS quota,
        COALESCE(api_key.quota_used, 0)::text AS quota_used,
        COALESCE(api_key.expires_at::text, '') AS expires_at,
        COALESCE(api_key.last_used_at::text, '') AS last_used_at,
        api_key.status,
        api_key.created_at::text AS created_at,
        COALESCE((
            SELECT pg_catalog.sum(COALESCE(ul.actual_cost, 0))
            FROM public.usage_logs AS ul
            WHERE ul.api_key_id = api_key.id
              AND ul.created_at >= $2
              AND ul.created_at < $4
        ), 0) AS total_30d_cost,
        COALESCE((
            SELECT pg_catalog.sum(COALESCE(ul.actual_cost, 0))
            FROM public.usage_logs AS ul
            WHERE ul.api_key_id = api_key.id
              AND ul.created_at >= $3
        ), 0) AS today_cost
    FROM public.api_keys AS api_key
    LEFT JOIN public.groups AS grp ON grp.id = api_key.group_id
    WHERE api_key.deleted_at IS NULL
      AND (
          api_key.name ILIKE '%' || $1 || '%' ESCAPE '\'
          OR api_key.key ILIKE '%' || $1 || '%' ESCAPE '\'
      )
)
SELECT id, name, group_name, quota, quota_used, expires_at, last_used_at, status, created_at,
       total_30d_cost::text, today_cost::text
FROM key_costs
`

const (
	keyBrowseByIDDescSQL           = keyBrowseSQLPrefix + "ORDER BY id DESC LIMIT 20 OFFSET $4"
	keyBrowseByIDAscSQL            = keyBrowseSQLPrefix + "ORDER BY id ASC LIMIT 20 OFFSET $4"
	keyBrowseByTodayCostDescSQL    = keyBrowseSQLPrefix + "ORDER BY today_cost DESC, id DESC LIMIT 20 OFFSET $4"
	keyBrowseByTodayCostAscSQL     = keyBrowseSQLPrefix + "ORDER BY today_cost ASC, id DESC LIMIT 20 OFFSET $4"
	keyBrowseByTotal30dCostDescSQL = keyBrowseSQLPrefix + "ORDER BY total_30d_cost DESC, id DESC LIMIT 20 OFFSET $4"
	keyBrowseByTotal30dCostAscSQL  = keyBrowseSQLPrefix + "ORDER BY total_30d_cost ASC, id DESC LIMIT 20 OFFSET $4"

	keyByTextSQL                 = keyTextSQLPrefix + "ORDER BY id DESC LIMIT 20 OFFSET $5"
	keyTextByIDAscSQL            = keyTextSQLPrefix + "ORDER BY id ASC LIMIT 20 OFFSET $5"
	keyTextByTodayCostDescSQL    = keyTextSQLPrefix + "ORDER BY today_cost DESC, id DESC LIMIT 20 OFFSET $5"
	keyTextByTodayCostAscSQL     = keyTextSQLPrefix + "ORDER BY today_cost ASC, id DESC LIMIT 20 OFFSET $5"
	keyTextByTotal30dCostDescSQL = keyTextSQLPrefix + "ORDER BY total_30d_cost DESC, id DESC LIMIT 20 OFFSET $5"
	keyTextByTotal30dCostAscSQL  = keyTextSQLPrefix + "ORDER BY total_30d_cost ASC, id DESC LIMIT 20 OFFSET $5"
)

const keyCountBrowseSQL = `
SELECT pg_catalog.count(*)
FROM public.api_keys AS api_key
WHERE api_key.deleted_at IS NULL
`

const keyCountTextSQL = `
SELECT pg_catalog.count(*)
FROM public.api_keys AS api_key
WHERE api_key.deleted_at IS NULL
  AND (
      api_key.name ILIKE '%' || $1 || '%' ESCAPE '\'
      OR api_key.key ILIKE '%' || $1 || '%' ESCAPE '\'
  )
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

	// The database session timezone defines today's UTC boundary, matching the
	// existing dashboard aggregation convention.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	thirtyDaysAgo := today.AddDate(0, 0, -30)
	results := search.Results{Page: query.Page(), PageSize: search.PageSize}
	err := withReadOnlyTx(ctx, repository.pool, repository.timeout, func(queryCtx context.Context, tx pgx.Tx) error {
		var err error
		results.Total, err = countKeys(queryCtx, tx, query)
		if err != nil {
			return err
		}
		results.Keys, err = searchKeys(queryCtx, tx, query, today, thirtyDaysAgo)
		return err
	})
	if err != nil {
		return search.Results{}, ErrSearchUnavailable
	}

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

func countKeys(ctx context.Context, tx pgx.Tx, query search.Query) (int64, error) {
	var total int64
	if query.Mode() == search.QueryModeBrowse {
		err := tx.QueryRow(ctx, keyCountBrowseSQL).Scan(&total)
		return total, err
	}
	err := tx.QueryRow(ctx, keyCountTextSQL, escapeLikeText(query.Text())).Scan(&total)
	return total, err
}

func searchKeys(ctx context.Context, tx pgx.Tx, query search.Query, today, thirtyDaysAgo time.Time) ([]search.KeyResult, error) {
	offset := (query.Page() - 1) * searchResultLimit
	statement := listingSQL(query)
	if query.Mode() == search.QueryModeBrowse {
		return collectKeyResults(ctx, tx, statement, thirtyDaysAgo, today, time.Now().UTC(), offset)
	}
	return collectKeyResults(ctx, tx, statement, escapeLikeText(query.Text()), thirtyDaysAgo, today, time.Now().UTC(), offset)
}

func listingSQL(query search.Query) string {
	if query.Mode() == search.QueryModeBrowse {
		switch query.SortBy() {
		case search.SortByTodayCost:
			if query.SortDirection() == search.SortDirectionAscending {
				return keyBrowseByTodayCostAscSQL
			}
			return keyBrowseByTodayCostDescSQL
		case search.SortByTotal30dCost:
			if query.SortDirection() == search.SortDirectionAscending {
				return keyBrowseByTotal30dCostAscSQL
			}
			return keyBrowseByTotal30dCostDescSQL
		default:
			if query.SortDirection() == search.SortDirectionAscending {
				return keyBrowseByIDAscSQL
			}
			return keyBrowseByIDDescSQL
		}
	}
	switch query.SortBy() {
	case search.SortByTodayCost:
		if query.SortDirection() == search.SortDirectionAscending {
			return keyTextByTodayCostAscSQL
		}
		return keyTextByTodayCostDescSQL
	case search.SortByTotal30dCost:
		if query.SortDirection() == search.SortDirectionAscending {
			return keyTextByTotal30dCostAscSQL
		}
		return keyTextByTotal30dCostDescSQL
	default:
		if query.SortDirection() == search.SortDirectionAscending {
			return keyTextByIDAscSQL
		}
		return keyByTextSQL
	}
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
