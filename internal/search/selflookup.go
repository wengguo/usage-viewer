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
