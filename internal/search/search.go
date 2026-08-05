package search

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	MinimumTextRunes = 2
	MaximumTextRunes = 100
)

var (
	ErrUnsupportedTarget = errors.New("unsupported search target")
	ErrEmptyQuery        = errors.New("search query is required")
	ErrInvalidText       = errors.New("search text must be valid UTF-8")
	ErrTextTooShort      = errors.New("search text is too short")
	ErrTextTooLong       = errors.New("search text is too long")
)

type TargetType string

const TargetKey TargetType = "key"

type QueryMode uint8

const QueryModeText QueryMode = iota + 1

type Request struct {
	TargetType TargetType
	Query      string
}

// Query is constructed only through Validate so repository callers cannot
// supply unbounded text.
type Query struct {
	targetType TargetType
	mode       QueryMode
	text       string
}

func Validate(request Request) (Query, error) {
	if request.TargetType != TargetKey {
		return Query{}, ErrUnsupportedTarget
	}
	value := strings.TrimSpace(request.Query)
	if value == "" {
		return Query{}, ErrEmptyQuery
	}
	if !utf8.ValidString(value) {
		return Query{}, ErrInvalidText
	}
	runeCount := utf8.RuneCountInString(value)
	if runeCount < MinimumTextRunes {
		return Query{}, ErrTextTooShort
	}
	if runeCount > MaximumTextRunes {
		return Query{}, ErrTextTooLong
	}
	return Query{targetType: TargetKey, mode: QueryModeText, text: value}, nil
}

func (query Query) TargetType() TargetType {
	return query.targetType
}

func (query Query) Mode() QueryMode {
	return query.mode
}

func (query Query) Text() string {
	return query.text
}

func (query Query) Valid() bool {
	return query.targetType == TargetKey && query.mode == QueryModeText && validBoundedText(query.text)
}

// KeyResult carries the key listing fields that mirror the Sub2API
// /api/v1/keys endpoint value derivations.
type KeyResult struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	GroupName         string `json:"groupName"`
	CurrentConcurrency int64 `json:"currentConcurrency"`
	TodayCost         string `json:"todayCost"`
	Total30dCost      string `json:"total30dCost"`
	Quota             string `json:"quota"`
	QuotaUsed         string `json:"quotaUsed"`
	LastUsedAt        string `json:"lastUsedAt"`
	ExpiresAt         string `json:"expiresAt"`
	Status            string `json:"status"`
	CreatedAt         string `json:"createdAt"`
}

type Results struct {
	Keys []KeyResult
}

func validBoundedText(value string) bool {
	if value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return false
	}
	runeCount := utf8.RuneCountInString(value)
	return runeCount >= MinimumTextRunes && runeCount <= MaximumTextRunes
}
