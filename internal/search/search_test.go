package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValidateAcceptsKeyNameQuery(t *testing.T) {
	tests := []struct {
		name     string
		request  Request
		wantMode QueryMode
		wantText string
	}{
		{name: "key name", request: Request{TargetType: TargetKey, Query: "  my-key  "}, wantMode: QueryModeText, wantText: "my-key"},
		{name: "numeric text is still text", request: Request{TargetType: TargetKey, Query: "12345"}, wantMode: QueryModeText, wantText: "12345"},
		{name: "utf8 name", request: Request{TargetType: TargetKey, Query: "  密钥  "}, wantMode: QueryModeText, wantText: "密钥"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := Validate(tt.request)
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !query.Valid() {
				t.Fatal("Validate() returned an invalid Query")
			}
			if query.TargetType() != TargetKey || query.Mode() != tt.wantMode || query.Text() != tt.wantText ||
				query.Page() != 1 || query.SortBy() != SortByID || query.SortDirection() != SortDirectionDescending {
				t.Fatalf("query = target %q mode %d text %q", query.TargetType(), query.Mode(), query.Text())
			}
		})
	}
}

func TestValidateRejectsInvalidRequestsWithoutEchoingInput(t *testing.T) {
	const sensitiveInput = "sensitive-search-sentinel"
	tests := []struct {
		name    string
		request Request
		wantErr error
	}{
		{name: "unsupported target", request: Request{TargetType: TargetType("account"), Query: sensitiveInput}, wantErr: ErrUnsupportedTarget},
		{name: "short", request: Request{TargetType: TargetKey, Query: "x"}, wantErr: ErrTextTooShort},
		{name: "short multibyte", request: Request{TargetType: TargetKey, Query: "用"}, wantErr: ErrTextTooShort},
		{name: "long", request: Request{TargetType: TargetKey, Query: strings.Repeat("界", MaximumTextRunes+1)}, wantErr: ErrTextTooLong},
		{name: "invalid UTF-8", request: Request{TargetType: TargetKey, Query: string([]byte{'a', 0xff})}, wantErr: ErrInvalidText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := Validate(tt.request)
			if !errors.Is(err, tt.wantErr) || err != tt.wantErr {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
			if query.Valid() {
				t.Fatal("rejected request returned a valid Query")
			}
			if strings.Contains(err.Error(), sensitiveInput) {
				t.Fatalf("validation error echoed input: %q", err)
			}
		})
	}
}

func TestValidateAcceptsBrowseAndPaginationOptions(t *testing.T) {
	query, err := Validate(Request{TargetType: TargetKey, Page: 3, SortBy: SortByTodayCost, SortDirection: SortDirectionAscending})
	if err != nil || !query.Valid() {
		t.Fatalf("Validate() error = %v", err)
	}
	if query.Mode() != QueryModeBrowse || query.Text() != "" || query.Page() != 3 ||
		query.SortBy() != SortByTodayCost || query.SortDirection() != SortDirectionAscending {
		t.Fatalf("unexpected browse query: %#v", query)
	}
}

func TestValidateRejectsInvalidPaginationAndSort(t *testing.T) {
	for _, tt := range []struct {
		request Request
		wantErr error
	}{
		{request: Request{TargetType: TargetKey, Page: -1}, wantErr: ErrInvalidPage},
		{request: Request{TargetType: TargetKey, SortBy: SortBy("name")}, wantErr: ErrInvalidSortBy},
		{request: Request{TargetType: TargetKey, SortDirection: SortDirection("sideways")}, wantErr: ErrInvalidSortOrder},
	} {
		_, err := Validate(tt.request)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("Validate(%#v) error = %v, want %v", tt.request, err, tt.wantErr)
		}
	}
}

func TestValidateUsesUTF8RuneBounds(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "minimum", value: strings.Repeat("界", MinimumTextRunes)},
		{name: "maximum", value: strings.Repeat("界", MaximumTextRunes)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			query, err := Validate(Request{TargetType: TargetKey, Query: tt.value})
			if err != nil || query.Text() != tt.value {
				t.Fatalf("Validate() query text length = %d, error = %v", len([]rune(query.Text())), err)
			}
		})
	}
}

func TestZeroQueryIsInvalid(t *testing.T) {
	if (Query{}).Valid() {
		t.Fatal("zero Query must not be valid")
	}
}

func TestResultTypesExposeOnlyApprovedIdentityFields(t *testing.T) {
	assertFields(t, reflect.TypeOf(KeyResult{}), []string{"ID", "Name", "GroupName", "CurrentConcurrency", "TodayCost", "Total30dCost", "Quota", "QuotaUsed", "LastUsedAt", "ExpiresAt", "Status", "CreatedAt"})
}

func assertFields(t *testing.T, value reflect.Type, want []string) {
	t.Helper()
	got := make([]string, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		got[index] = value.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields = %v, want %v", value.Name(), got, want)
	}
}
