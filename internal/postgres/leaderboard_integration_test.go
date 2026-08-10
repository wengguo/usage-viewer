//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestLeaderboardRepositoryRanksByWindowAndExcludesDeleted(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.groups (id, name) VALUES (9601, 'Leaderboard Group')`,
		`INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at, deleted_at) VALUES
            (9701, 'sk-leaderboard-alpha-01', 'Leaderboard Alpha', 9601, 100, 0, 'active', pg_catalog.now(), NULL),
            (9702, 'sk-leaderboard-beta-002', 'Leaderboard Beta', 9601, 100, 0, 'active', pg_catalog.now(), NULL),
            (9703, 'sk-leaderboard-deleted1', 'Leaderboard Deleted', 9601, 100, 0, 'active', pg_catalog.now(), pg_catalog.now())`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9801, 9701, 5.00, pg_catalog.now()),
            (9802, 9701, 3.00, pg_catalog.now() - interval '2 days'),
            (9803, 9702, 10.00, pg_catalog.now()),
            (9804, 9703, 999.00, pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id IN (9801, 9802, 9803, 9804)`,
			`DELETE FROM public.api_keys WHERE id IN (9701, 9702, 9703)`,
			`DELETE FROM public.groups WHERE id = 9601`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))
	repository := NewLeaderboardRepository(pool, cfg.DatabaseQueryTimeout)

	windows, err := repository.Top(context.Background(), 10)
	require.NoError(t, err)

	// Alpha: $5 today + $3 two days ago = $8 total. Beta: $10 today only.
	// Deleted key: $999, must never surface in any window.
	oneDay := windows[search.Window1Day]
	require.Len(t, oneDay, 2, "1-day window excludes the 2-day-old usage row and the deleted key")
	require.Equal(t, "Leaderboard Beta", oneDay[0].Name)
	require.Equal(t, "10.00", oneDay[0].ActualCost)
	require.Equal(t, "Leaderboard Alpha", oneDay[1].Name)
	require.Equal(t, "5.00", oneDay[1].ActualCost)
	require.NotContains(t, oneDay[0].KeyMasked, "leaderboard")

	// 3-day window pulls in alpha's second usage row, but beta's single $10
	// row still outranks alpha's summed $8 — rank order does not flip.
	threeDay := windows[search.Window3Day]
	require.Len(t, threeDay, 2)
	require.Equal(t, "Leaderboard Beta", threeDay[0].Name, "beta's single $10 row still outranks alpha's summed $8")
	require.Equal(t, "10.00", threeDay[0].ActualCost)
	require.Equal(t, "Leaderboard Alpha", threeDay[1].Name)
	require.Equal(t, "8.00", threeDay[1].ActualCost, "3-day window sums both alpha usage rows (5.00 + 3.00)")

	for _, window := range windows {
		for _, entry := range window {
			require.NotEqual(t, "Leaderboard Deleted", entry.Name, "deleted key must never appear")
		}
	}
}
