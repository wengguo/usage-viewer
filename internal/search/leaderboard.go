package search

import "errors"

// LeaderboardWindow identifies one of the four fixed natural-day ranges the
// leaderboard reports on. Each window starts at local-midnight-UTC N-1 days
// ago (inclusive) and runs through now, so "1d" means "today so far".
type LeaderboardWindow string

const (
	Window1Day  LeaderboardWindow = "1d"
	Window3Day  LeaderboardWindow = "3d"
	Window7Day  LeaderboardWindow = "7d"
	Window30Day LeaderboardWindow = "30d"
)

// AllLeaderboardWindows returns the four supported windows in the fixed
// display order used by both the API response and the frontend layout.
func AllLeaderboardWindows() []LeaderboardWindow {
	return []LeaderboardWindow{Window1Day, Window3Day, Window7Day, Window30Day}
}

// WindowDays returns how many trailing calendar days (including today) the
// window covers.
func WindowDays(window LeaderboardWindow) int {
	switch window {
	case Window1Day:
		return 1
	case Window3Day:
		return 3
	case Window7Day:
		return 7
	case Window30Day:
		return 30
	default:
		return 0
	}
}

const (
	minimumLeaderboardLimit = 10
	maximumLeaderboardLimit = 50
)

var allowedLeaderboardLimits = map[int]bool{10: true, 20: true, 50: true}

var ErrInvalidLimit = errors.New("leaderboard limit must be 10, 20, or 50")

// ValidateLimit normalizes a requested leaderboard size. A zero value (the
// JSON zero-value for an omitted field) defaults to 10; any other value must
// be exactly one of the three allowed sizes.
func ValidateLimit(limit int) (int, error) {
	if limit == 0 {
		return minimumLeaderboardLimit, nil
	}
	if !allowedLeaderboardLimits[limit] {
		return 0, ErrInvalidLimit
	}
	return limit, nil
}

// LeaderboardEntry is one ranked row within a single window's Top N.
type LeaderboardEntry struct {
	Rank       int    `json:"rank"`
	KeyMasked  string `json:"keyMasked"`
	Name       string `json:"name"`
	GroupName  string `json:"groupName"`
	ActualCost string `json:"actualCost"`
}
