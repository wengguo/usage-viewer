package postgres

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestCurrentContractContainsExactSafeColumns(t *testing.T) {
	contract := CurrentContract()
	wantRelations := []RelationContract{
		{Schema: "public", Name: "api_keys", Relkind: "r"},
		{Schema: "public", Name: "groups", Relkind: "r"},
		{Schema: "public", Name: "usage_logs", Relkind: "r"},
	}
	if len(contract.Relations) != len(wantRelations) {
		t.Fatalf("contract relation count = %d, want %d", len(contract.Relations), len(wantRelations))
	}
	for i, want := range wantRelations {
		if got := contract.Relations[i]; got != want {
			t.Errorf("contract relation %d = %#v, want %#v", i, got, want)
		}
	}
	if len(contract.Columns) != 17 {
		t.Fatalf("contract column count = %d, want 17", len(contract.Columns))
	}

	wantByTable := map[string]int{"api_keys": 11, "groups": 2, "usage_logs": 4}
	gotByTable := make(map[string]int)
	seen := make(map[string]bool)
	for _, column := range contract.Columns {
		if column.Schema != "public" {
			t.Errorf("column schema = %q, want public", column.Schema)
		}
		gotByTable[column.Table]++
		key := column.Schema + "." + column.Table + "." + column.Column
		if seen[key] {
			t.Errorf("duplicate contract column %q", key)
		}
		seen[key] = true
	}
	for table, want := range wantByTable {
		if got := gotByTable[table]; got != want {
			t.Errorf("%s column count = %d, want %d", table, got, want)
		}
	}
	if len(gotByTable) != len(wantByTable) {
		t.Fatalf("contract contains unexpected table set: %#v", gotByTable)
	}
}

func TestValidateAdmissionRejectsUnexpectedRequiredRelationKinds(t *testing.T) {
	unexpectedKinds := []string{"p", "v", "m", "f", "c", "S", "i", "I", "t", "", "future-kind-secret"}
	for relationIndex := range CurrentContract().Relations {
		for _, relkind := range unexpectedKinds {
			name := CurrentContract().Relations[relationIndex].Name + "/" + relkind
			if relkind == "" {
				name += "empty"
			}
			t.Run(name, func(t *testing.T) {
				snapshot := acceptedSnapshot()
				snapshot.RequiredRelations[relationIndex].Relkind = relkind
				err := ValidateAdmission("expected-role-secret", CurrentContract(), snapshot)
				assertAdmissionCode(t, err, diagnostics.CodeSchemaCompatibility)
				assertAdmissionSentinelsAbsent(t, err,
					"expected-role-secret",
					CurrentContract().Relations[relationIndex].Name,
					"future-kind-secret",
				)
			})
		}
	}
}

func TestValidateAdmissionRejectsInvalidRequiredRelationEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdmissionSnapshot)
	}{
		{name: "missing relation", mutate: func(s *AdmissionSnapshot) { s.RequiredRelations = s.RequiredRelations[1:] }},
		{name: "extra relation", mutate: func(s *AdmissionSnapshot) {
			s.RequiredRelations = append(s.RequiredRelations, ObservedRelation{Schema: "public", Name: "extra-relation-secret", Relkind: "r"})
		}},
		{name: "duplicate relation", mutate: func(s *AdmissionSnapshot) {
			s.RequiredRelations = append(s.RequiredRelations, s.RequiredRelations[0])
		}},
		{name: "blank schema", mutate: func(s *AdmissionSnapshot) { s.RequiredRelations[0].Schema = "" }},
		{name: "blank name", mutate: func(s *AdmissionSnapshot) { s.RequiredRelations[0].Name = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := acceptedSnapshot()
			tt.mutate(&snapshot)
			err := ValidateAdmission("expected-role-secret", CurrentContract(), snapshot)
			assertAdmissionCode(t, err, diagnostics.CodeSchemaCompatibility)
			assertAdmissionSentinelsAbsent(t, err, "expected-role-secret", "extra-relation-secret", "accounts", "users", "usage_logs")
		})
	}
}

