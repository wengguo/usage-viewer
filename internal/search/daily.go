package search

import "errors"

const (
	MinimumUsageDays = 1
	MaximumUsageDays = 90
)

var ErrInvalidDays = errors.New("usage days must be between 1 and 90")

// DailyUsagePoint is one day of actual cost for a single API key, mirroring
// the Sub2API daily usage endpoint aggregation over usage_logs.actual_cost.
type DailyUsagePoint struct {
	Date       string `json:"date"`
	ActualCost string `json:"actualCost"`
}

// DailyUsageRequest is validated at the handler boundary.
type DailyUsageRequest struct {
	KeyID int64
	Days  int
}

func ValidateDays(days int) (int, error) {
	if days < MinimumUsageDays || days > MaximumUsageDays {
		return 0, ErrInvalidDays
	}
	return days, nil
}
