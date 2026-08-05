package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

type diagnosticStage uint8

const (
	stageConnectivity diagnosticStage = iota
	stageRole
	stageReadOnly
	stageAuthority
	stageSchema
)

// CheckLightAdmission verifies connectivity only. It is used when the viewer
// shares database credentials with the main application, so role, read-only,
// authority, and exact-schema checks do not apply — the application schema is
// authoritative by definition.
func CheckLightAdmission(ctx context.Context, conn *pgx.Conn) error {
	if ctx == nil || conn == nil {
		return diagnosticForStage(stageConnectivity, errors.New("preflight dependency unavailable"))
	}

	var probe int
	if err := conn.QueryRow(ctx, connectivityProbeSQL).Scan(&probe); err != nil || probe != 1 {
		if err == nil {
			err = errors.New("connectivity probe returned unexpected evidence")
		}
		return diagnosticForStage(stageConnectivity, err)
	}
	return nil
}

func CheckConnectionAdmission(ctx context.Context, conn *pgx.Conn, expectedRole string) error {
	if ctx == nil || conn == nil {
		return diagnosticForStage(stageConnectivity, errors.New("preflight dependency unavailable"))
	}

	var probe int
	if err := conn.QueryRow(ctx, connectivityProbeSQL).Scan(&probe); err != nil || probe != 1 {
		if err == nil {
			err = errors.New("connectivity probe returned unexpected evidence")
		}
		return diagnosticForStage(stageConnectivity, err)
	}

	contract := CurrentContract()
	snapshot, err := collectAdmissionSnapshot(ctx, conn, contract)
	if err != nil {
		return err
	}
	return ValidateAdmission(expectedRole, contract, snapshot)
}

func collectAdmissionSnapshot(ctx context.Context, conn *pgx.Conn, contract Contract) (AdmissionSnapshot, error) {
	var snapshot AdmissionSnapshot
	if err := conn.QueryRow(ctx, roleEvidenceSQL).Scan(
		&snapshot.CurrentRole,
		&snapshot.RoleCanLogin,
		&snapshot.RoleSuper,
		&snapshot.RoleInherit,
		&snapshot.RoleCreateRole,
		&snapshot.RoleCreateDB,
		&snapshot.RoleReplication,
		&snapshot.RoleBypassRLS,
		&snapshot.DatabaseConnect,
		&snapshot.PublicSchemaUsage,
	); err != nil {
		return AdmissionSnapshot{}, diagnosticForStage(stageRole, err)
	}

	if err := conn.QueryRow(ctx, readOnlyEvidenceSQL).Scan(&snapshot.DefaultTransactionReadOnly); err != nil {
		return AdmissionSnapshot{}, diagnosticForStage(stageReadOnly, err)
	}

	columnSchemas, tables, columns := contractArguments(contract)
	if err := conn.QueryRow(ctx, authorityEvidenceSQL, columnSchemas, tables, columns).Scan(
		&snapshot.MembershipCount,
		&snapshot.OwnedDatabaseCount,
		&snapshot.OwnedSchemaCount,
		&snapshot.OwnedRelationCount,
		&snapshot.DatabaseCreateOrTempCount,
		&snapshot.SchemaCreateCount,
		&snapshot.TableWritePrivilegeCount,
		&snapshot.SequencePrivilegeCount,
		&snapshot.GrantOptionCount,
		&snapshot.SecurityDefinerExecuteCount,
		&snapshot.LargeObjectWriteCount,
		&snapshot.OwnedRoutineCount,
		&snapshot.BroadSelectPrivilegeCount,
		&snapshot.UnexpectedSelectPrivilegeCount,
	); err != nil {
		return AdmissionSnapshot{}, diagnosticForStage(stageAuthority, err)
	}

	relationSchemas, relations := relationContractArguments(contract)
	observedRelations, err := collectRelationEvidence(ctx, conn, relationSchemas, relations)
	if err != nil {
		return AdmissionSnapshot{}, err
	}
	if !exactRelationMatch(contract, observedRelations) {
		return AdmissionSnapshot{}, schemaDiagnostic()
	}
	snapshot.RequiredRelations = observedRelations

	observed, err := collectColumnEvidence(ctx, conn, columnSchemas, tables, columns)
	if err != nil {
		return AdmissionSnapshot{}, err
	}
	snapshot.RequiredColumns = observed
	return snapshot, nil
}

func collectRelationEvidence(ctx context.Context, conn *pgx.Conn, schemas, relations []string) ([]ObservedRelation, error) {
	rows, err := conn.Query(ctx, relationEvidenceSQL, schemas, relations)
	if err != nil {
		return nil, diagnosticForStage(stageSchema, err)
	}
	defer rows.Close()

	observed := make([]ObservedRelation, 0, len(relations))
	for rows.Next() {
		var relation ObservedRelation
		if err := rows.Scan(&relation.Schema, &relation.Name, &relation.Relkind); err != nil {
			return nil, diagnosticForStage(stageSchema, err)
		}
		observed = append(observed, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, diagnosticForStage(stageSchema, err)
	}
	return observed, nil
}

func collectColumnEvidence(ctx context.Context, conn *pgx.Conn, schemas, tables, columns []string) ([]ObservedColumn, error) {
	rows, err := conn.Query(ctx, columnEvidenceSQL, schemas, tables, columns)
	if err != nil {
		return nil, diagnosticForStage(stageSchema, err)
	}
	defer rows.Close()

	observed := make([]ObservedColumn, 0, len(columns))
	for rows.Next() {
		var column ObservedColumn
		if err := rows.Scan(
			&column.Schema,
			&column.Table,
			&column.Column,
			&column.UDTName,
			&column.Nullable,
			&column.Selectable,
		); err != nil {
			return nil, diagnosticForStage(stageSchema, err)
		}
		observed = append(observed, column)
	}
	if err := rows.Err(); err != nil {
		return nil, diagnosticForStage(stageSchema, err)
	}
	return observed, nil
}

func contractArguments(contract Contract) ([]string, []string, []string) {
	schemas := make([]string, len(contract.Columns))
	tables := make([]string, len(contract.Columns))
	columns := make([]string, len(contract.Columns))
	for i, column := range contract.Columns {
		schemas[i] = column.Schema
		tables[i] = column.Table
		columns[i] = column.Column
	}
	return schemas, tables, columns
}

func relationContractArguments(contract Contract) ([]string, []string) {
	schemas := make([]string, len(contract.Relations))
	relations := make([]string, len(contract.Relations))
	for i, relation := range contract.Relations {
		schemas[i] = relation.Schema
		relations[i] = relation.Name
	}
	return schemas, relations
}

func diagnosticForStage(stage diagnosticStage, cause error) *diagnostics.Diagnostic {
	switch stage {
	case stageConnectivity:
		return diagnostics.Wrap(diagnostics.CodeDatabaseConnectivity, diagnostics.CategoryDatabaseConnectivity, "database connection could not be established", cause)
	case stageRole, stageAuthority:
		return diagnostics.Wrap(diagnostics.CodeDatabasePrivilege, diagnostics.CategoryDatabasePrivilege, "database role is not admitted", cause)
	case stageReadOnly:
		return diagnostics.Wrap(diagnostics.CodeDatabaseReadOnly, diagnostics.CategoryDatabaseReadOnly, "database read-only verification failed", cause)
	case stageSchema:
		return diagnostics.Wrap(diagnostics.CodeSchemaCompatibility, diagnostics.CategorySchemaCompatibility, "database schema is not compatible", cause)
	default:
		return diagnostics.Wrap(diagnostics.CodeServer, diagnostics.CategoryServer, "server could not start safely", cause)
	}
}
