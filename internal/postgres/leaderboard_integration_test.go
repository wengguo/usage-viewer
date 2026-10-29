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
            (9703, 'sk-leaderboard-deleted1', 'Leaderboard Deleted', 9601, 100, 0, 'active', pg_catalog.now(), pg_catalog.now()),
            (9704, 'sk-leaderboard-tie-0004', 'Leaderboard Tie', 9601, 100, 0, 'active', pg_catalog.now(), NULL)`,
		`INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
            (9801, 9701, 5.00, pg_catalog.now()),
            (9802, 9701, 3.00, pg_catalog.now() - interval '2 days'),
            (9803, 9701, 4.00, pg_catalog.now() - interval '6 days'),
            (9804, 9701, 2.00, pg_catalog.now() - interval '29 days'),
            (9805, 9702, 10.00, pg_catalog.now()),
            (9806, 9703, 999.00, pg_catalog.now()),
            (9807, 9704, 10.00, pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.usage_logs WHERE id IN (9801, 9802, 9803, 9804, 9805, 9806, 9807)`,
			`DELETE FROM public.api_keys WHERE id IN (9701, 9702, 9703, 9704)`,
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

	expected := map[search.LeaderboardWindow]struct {
		names []string
		costs []string
	}{
		search.Window1Day:  {names: []string{"Leaderboard Tie", "Leaderboard Beta", "Leaderboard Alpha"}, costs: []string{"10.00", "10.00", "5.00"}},
		search.Window3Day:  {names: []string{"Leaderboard Tie", "Leaderboard Beta", "Leaderboard Alpha"}, costs: []string{"10.00", "10.00", "8.00"}},
		search.Window7Day:  {names: []string{"Leaderboard Alpha", "Leaderboard Tie", "Leaderboard Beta"}, costs: []string{"12.00", "10.00", "10.00"}},
		search.Window30Day: {names: []string{"Leaderboard Alpha", "Leaderboard Tie", "Leaderboard Beta"}, costs: []string{"14.00", "10.00", "10.00"}},
	}

	for _, window := range search.AllLeaderboardWindows() {
		entries, ok := windows[window]
		require.True(t, ok, "window %q must be present", window)
		require.NotNil(t, entries, "window %q must return a non-nil slice", window)
		require.Equal(t, expected[window].names, leaderboardNames(entries), "window %q names", window)
		require.Equal(t, expected[window].costs, leaderboardCosts(entries), "window %q costs", window)
		for index, entry := range entries {
			require.Equal(t, index+1, entry.Rank, "window %q rank", window)
			require.NotContains(t, entry.KeyMasked, "leaderboard", "raw key data must stay masked")
			require.NotEqual(t, "Leaderboard Deleted", entry.Name, "deleted key must never appear")
		}
	}
}

func leaderboardNames(entries []search.LeaderboardEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func leaderboardCosts(entries []search.LeaderboardEntry) []string {
	costs := make([]string, 0, len(entries))
	for _, entry := range entries {
		costs = append(costs, entry.ActualCost)
	}
	return costs
}
