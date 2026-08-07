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
	for name, statement := range map[string]string{
		"browse id descending": keyBrowseByIDDescSQL, "browse id ascending": keyBrowseByIDAscSQL, "browse today desc": keyBrowseByTodayCostDescSQL,
		"browse today asc": keyBrowseByTodayCostAscSQL, "browse thirty desc": keyBrowseByTotal30dCostDescSQL,
		"browse thirty asc": keyBrowseByTotal30dCostAscSQL, "text id descending": keyByTextSQL, "text id ascending": keyTextByIDAscSQL,
		"text today desc": keyTextByTodayCostDescSQL, "text today asc": keyTextByTodayCostAscSQL,
		"text thirty desc": keyTextByTotal30dCostDescSQL, "text thirty asc": keyTextByTotal30dCostAscSQL,
	} {
		t.Run(name, func(t *testing.T) {
			normalized := strings.ToLower(statement)
			for _, required := range []string{"select ", "deleted_at is null", "limit 20", "from public.api_keys", "left join public.groups", "offset $"} {
				if !strings.Contains(normalized, required) {
					t.Fatalf("query missing %q: %s", required, statement)
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
		"$1", "$2", "$3", "$4", "$5", "order by", "id desc", " escape '\\'", "ilike",
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

func TestCostSearchSQLOrdersNumericallyWithStableIDTieBreak(t *testing.T) {
	for name, test := range map[string]struct {
		statement string
		order     string
	}{
		"today descending":  {keyBrowseByTodayCostDescSQL, "order by today_cost desc, id desc"},
		"today ascending":   {keyTextByTodayCostAscSQL, "order by today_cost asc, id desc"},
		"thirty descending": {keyBrowseByTotal30dCostDescSQL, "order by total_30d_cost desc, id desc"},
		"thirty ascending":  {keyTextByTotal30dCostAscSQL, "order by total_30d_cost asc, id desc"},
	} {
		normalized := strings.ToLower(test.statement)
		if !strings.Contains(normalized, test.order) || strings.Contains(normalized, "order by today_cost::text") || strings.Contains(normalized, "order by total_30d_cost::text") {
			t.Errorf("%s does not preserve numeric cost ordering and stable IDs: %s", name, test.statement)
		}
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
