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
