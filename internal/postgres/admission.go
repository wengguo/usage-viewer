package postgres

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

type ObservedColumn struct {
	Schema     string
	Table      string
	Column     string
	UDTName    string
	Nullable   bool
	Selectable bool
}

type ObservedRelation struct {
	Schema  string
	Name    string
	Relkind string
}

type AdmissionSnapshot struct {
	CurrentRole     string
	RoleCanLogin    bool
	RoleSuper       bool
	RoleInherit     bool
	RoleCreateRole  bool
	RoleCreateDB    bool
	RoleReplication bool
	RoleBypassRLS   bool

	DefaultTransactionReadOnly bool
	DatabaseConnect            bool
	PublicSchemaUsage          bool

	MembershipCount                int64
	OwnedDatabaseCount             int64
	OwnedSchemaCount               int64
	OwnedRelationCount             int64
	DatabaseCreateOrTempCount      int64
	SchemaCreateCount              int64
	TableWritePrivilegeCount       int64
	SequencePrivilegeCount         int64
	GrantOptionCount               int64
	SecurityDefinerExecuteCount    int64
	LargeObjectWriteCount          int64
	OwnedRoutineCount              int64
	BroadSelectPrivilegeCount      int64
	UnexpectedSelectPrivilegeCount int64
	RequiredRelations              []ObservedRelation
	RequiredColumns                []ObservedColumn
}

func ValidateAdmission(expectedRole string, contract Contract, snapshot AdmissionSnapshot) error {
	if strings.TrimSpace(expectedRole) == "" || snapshot.CurrentRole != expectedRole ||
		!snapshot.RoleCanLogin || snapshot.RoleSuper || snapshot.RoleInherit ||
		snapshot.RoleCreateRole || snapshot.RoleCreateDB || snapshot.RoleReplication ||
		snapshot.RoleBypassRLS || !snapshot.DatabaseConnect || !snapshot.PublicSchemaUsage ||
		hasForbiddenAuthority(snapshot) {
		return privilegeDiagnostic()
	}

	if !snapshot.DefaultTransactionReadOnly {
		return readOnlyDiagnostic()
	}

	if !exactRelationMatch(contract, snapshot.RequiredRelations) {
		return schemaDiagnostic()
	}

	selectable, schemaMatches := exactSchemaMatch(contract, snapshot.RequiredColumns)
	if !schemaMatches {
		return schemaDiagnostic()
	}
	if !selectable {
		return privilegeDiagnostic()
	}
	return nil
}

func exactRelationMatch(contract Contract, observed []ObservedRelation) bool {
	if len(contract.Relations) == 0 || len(observed) != len(contract.Relations) {
		return false
	}

	expected := make(map[relationKey]RelationContract, len(contract.Relations))
	for _, relation := range contract.Relations {
		key := makeRelationKey(relation.Schema, relation.Name)
		if strings.TrimSpace(key.schema) == "" || strings.TrimSpace(key.name) == "" || relation.Relkind != "r" {
			return false
		}
		if _, duplicate := expected[key]; duplicate {
			return false
		}
		expected[key] = relation
	}

	seen := make(map[relationKey]bool, len(observed))
	for _, relation := range observed {
		key := makeRelationKey(relation.Schema, relation.Name)
		want, ok := expected[key]
		if !ok || seen[key] || relation.Relkind != want.Relkind {
			return false
		}
		seen[key] = true
	}
	return len(seen) == len(expected)
}

func hasForbiddenAuthority(snapshot AdmissionSnapshot) bool {
	counts := [...]int64{
		snapshot.MembershipCount,
		snapshot.OwnedDatabaseCount,
		snapshot.OwnedSchemaCount,
		snapshot.OwnedRelationCount,
		snapshot.DatabaseCreateOrTempCount,
		snapshot.SchemaCreateCount,
		snapshot.TableWritePrivilegeCount,
		snapshot.SequencePrivilegeCount,
		snapshot.GrantOptionCount,
		snapshot.SecurityDefinerExecuteCount,
		snapshot.LargeObjectWriteCount,
		snapshot.OwnedRoutineCount,
		snapshot.BroadSelectPrivilegeCount,
		snapshot.UnexpectedSelectPrivilegeCount,
	}
	for _, count := range counts {
		if count != 0 {
			return true
		}
	}
	return false
}

func exactSchemaMatch(contract Contract, observed []ObservedColumn) (bool, bool) {
	if len(contract.Columns) == 0 || len(observed) != len(contract.Columns) {
		return false, false
	}

	expected := make(map[columnKey]ColumnContract, len(contract.Columns))
	for _, column := range contract.Columns {
		key := makeColumnKey(column.Schema, column.Table, column.Column)
		if key.schema == "" || key.table == "" || key.column == "" || column.UDTName == "" {
			return false, false
		}
		if _, duplicate := expected[key]; duplicate {
			return false, false
		}
		expected[key] = column
	}

	seen := make(map[columnKey]bool, len(observed))
	allSelectable := true
	for _, column := range observed {
		key := makeColumnKey(column.Schema, column.Table, column.Column)
		want, ok := expected[key]
		if !ok || seen[key] || column.UDTName != want.UDTName || column.Nullable != want.Nullable {
			return false, false
		}
		seen[key] = true
		allSelectable = allSelectable && column.Selectable
	}
	return allSelectable, len(seen) == len(expected)
}

type columnKey struct {
	schema string
	table  string
	column string
}

type relationKey struct {
	schema string
	name   string
}

func makeRelationKey(schema, name string) relationKey {
	return relationKey{schema: schema, name: name}
}

func makeColumnKey(schema, table, column string) columnKey {
	return columnKey{schema: schema, table: table, column: column}
}

func privilegeDiagnostic() error {
	return diagnostics.New(diagnostics.CodeDatabasePrivilege, diagnostics.CategoryDatabasePrivilege, "database role is not admitted")
}

func readOnlyDiagnostic() error {
	return diagnostics.New(diagnostics.CodeDatabaseReadOnly, diagnostics.CategoryDatabaseReadOnly, "database read-only verification failed")
}

func schemaDiagnostic() error {
	return diagnostics.New(diagnostics.CodeSchemaCompatibility, diagnostics.CategorySchemaCompatibility, "database schema is not compatible")
}
