# Module 1: Backend Foundations (pure Go, no DB)

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). Do this module first — modules 2 and 3 import these types.

**Scope:** `internal/search` package only. No SQL, no HTTP. Everything here is deterministic and unit-testable without Docker/Postgres.

---

### Task 1: `search.MaskKey`

**Files:**
- Create: `internal/search/mask.go`
- Create: `internal/search/mask_test.go`

**Step 1: Write the failing test**

```go
// internal/search/mask_test.go
package search

import "testing"

func TestMaskKeyKeepsPrefixAndSuffixForLongKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "typical sk key", key: "sk-test-key-abc123", want: "sk-tes***abc123"},
		{name: "exactly 13 chars", key: "abcdefghijklm", want: "abcdef***hijklm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskKey(tt.key); got != tt.want {
				t.Fatalf("MaskKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestMaskKeyFullyMasksShortKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "one char", key: "a"},
		{name: "exactly 12 chars", key: "abcdefghijkl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskKey(tt.key); got != "***" {
				t.Fatalf("MaskKey(%q) = %q, want %q", tt.key, got, "***")
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/search/... -run TestMaskKey -v`
Expected: `FAIL` — `undefined: MaskKey` (compile error, since `mask.go` doesn't exist yet).

**Step 3: Write minimal implementation**

```go
// internal/search/mask.go
package search

// MaskKey returns key with its middle characters hidden, keeping only the
// first 6 and last 6 characters visible. Keys of 12 characters or fewer are
// masked entirely, since a 6+6 split would either overlap or reveal the
// whole value.
func MaskKey(key string) string {
	const visiblePrefix = 6
	const visibleSuffix = 6
	if len(key) <= visiblePrefix+visibleSuffix {
		return "***"
	}
	return key[:visiblePrefix] + "***" + key[len(key)-visibleSuffix:]
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/search/... -run TestMaskKey -v`
Expected: `PASS` for both `TestMaskKeyKeepsPrefixAndSuffixForLongKeys` and `TestMaskKeyFullyMasksShortKeys`.

**Step 5: Commit**

```bash
git add internal/search/mask.go internal/search/mask_test.go
git commit -m "feat: add search.MaskKey to hide key middle characters"
```

---

### Task 2: Leaderboard domain types and `ValidateLimit`

**Files:**
- Create: `internal/search/leaderboard.go`
- Create: `internal/search/leaderboard_test.go`

**Step 1: Write the failing test**

```go
// internal/search/leaderboard_test.go
package search

import (
	"errors"
	"testing"
)

func TestValidateLimitAcceptsAllowedValues(t *testing.T) {
	for _, limit := range []int{10, 20, 50} {
		if got, err := ValidateLimit(limit); err != nil || got != limit {
			t.Fatalf("ValidateLimit(%d) = %d, %v", limit, got, err)
		}
	}
}

func TestValidateLimitDefaultsZeroToTen(t *testing.T) {
	if got, err := ValidateLimit(0); err != nil || got != 10 {
		t.Fatalf("ValidateLimit(0) = %d, %v, want 10, nil", got, err)
	}
}

func TestValidateLimitRejectsUnsupportedValues(t *testing.T) {
	for _, limit := range []int{-1, 1, 11, 15, 100} {
		if _, err := ValidateLimit(limit); !errors.Is(err, ErrInvalidLimit) {
			t.Fatalf("ValidateLimit(%d) error = %v, want ErrInvalidLimit", limit, err)
		}
	}
}

func TestLeaderboardWindowsAreExactlyFourNaturalDayRanges(t *testing.T) {
	want := []LeaderboardWindow{Window1Day, Window3Day, Window7Day, Window30Day}
	got := AllLeaderboardWindows()
	if len(got) != len(want) {
		t.Fatalf("AllLeaderboardWindows() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("AllLeaderboardWindows()[%d] = %q, want %q", i, got[i], w)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/search/... -run 'TestValidateLimit|TestLeaderboardWindows' -v`
Expected: `FAIL` — compile error (`ValidateLimit`, `ErrInvalidLimit`, `LeaderboardWindow`, etc. undefined).

**Step 3: Write minimal implementation**

```go
// internal/search/leaderboard.go
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
```

Note: `maximumLeaderboardLimit` is currently unused by any logic (the allowed-set check does the real work) — it exists to self-document the range for readers. Go will not complain about an unused **const**, only unused local variables/imports, so this compiles fine.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/search/... -run 'TestValidateLimit|TestLeaderboardWindows' -v`
Expected: `PASS` for all four test functions.

**Step 5: Commit**

```bash
git add internal/search/leaderboard.go internal/search/leaderboard_test.go
git commit -m "feat: add leaderboard window/limit domain types"
```

---

### Task 3: Self-lookup domain types and `ValidateCredential`

**Files:**
- Create: `internal/search/selflookup.go`
- Create: `internal/search/selflookup_test.go`

**Step 1: Write the failing test**

```go
// internal/search/selflookup_test.go
package search

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateCredentialTrimsAndAccepts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain key", input: "sk-test-key-abc123", want: "sk-test-key-abc123"},
		{name: "surrounding whitespace", input: "  demo-key-alpha  ", want: "demo-key-alpha"},
		{name: "utf8 name", input: "密钥别名", want: "密钥别名"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateCredential(tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("ValidateCredential(%q) = %q, %v, want %q, nil", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestValidateCredentialRejectsEmptyAndOversizedInput(t *testing.T) {
	longInput := strings.Repeat("a", MaximumTextRunes+1)
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace only", input: "   "},
		{name: "single char", input: "a"},
		{name: "too long", input: longInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateCredential(tt.input); !errors.Is(err, ErrInvalidCredential) {
				t.Fatalf("ValidateCredential(%q) error = %v, want ErrInvalidCredential", tt.input, err)
			}
		})
	}
}
```

Note: this reuses `MinimumTextRunes`/`MaximumTextRunes` already defined in `internal/search/search.go` — same package, no import needed.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/search/... -run TestValidateCredential -v`
Expected: `FAIL` — compile error (`ValidateCredential`, `ErrInvalidCredential`, `SelfResult` undefined).

**Step 3: Write minimal implementation**

```go
// internal/search/selflookup.go
package search

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrInvalidCredential = errors.New("self-lookup credential is invalid")

// ValidateCredential trims and bounds-checks a self-lookup credential (either
// a full key value or a key name), reusing the same length bounds as the
// general search text field so the two code paths stay consistent.
func ValidateCredential(input string) (string, error) {
	value := strings.TrimSpace(input)
	if !utf8.ValidString(value) {
		return "", ErrInvalidCredential
	}
	runeCount := utf8.RuneCountInString(value)
	if runeCount < MinimumTextRunes || runeCount > MaximumTextRunes {
		return "", ErrInvalidCredential
	}
	return value, nil
}

// SelfResult is the single-key detail view returned to a caller who proved
// knowledge of the key's own value or name. It never carries the raw key or
// the internal database id — DailyUsage is populated server-side using the
// id resolved during lookup, so the frontend never needs (or receives) that
// id to fetch its own chart data.
type SelfResult struct {
	KeyMasked  string            `json:"keyMasked"`
	Name       string            `json:"name"`
	GroupName  string            `json:"groupName"`
	Quota      string            `json:"quota"`
	QuotaUsed  string            `json:"quotaUsed"`
	Status     string            `json:"status"`
	ExpiresAt  string            `json:"expiresAt"`
	TodayCost  string            `json:"todayCost"`
	DailyUsage []DailyUsagePoint `json:"dailyUsage"`
}
```

Note `DailyUsagePoint` is the same type `internal/postgres/daily.go` already scans into for `/api/key-usage` — this reuses it rather than defining a parallel shape.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/search/... -run TestValidateCredential -v`
Expected: `PASS` for both test functions.

**Step 5: Commit**

```bash
git add internal/search/selflookup.go internal/search/selflookup_test.go
git commit -m "feat: add self-lookup credential validation and result type"
```

---

### Module 1 checkpoint

Run the full package test before moving to Module 2:

```bash
go test ./internal/search/... -v 2>&1 | tail -40
go vet ./internal/search/...
```
Expected: every test in `internal/search` passes (old and new), `go vet` reports nothing.

Next: [`2026-08-09-plan-02-backend-repositories.md`](2026-08-09-plan-02-backend-repositories.md)
