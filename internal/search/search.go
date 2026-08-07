package search

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	MinimumTextRunes = 2
	MaximumTextRunes = 100
	PageSize         = 20
)

var (
	ErrUnsupportedTarget = errors.New("unsupported search target")
	ErrInvalidText       = errors.New("search text must be valid UTF-8")
	ErrTextTooShort      = errors.New("search text is too short")
	ErrTextTooLong       = errors.New("search text is too long")
	ErrInvalidPage       = errors.New("search page is invalid")
	ErrInvalidSortBy     = errors.New("search sort field is invalid")
	ErrInvalidSortOrder  = errors.New("search sort direction is invalid")
)

type TargetType string

const TargetKey TargetType = "key"

type SortBy string

const (
	SortByID           SortBy = "id"
	SortByTodayCost    SortBy = "todayCost"
	SortByTotal30dCost SortBy = "total30dCost"
)

type SortDirection string

const (
	SortDirectionAscending  SortDirection = "asc"
	SortDirectionDescending SortDirection = "desc"
)

type QueryMode uint8

const QueryModeText QueryMode = iota + 1

const QueryModeBrowse QueryMode = QueryModeText + 1

type Request struct {
	TargetType    TargetType
	Query         string
	Page          int
	SortBy        SortBy
	SortDirection SortDirection
}

// Query is constructed only through Validate so repository callers cannot
// supply unbounded text.
type Query struct {
	targetType    TargetType
	mode          QueryMode
	text          string
	page          int
	sortBy        SortBy
	sortDirection SortDirection
}

func Validate(request Request) (Query, error) {
	if request.TargetType != TargetKey {
		return Query{}, ErrUnsupportedTarget
	}
	page := request.Page
	if page == 0 {
		page = 1
	}
	if page < 1 || page > maximumPage() {
		return Query{}, ErrInvalidPage
	}
	sortBy := request.SortBy
	if sortBy == "" {
		sortBy = SortByID
	}
	if sortBy != SortByID && sortBy != SortByTodayCost && sortBy != SortByTotal30dCost {
		return Query{}, ErrInvalidSortBy
	}
	sortDirection := request.SortDirection
	if sortDirection == "" {
		sortDirection = SortDirectionDescending
	}
	if sortDirection != SortDirectionAscending && sortDirection != SortDirectionDescending {
		return Query{}, ErrInvalidSortOrder
	}
	value := strings.TrimSpace(request.Query)
	if value == "" {
		return Query{targetType: TargetKey, mode: QueryModeBrowse, page: page, sortBy: sortBy, sortDirection: sortDirection}, nil
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
	return Query{targetType: TargetKey, mode: QueryModeText, text: value, page: page, sortBy: sortBy, sortDirection: sortDirection}, nil
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

func (query Query) Page() int {
	return query.page
}

func (query Query) SortBy() SortBy {
	return query.sortBy
}

func (query Query) SortDirection() SortDirection {
	return query.sortDirection
}

func (query Query) Valid() bool {
	if query.targetType != TargetKey || query.page < 1 || query.page > maximumPage() ||
		(query.sortBy != SortByID && query.sortBy != SortByTodayCost && query.sortBy != SortByTotal30dCost) ||
		(query.sortDirection != SortDirectionAscending && query.sortDirection != SortDirectionDescending) {
		return false
	}
	switch query.mode {
	case QueryModeBrowse:
		return query.text == ""
	case QueryModeText:
		return validBoundedText(query.text)
	default:
		return false
	}
}

// KeyResult carries the key listing fields that mirror the Sub2API
// /api/v1/keys endpoint value derivations.
type KeyResult struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	GroupName          string `json:"groupName"`
	CurrentConcurrency int64  `json:"currentConcurrency"`
	TodayCost          string `json:"todayCost"`
	Total30dCost       string `json:"total30dCost"`
	Quota              string `json:"quota"`
	QuotaUsed          string `json:"quotaUsed"`
	LastUsedAt         string `json:"lastUsedAt"`
	ExpiresAt          string `json:"expiresAt"`
	Status             string `json:"status"`
	CreatedAt          string `json:"createdAt"`
}

type Results struct {
	Keys     []KeyResult
	Page     int
	PageSize int
	Total    int64
}

func validBoundedText(value string) bool {
	if value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return false
	}
	runeCount := utf8.RuneCountInString(value)
	return runeCount >= MinimumTextRunes && runeCount <= MaximumTextRunes
}

func maximumPage() int {
	return int(^uint(0)>>1)/PageSize + 1
}