func TestValidateAdmissionRejectsInvalidRelationContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Contract)
	}{
		{name: "empty contract", mutate: func(c *Contract) { c.Relations = nil }},
		{name: "missing contract relation", mutate: func(c *Contract) { c.Relations = c.Relations[1:] }},
		{name: "duplicate contract relation", mutate: func(c *Contract) { c.Relations = append(c.Relations, c.Relations[0]) }},
		{name: "blank contract schema", mutate: func(c *Contract) { c.Relations[0].Schema = "" }},
		{name: "blank contract name", mutate: func(c *Contract) { c.Relations[0].Name = "" }},
		{name: "blank contract kind", mutate: func(c *Contract) { c.Relations[0].Relkind = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract := CurrentContract()
			tt.mutate(&contract)
			err := ValidateAdmission("expected-role-secret", contract, acceptedSnapshot())
			assertAdmissionCode(t, err, diagnostics.CodeSchemaCompatibility)
			assertAdmissionSentinelsAbsent(t, err, "expected-role-secret", "accounts", "users", "usage_logs")
		})
	}
}

func TestValidateAdmissionAcceptsExactLeastPrivilegeSnapshot(t *testing.T) {
	if err := ValidateAdmission("expected-role-secret", CurrentContract(), acceptedSnapshot()); err != nil {
		t.Fatalf("ValidateAdmission() error = %v", err)
	}
}

func TestValidateAdmissionRejectsDangerousRoleStates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdmissionSnapshot)
	}{
		{name: "role cannot login", mutate: func(s *AdmissionSnapshot) { s.RoleCanLogin = false }},
		{name: "superuser", mutate: func(s *AdmissionSnapshot) { s.RoleSuper = true }},
		{name: "inheritance", mutate: func(s *AdmissionSnapshot) { s.RoleInherit = true }},
		{name: "create role", mutate: func(s *AdmissionSnapshot) { s.RoleCreateRole = true }},
		{name: "create database", mutate: func(s *AdmissionSnapshot) { s.RoleCreateDB = true }},
		{name: "replication", mutate: func(s *AdmissionSnapshot) { s.RoleReplication = true }},
		{name: "bypass RLS", mutate: func(s *AdmissionSnapshot) { s.RoleBypassRLS = true }},
		{name: "missing database connect", mutate: func(s *AdmissionSnapshot) { s.DatabaseConnect = false }},
		{name: "missing public schema usage", mutate: func(s *AdmissionSnapshot) { s.PublicSchemaUsage = false }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := acceptedSnapshot()
			tt.mutate(&snapshot)
			assertAdmissionCode(t, ValidateAdmission("expected-role-secret", CurrentContract(), snapshot), diagnostics.CodeDatabasePrivilege)
		})
	}
}

func TestValidateAdmissionRejectsEveryForbiddenAuthorityCount(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdmissionSnapshot)
	}{
		{name: "membership", mutate: func(s *AdmissionSnapshot) { s.MembershipCount = 1 }},
		{name: "database ownership", mutate: func(s *AdmissionSnapshot) { s.OwnedDatabaseCount = 1 }},
		{name: "schema ownership", mutate: func(s *AdmissionSnapshot) { s.OwnedSchemaCount = 1 }},
		{name: "relation ownership", mutate: func(s *AdmissionSnapshot) { s.OwnedRelationCount = 1 }},
		{name: "routine ownership", mutate: func(s *AdmissionSnapshot) { s.OwnedRoutineCount = 1 }},
		{name: "database create or temporary", mutate: func(s *AdmissionSnapshot) { s.DatabaseCreateOrTempCount = 1 }},
		{name: "schema create", mutate: func(s *AdmissionSnapshot) { s.SchemaCreateCount = 1 }},
		{name: "table or column write", mutate: func(s *AdmissionSnapshot) { s.TableWritePrivilegeCount = 1 }},
		{name: "sequence privilege", mutate: func(s *AdmissionSnapshot) { s.SequencePrivilegeCount = 1 }},
		{name: "grant option", mutate: func(s *AdmissionSnapshot) { s.GrantOptionCount = 1 }},
		{name: "security definer execution", mutate: func(s *AdmissionSnapshot) { s.SecurityDefinerExecuteCount = 1 }},
		{name: "large object write", mutate: func(s *AdmissionSnapshot) { s.LargeObjectWriteCount = 1 }},
		{name: "broad select", mutate: func(s *AdmissionSnapshot) { s.BroadSelectPrivilegeCount = 1 }},
		{name: "unexpected selectable column", mutate: func(s *AdmissionSnapshot) { s.UnexpectedSelectPrivilegeCount = 1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := acceptedSnapshot()
			tt.mutate(&snapshot)
			assertAdmissionCode(t, ValidateAdmission("expected-role-secret", CurrentContract(), snapshot), diagnostics.CodeDatabasePrivilege)
		})
	}
}

