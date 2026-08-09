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

func mustSearchQuery(t *testing.T, value string) search.Query {
	t.Helper()
	query, err := search.Validate(search.Request{TargetType: search.TargetKey, Query: value})
	require.NoError(t, err)
	return query
}
