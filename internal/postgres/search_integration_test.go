//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestSearchRepositoryReturnsOnlyCurrentUndeletedKeys(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9401, 'Phase Two Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, last_used_at, expires_at, status, created_at, deleted_at) VALUES
            (9501, 'sk-phase-two-current', 'Phase Two Current', 9401, 100, 0, NULL, NULL, 'active', pg_catalog.now(), NULL),
            (9502, 'sk-phase-two-deleted', 'Phase Two Deleted', 9401, 100, 0, NULL, NULL, 'active', pg_catalog.now(), pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.api_keys WHERE id IN (9501, 9502)`,
			`DELETE FROM public.groups WHERE id = 9401`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))
	repository := NewSearchRepository(pool, cfg.DatabaseQueryTimeout, nil)

	query := mustSearchQuery(t, "Phase Two Current")
	results, err := repository.Search(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, results.Keys, 1)
	require.Equal(t, int64(9501), results.Keys[0].ID)
	require.Equal(t, "Phase Two Current", results.Keys[0].Name)
	require.Equal(t, "Phase Two Group", results.Keys[0].GroupName)

	deletedQuery := mustSearchQuery(t, "Phase Two Deleted")
	deletedResults, err := repository.Search(context.Background(), deletedQuery)
	require.NoError(t, err)
	require.Empty(t, deletedResults.Keys)
}

func TestSearchRepositoryOrdersUsageCostsNumerically(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9411, 'Numeric Sort Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at, deleted_at) VALUES
            (9511, 'sk-numeric-sort-two', 'Numeric Sort 2', 9411, 100, 0, 'active', pg_catalog.now(), NULL),
            (9512, 'sk-numeric-sort-ten-old', 'Numeric Sort 10 Old', 9411, 100, 0, 'active', pg_catalog.now(), NULL),
            (9513, 'sk-numeric-sort-ten-new', 'Numeric Sort 10 New', 9411, 100, 0, 'active', pg_catalog.now(), NULL),
            (9514, 'sk-numeric-sort-hundred', 'Numeric Sort 100', 9411, 100, 0, 'active', pg_catalog.now(), NULL),
            (9515, 'sk-numeric-sort-one-ten', 'Numeric Sort 110', 9411, 100, 0, 'active', pg_catalog.now(), NULL)`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9811, 9511, 2, pg_catalog.now()),
            (9812, 9512, 10, pg_catalog.now()),
            (9813, 9513, 10, pg_catalog.now()),
            (9814, 9514, 100, pg_catalog.now()),
            (9815, 9515, 110, pg_catalog.now()),
            (9816, 9511, 1000, pg_catalog.now() - interval '10 days'),
            (9817, 9512, 100, pg_catalog.now() - interval '10 days'),
            (9818, 9513, 100, pg_catalog.now() - interval '10 days'),
            (9819, 9514, 10, pg_catalog.now() - interval '10 days'),
            (9820, 9515, 2, pg_catalog.now() - interval '10 days')`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id BETWEEN 9811 AND 9820`,
			`DELETE FROM public.api_keys WHERE id BETWEEN 9511 AND 9515`,
			`DELETE FROM public.groups WHERE id = 9411`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	repository := NewSearchRepository(pool, cfg.DatabaseQueryTimeout, nil)

	tests := []struct {
		name      string
		sortBy    search.SortBy
		direction search.SortDirection
		wantIDs   []int64
	}{
		{name: "today descending", sortBy: search.SortByTodayCost, direction: search.SortDirectionDescending, wantIDs: []int64{9515, 9514, 9513, 9512, 9511}},
		{name: "today ascending", sortBy: search.SortByTodayCost, direction: search.SortDirectionAscending, wantIDs: []int64{9511, 9513, 9512, 9514, 9515}},
		{name: "thirty days descending", sortBy: search.SortByTotal30dCost, direction: search.SortDirectionDescending, wantIDs: []int64{9511, 9515, 9514, 9513, 9512}},
		{name: "thirty days ascending", sortBy: search.SortByTotal30dCost, direction: search.SortDirectionAscending, wantIDs: []int64{9512, 9513, 9514, 9515, 9511}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, validateErr := search.Validate(search.Request{
				TargetType: search.TargetKey, Query: "Numeric Sort", SortBy: tt.sortBy, SortDirection: tt.direction,
			})
			require.NoError(t, validateErr)
			results, searchErr := repository.Search(context.Background(), query)
			require.NoError(t, searchErr)
			require.Len(t, results.Keys, len(tt.wantIDs))
			gotIDs := make([]int64, 0, len(results.Keys))
			for _, result := range results.Keys {
				gotIDs = append(gotIDs, result.ID)
			}
			require.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

func mustSearchQuery(t *testing.T, value string) search.Query {
	t.Helper()
	query, err := search.Validate(search.Request{TargetType: search.TargetKey, Query: value})
	require.NoError(t, err)
	return query
}
