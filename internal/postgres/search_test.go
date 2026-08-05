package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/search"
)

func TestSearchSQLIsStaticBoundedAndProjectionSafe(t *testing.T) {
	tests := []struct {
		name       string
		statement  string
		projection string
		limit      string
	}{
		{
			name:       "key text",
			statement:  keyByTextSQL,
			projection: "api_key.id, api_key.name, coalesce(grp.name, ''), api_key.quota, api_key.quota_used, api_key.expires_at, api_key.status, api_key.created_at, sum(actual_cost)",
			limit:      "limit 20",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := strings.ToLower(tt.statement)
			for _, required := range []string{"select ", "deleted_at is null", tt.limit, "from public.api_keys", "left join public.groups"} {
				if !strings.Contains(normalized, required) {
					t.Fatalf("query missing %q: %s", required, tt.statement)
				}
			}
			for _, forbidden := range []string{"select *", " insert ", " update ", " delete "} {
				if strings.Contains(" "+normalized+" ", forbidden) {
					t.Fatalf("query contains forbidden fragment %q", forbidden)
				}
			}
		})
	}
}

func TestKeySearchSQLUsesBoundArgumentsAndDeterministicOrdering(t *testing.T) {
	normalized := strings.ToLower(keyByTextSQL)
	for _, required := range []string{
		"$1", "$2", "$3", "$4", "$5",
		"order by", "id asc", " escape '\\'", "ilike",
		"api_key.name ilike", "api_key.key ilike", " or ",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("key query missing %q", required)
		}
	}
	if strings.Count(normalized, "ilike") < 2 {
		t.Fatalf("key query must match both name and key")
	}
	if strings.Count(normalized, "select ") < 2 {
		t.Fatalf("key query must contain subselects for usage aggregation")
	}
}

func TestEscapeLikeTextTreatsWildcardsLiterally(t *testing.T) {
	if got, want := escapeLikeText(`a%b_c\d`), `a\%b\_c\\d`; got != want {
		t.Fatalf("escapeLikeText() = %q, want %q", got, want)
	}
}

func TestSearchRepositoryRejectsInvalidDependenciesAndQueryWithoutDetails(t *testing.T) {
	valid, err := search.Validate(search.Request{TargetType: search.TargetKey, Query: "sample"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		repository *SearchRepository
		query      search.Query
	}{
		{name: "nil repository", repository: nil, query: valid},
		{name: "nil pool", repository: NewSearchRepository(nil, time.Second, nil), query: valid},
		{name: "invalid timeout", repository: NewSearchRepository(nil, 0, nil), query: valid},
		{name: "invalid query", repository: NewSearchRepository(nil, time.Second, nil), query: search.Query{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotErr := tt.repository.Search(context.Background(), tt.query)
			if !errors.Is(gotErr, ErrSearchUnavailable) || gotErr.Error() != "search is temporarily unavailable" {
				t.Fatalf("Search() error = %v", gotErr)
			}
		})
	}
}
