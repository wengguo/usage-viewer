package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelfLookupRepositoryRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name       string
		repository *SelfLookupRepository
		credential string
	}{
		{name: "nil repository", repository: nil, credential: "sk-example"},
		{name: "nil pool", repository: NewSelfLookupRepository(nil, time.Second), credential: "sk-example"},
		{name: "invalid timeout", repository: NewSelfLookupRepository(nil, 0), credential: "sk-example"},
		{name: "empty credential", repository: NewSelfLookupRepository(nil, time.Second), credential: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, gotErr := tt.repository.Lookup(context.Background(), tt.credential)
			if !errors.Is(gotErr, ErrSelfLookupUnavailable) {
				t.Fatalf("Lookup() error = %v, want ErrSelfLookupUnavailable", gotErr)
			}
		})
	}
}

func TestSelfLookupSQLIsStaticBoundedAndExactMatchOnly(t *testing.T) {
	normalized := strings.ToLower(selfLookupSQL)
	for _, required := range []string{
		"select ",
		"from public.api_keys",
		"left join public.groups",
		"deleted_at is null",
		"api_key.key = $1",
		"api_key.name = $1",
		" or ",
		"order by",
		"limit 1",
	} {
		if !strings.Contains(normalized, required) {
			t.Fatalf("self-lookup SQL missing %q: %s", required, selfLookupSQL)
		}
	}
	for _, forbidden := range []string{"select *", " insert ", " update ", " delete ", "ilike"} {
		if strings.Contains(" "+normalized+" ", forbidden) {
			t.Fatalf("self-lookup SQL contains forbidden fragment %q (must be exact match, not fuzzy)", forbidden)
		}
	}
}
