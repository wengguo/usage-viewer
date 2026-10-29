package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLeaderboardRepositoryRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name       string
		repository *LeaderboardRepository
		limit      int
	}{
		{name: "nil repository", repository: nil, limit: 10},
		{name: "nil pool", repository: NewLeaderboardRepository(nil, time.Second), limit: 10},
		{name: "invalid timeout", repository: NewLeaderboardRepository(nil, 0), limit: 10},
		{name: "invalid limit", repository: NewLeaderboardRepository(nil, time.Second), limit: 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotErr := tt.repository.Top(context.Background(), tt.limit)
			if !errors.Is(gotErr, ErrLeaderboardUnavailable) {
				t.Fatalf("Top() error = %v, want ErrLeaderboardUnavailable", gotErr)
			}
		})
	}
}

func TestLeaderboardSQLScansUsageOnceAndRanksAllWindows(t *testing.T) {
	normalized := strings.ToLower(leaderboardSQL)
	for _, required := range []string{
		"with key_costs as materialized",
		"from public.usage_logs",
		"filter (where ul.created_at >= $1)",
		"filter (where ul.created_at >= $2)",
		"filter (where ul.created_at >= $3)",
		"where ul.created_at >= $4",
		"cross join lateral",
		"partition by window_costs.window_name",
		"join public.api_keys",
		"left join public.groups",
		"api_key.deleted_at is null",
		"rank <= $5",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("leaderboard SQL missing %q: %s", required, leaderboardSQL)
		}
	}
	if got := strings.Count(normalized, "from public.usage_logs"); got != 1 {
		t.Fatalf("usage_logs scan count = %d, want 1: %s", got, leaderboardSQL)
	}
	for _, forbidden := range []string{"select *", " insert ", " update ", " delete "} {
		if strings.Contains(" "+normalized+" ", forbidden) {
			t.Fatalf("leaderboard SQL contains forbidden fragment %q", forbidden)
		}
	}
}
