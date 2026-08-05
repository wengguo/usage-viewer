//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestSearchRepositoryReturnsOnlyCurrentBoundedTargets(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t,
		`INSERT INTO public.accounts (id, name, platform, status, deleted_at) VALUES
            (9201, 'Phase Two Current', 'openai', 'active', NULL),
            (9202, 'Phase Two Deleted', 'openai', 'active', pg_catalog.now())`,
		`INSERT INTO public.users (id, email, username, status, deleted_at) VALUES
            (9301, 'current@example.invalid', 'phase-two-current', 'active', NULL),
            (9302, 'deleted@example.invalid', 'phase-two-deleted', 'active', pg_catalog.now())`,
	)
	t.Cleanup(func() {
		harness.execAdmin(t,
			`DELETE FROM public.accounts WHERE id IN (9201, 9202)`,
			`DELETE FROM public.users WHERE id IN (9301, 9302)`,
		)
	})

	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))
	repository := NewSearchRepository(pool, cfg.DatabaseQueryTimeout)

	accountText := mustSearchQuery(t, search.TargetAccount, "Phase Two")
	accountResults, err := repository.Search(context.Background(), accountText)
	require.NoError(t, err)
	require.Equal(t, []search.AccountResult{{ID: 9201, Name: "Phase Two Current"}}, accountResults.Accounts)
	require.Empty(t, accountResults.Users)

	userText := mustSearchQuery(t, search.TargetUser, "phase-two")
	userResults, err := repository.Search(context.Background(), userText)
	require.NoError(t, err)
	require.Equal(t, []search.UserResult{{ID: 9301, Username: "phase-two-current", Email: "current@example.invalid"}}, userResults.Users)
	require.Empty(t, userResults.Accounts)

	for _, tt := range []struct {
		target search.TargetType
		id     string
	}{
		{target: search.TargetAccount, id: "9202"},
		{target: search.TargetUser, id: "9302"},
	} {
		results, searchErr := repository.Search(context.Background(), mustSearchQuery(t, tt.target, tt.id))
		require.NoError(t, searchErr)
		require.Empty(t, results.Accounts)
		require.Empty(t, results.Users)
	}
}

func mustSearchQuery(t *testing.T, target search.TargetType, value string) search.Query {
	t.Helper()
	query, err := search.Validate(search.Request{TargetType: target, Query: value})
	require.NoError(t, err)
	return query
}
