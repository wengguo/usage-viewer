package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
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

func TestLeaderboardWindowSQLIsStaticBoundedAndExcludesDeletedKeys(t *testing.T) {
	for _, window := range search.AllLeaderboardWindows() {
		statement, ok := leaderboardSQLByWindow[window]
		if !ok {
			t.Fatalf("no SQL registered for window %q", window)
		}
		normalized := strings.ToLower(statement)
		for _, required := range []string{
			"select ",
			"from public.usage_logs",
			"join public.api_keys",
			"left join public.groups",
			"api_key.deleted_at is null",
			"group by",
			"order by",
			"limit $",
		} {
			if !strings.Contains(normalized, required) {
				t.Fatalf("window %q SQL missing %q: %s", window, required, statement)
			}
		}
		for _, forbidden := range []string{"select *", " insert ", " update ", " delete "} {
			if strings.Contains(" "+normalized+" ", forbidden) {
				t.Fatalf("window %q SQL contains forbidden fragment %q", window, forbidden)
			}
		}
	}
}
