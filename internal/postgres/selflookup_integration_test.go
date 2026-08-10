//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelfLookupRepositoryExactMatchesKeyOrNameWithStableTiebreak(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9901, 'Self Lookup Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at, expires_at) VALUES
            (9911, 'sk-self-lookup-first-01', 'shared-name', 9901, 100, 25, 'active', pg_catalog.now(), NULL),
            (9912, 'sk-self-lookup-second-2', 'shared-name', 9901, 100, 10, 'active', pg_catalog.now(), NULL),
            (9913, 'sk-self-lookup-deleted01', 'deleted-self-lookup', 9901, 100, 0, 'active', pg_catalog.now(), NULL)`,
		`UPDATE public.api_keys SET deleted_at = pg_catalog.now() WHERE id = 9913`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9921, 9911, 2.50, pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id = 9921`,
			`DELETE FROM public.api_keys WHERE id IN (9911, 9912, 9913)`,
			`DELETE FROM public.groups WHERE id = 9901`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))
	repository := NewSelfLookupRepository(pool, cfg.DatabaseQueryTimeout)

	result, id, ok, err := repository.Lookup(context.Background(), "sk-self-lookup-first-01")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(9911), id)
	require.Equal(t, "2.50", result.TodayCost)
	require.Equal(t, "25", result.QuotaUsed)
	require.NotContains(t, result.KeyMasked, "self-lookup-first")

	result, id, ok, err = repository.Lookup(context.Background(), "shared-name")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(9911), id, "duplicate name resolves to the lowest id, not 9912")

	_, _, ok, err = repository.Lookup(context.Background(), "sk-self-lookup-deleted01")
	require.NoError(t, err)
	require.False(t, ok, "deleted key must not be found")

	_, _, ok, err = repository.Lookup(context.Background(), "does-not-exist-sentinel")
	require.NoError(t, err)
	require.False(t, ok)
}
