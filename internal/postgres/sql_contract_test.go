package postgres

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestProductionSQLContract(t *testing.T) {
	registry := productionSQLRegistry()
	if len(registry) != 6 {
		t.Fatalf("production SQL registry count = %d, want 6", len(registry))
	}

	wantNames := []string{"authority_evidence", "column_evidence", "connectivity_probe", "read_only_evidence", "relation_evidence", "role_evidence"}
	gotNames := make([]string, 0, len(registry))
	seenStatements := make(map[string]string)
	for name, statement := range registry {
		gotNames = append(gotNames, name)
		t.Run(name, func(t *testing.T) {
			normalized, statements, err := normalizeSQL(statement)
			if err != nil {
				t.Fatalf("normalize SQL: %v", err)
			}
			if statements != 1 {
				t.Fatalf("statement count = %d, want 1", statements)
			}
			if !strings.HasPrefix(normalized, "select ") && !strings.HasPrefix(normalized, "with ") {
				t.Fatalf("statement does not begin with SELECT or WITH: %q", normalized)
			}
			assertNoForbiddenSQL(t, normalized)
			if previous, duplicate := seenStatements[normalized]; duplicate {
				t.Fatalf("statement duplicates registry entry %q", previous)
			}
			seenStatements[normalized] = name
		})
	}
	sort.Strings(gotNames)
	if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("registry names = %v, want %v", gotNames, wantNames)
	}
}

func TestRelationEvidenceSQLContract(t *testing.T) {
	normalized, statements, err := normalizeSQL(relationEvidenceSQL)
	if err != nil {
		t.Fatal(err)
	}
	if statements != 1 {
		t.Fatalf("statement count = %d, want 1", statements)
	}
	for _, fragment := range []string{
		"with required_relations(schema_name, relation_name, position) as",
		"pg_catalog.unnest($1::text[], $2::text[])",
		"with ordinality",
		"left join pg_catalog.pg_namespace",
		"left join pg_catalog.pg_class",
		"relation.relkind::text",
		"coalesce(relation.relkind::text, '')",
		"order by required.position",
	} {
		if !strings.Contains(normalized, fragment) {
			t.Errorf("relation evidence SQL missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"join pg_catalog.pg_attribute",
		"has_column_privilege",
		"from accounts",
		"from users",
		"from usage_logs",
	} {
		if strings.Contains(normalized, forbidden) {
			t.Errorf("relation evidence SQL contains forbidden fragment %q", forbidden)
		}
	}
	assertNoForbiddenSQL(t, normalized)
}

func TestProductionSQLReadsOnlyCatalogMetadata(t *testing.T) {
	for name, statement := range productionSQLRegistry() {
		t.Run(name, func(t *testing.T) {
			normalized, _, err := normalizeSQL(statement)
			if err != nil {
				t.Fatal(err)
			}
			for _, entityRelation := range []string{"from accounts", "from users", "from usage_logs", "join accounts", "join users", "join usage_logs"} {
				if strings.Contains(normalized, entityRelation) {
					t.Errorf("preflight SQL reads entity relation through %q", entityRelation)
				}
			}
		})
	}
}

func TestDiagnosticMapping(t *testing.T) {
	const rawCause = "raw-pgx-error-secret-sentinel"
	tests := []struct {
		name  string
		stage diagnosticStage
		code  diagnostics.Code
	}{
		{name: "connectivity", stage: stageConnectivity, code: diagnostics.CodeDatabaseConnectivity},
		{name: "role", stage: stageRole, code: diagnostics.CodeDatabasePrivilege},
		{name: "authority", stage: stageAuthority, code: diagnostics.CodeDatabasePrivilege},
		{name: "read only", stage: stageReadOnly, code: diagnostics.CodeDatabaseReadOnly},
		{name: "schema", stage: stageSchema, code: diagnostics.CodeSchemaCompatibility},
		{name: "unknown", stage: diagnosticStage(255), code: diagnostics.CodeServer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := diagnosticForStage(tt.stage, errors.New(rawCause))
			if got := diagnostics.CodeOf(err); got != tt.code {
				t.Errorf("diagnostic code = %q, want %q", got, tt.code)
			}
			if strings.Contains(err.Error(), rawCause) {
				t.Fatal("diagnostic disclosed raw pgx cause")
			}
		})
	}
}

func productionSQLRegistry() map[string]string {
	return map[string]string{
		"connectivity_probe": connectivityProbeSQL,
		"role_evidence":      roleEvidenceSQL,
		"read_only_evidence": readOnlyEvidenceSQL,
		"authority_evidence": authorityEvidenceSQL,
		"relation_evidence":  relationEvidenceSQL,
		"column_evidence":    columnEvidenceSQL,
	}
}

func normalizeSQL(statement string) (string, int, error) {
	var output strings.Builder
	statements := 1
	for i := 0; i < len(statement); {
		switch {
		case i+1 < len(statement) && statement[i:i+2] == "--":
			i += 2
			for i < len(statement) && statement[i] != '\n' {
				i++
			}
			output.WriteByte(' ')
		case i+1 < len(statement) && statement[i:i+2] == "/*":
			end := strings.Index(statement[i+2:], "*/")
			if end < 0 {
				return "", 0, errors.New("unterminated block comment")
			}
			i += end + 4
			output.WriteByte(' ')
		case statement[i] == '\'':
			output.WriteString("''")
			i++
			closed := false
			for i < len(statement) {
				if statement[i] != '\'' {
					i++
					continue
				}
				if i+1 < len(statement) && statement[i+1] == '\'' {
					i += 2
					continue
				}
				i++
				closed = true
				break
			}
			if !closed {
				return "", 0, errors.New("unterminated string literal")
			}
		case statement[i] == ';':
			if strings.TrimSpace(statement[i+1:]) != "" {
				statements++
			}
			output.WriteByte(' ')
			i++
		default:
			output.WriteByte(statement[i])
			i++
		}
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(output.String())), " ")
	if normalized == "" {
		return "", 0, errors.New("empty SQL")
	}
	return normalized, statements, nil
}

func assertNoForbiddenSQL(t *testing.T, normalized string) {
	t.Helper()
	forbiddenWords := []string{
		"insert", "update", "delete", "merge", "truncate", "create", "alter", "drop",
		"grant", "revoke", "copy", "call", "do", "lock",
	}
	for _, word := range forbiddenWords {
		pattern := regexp.MustCompile(`\b` + word + `\b`)
		if pattern.MatchString(normalized) {
			t.Errorf("SQL contains forbidden executable word %q", word)
		}
	}
	for _, fragment := range []string{"for update", "for share", "pg_advisory", "nextval", "setval", "set_config", "dblink"} {
		if strings.Contains(normalized, fragment) {
			t.Errorf("SQL contains forbidden executable fragment %q", fragment)
		}
	}
}
