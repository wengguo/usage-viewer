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