func TestValidateAdmissionRejectsRoleMismatchAndMissingSelect(t *testing.T) {
	tests := []struct {
		name         string
		expectedRole string
		mutate       func(*AdmissionSnapshot)
	}{
		{name: "empty expected role", expectedRole: "", mutate: func(*AdmissionSnapshot) {}},
		{name: "mismatched current role", expectedRole: "other-role-secret", mutate: func(*AdmissionSnapshot) {}},
		{name: "missing required column select", expectedRole: "expected-role-secret", mutate: func(s *AdmissionSnapshot) {
			s.RequiredColumns[0].Selectable = false
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := acceptedSnapshot()
			tt.mutate(&snapshot)
			err := ValidateAdmission(tt.expectedRole, CurrentContract(), snapshot)
			assertAdmissionCode(t, err, diagnostics.CodeDatabasePrivilege)
			assertAdmissionSentinelsAbsent(t, err, "expected-role-secret", "other-role-secret", snapshot.RequiredColumns[0].Table, snapshot.RequiredColumns[0].Column)
		})
	}
}

func TestValidateAdmissionRejectsSchemaMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AdmissionSnapshot)
	}{
		{name: "missing column", mutate: func(s *AdmissionSnapshot) { s.RequiredColumns = s.RequiredColumns[1:] }},
		{name: "extra column", mutate: func(s *AdmissionSnapshot) {
			s.RequiredColumns = append(s.RequiredColumns, ObservedColumn{Schema: "public", Table: "extra-table-secret", Column: "extra-column-secret", UDTName: "text", Selectable: true})
		}},
		{name: "duplicate column", mutate: func(s *AdmissionSnapshot) { s.RequiredColumns = append(s.RequiredColumns, s.RequiredColumns[0]) }},
		{name: "wrong type", mutate: func(s *AdmissionSnapshot) { s.RequiredColumns[0].UDTName = "wrong-type-secret" }},
		{name: "wrong nullability", mutate: func(s *AdmissionSnapshot) { s.RequiredColumns[0].Nullable = !s.RequiredColumns[0].Nullable }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := acceptedSnapshot()
			tt.mutate(&snapshot)
			err := ValidateAdmission("expected-role-secret", CurrentContract(), snapshot)
			assertAdmissionCode(t, err, diagnostics.CodeSchemaCompatibility)
			assertAdmissionSentinelsAbsent(t, err, "extra-table-secret", "extra-column-secret", "wrong-type-secret", "accounts", "id")
		})
	}
}

func TestValidateAdmissionRequiresReadOnlyDefault(t *testing.T) {
	snapshot := acceptedSnapshot()
	snapshot.DefaultTransactionReadOnly = false
	assertAdmissionCode(t, ValidateAdmission("expected-role-secret", CurrentContract(), snapshot), diagnostics.CodeDatabaseReadOnly)
}

func acceptedSnapshot() AdmissionSnapshot {
	contract := CurrentContract()
	relations := make([]ObservedRelation, len(contract.Relations))
	for i, relation := range contract.Relations {
		relations[i] = ObservedRelation{
			Schema:  relation.Schema,
			Name:    relation.Name,
			Relkind: relation.Relkind,
		}
	}
	columns := make([]ObservedColumn, len(contract.Columns))
	for i, column := range contract.Columns {
		columns[i] = ObservedColumn{
			Schema:     column.Schema,
			Table:      column.Table,
			Column:     column.Column,
			UDTName:    column.UDTName,
			Nullable:   column.Nullable,
			Selectable: true,
		}
	}
	return AdmissionSnapshot{
		CurrentRole:                "expected-role-secret",
		RoleCanLogin:               true,
		DefaultTransactionReadOnly: true,
		DatabaseConnect:            true,
		PublicSchemaUsage:          true,
		RequiredRelations:          relations,
		RequiredColumns:            columns,
	}
}

func assertAdmissionCode(t *testing.T, err error, want diagnostics.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected diagnostic %q, got nil", want)
	}
	if got := diagnostics.CodeOf(err); got != want {
		t.Fatalf("diagnostic code = %q, want %q; error = %v", got, want, err)
	}
}

func assertAdmissionSentinelsAbsent(t *testing.T, err error, sentinels ...string) {
	t.Helper()
	if err == nil {
		return
	}
	for _, sentinel := range sentinels {
		if strings.Contains(err.Error(), sentinel) {
			t.Errorf("diagnostic disclosed sentinel %q", sentinel)
		}
	}
}
