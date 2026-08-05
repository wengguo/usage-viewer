package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestDailyUsageSQLIsStaticBoundedAndAggregatesActualCost(t *testing.T) {
	normalized := strings.ToLower(dailyUsageSQL)
	for _, required := range []string{
		"select",
		"to_char(ul.created_at, 'yyyy-mm-dd')",
		"sum(ul.actual_cost)",
		"from public.usage_logs",
		"ul.api_key_id = $1",
		"ul.created_at >= $2",
		"ul.created_at < $3",
		"group by",
		"order by",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("daily usage SQL missing %q", required)
		}
	}
	for _, forbidden := range []string{"select *", " insert ", " update ", " delete ", " join "} {
		if strings.Contains(" "+normalized+" ", forbidden) {
			t.Fatalf("daily usage SQL contains forbidden fragment %q", forbidden)
		}
	}
}

func TestValidateDaysBounds(t *testing.T) {
	for _, days := range []int{1, 30, 90} {
		if got, err := search.ValidateDays(days); err != nil || got != days {
			t.Fatalf("ValidateDays(%d) = %d, %v", days, got, err)
		}
	}
	for _, days := range []int{0, -1, 91, 100} {
		if _, err := search.ValidateDays(days); !errors.Is(err, search.ErrInvalidDays) {
			t.Fatalf("ValidateDays(%d) error = %v, want ErrInvalidDays", days, err)
		}
	}
}

func TestDailyUsageRepositoryRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name       string
		repository *DailyUsageRepository
		keyID      int64
		days       int
	}{
		{name: "nil repository", repository: nil, keyID: 1, days: 30},
		{name: "nil pool", repository: NewDailyUsageRepository(nil, time.Second), keyID: 1, days: 30},
		{name: "invalid timeout", repository: NewDailyUsageRepository(nil, 0), keyID: 1, days: 30},
		{name: "zero key id", repository: NewDailyUsageRepository(nil, time.Second), keyID: 0, days: 30},
		{name: "invalid days", repository: NewDailyUsageRepository(nil, time.Second), keyID: 1, days: 91},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotErr := tt.repository.DailyUsage(context.Background(), tt.keyID, tt.days)
			if !errors.Is(gotErr, ErrDailyUsageUnavailable) || gotErr.Error() != "daily usage is temporarily unavailable" {
				t.Fatalf("DailyUsage() error = %v", gotErr)
			}
		})
	}
}
