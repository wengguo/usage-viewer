//go:build integration

package postgres

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestPreflightAcceptsExactLeastPrivilegeRole(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	if err := runCandidatePreflight(t, harness); err != nil {
		t.Fatalf("exact role preflight failed: %v", err)
	}
}

func TestPreflightRejectsUnexpectedRequiredRelationKinds(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	require.NoError(t, runCandidatePreflight(t, harness), "accepted fixture before relation substitutions")

	tests := []struct {
		name          string
		relkind       string
		install       []string
		remove        []string
		grantContract bool
	}{
		{
			name:    "partitioned table",
			relkind: "p",
			install: []string{`CREATE TABLE public.api_keys (
                id bigint NOT NULL,
                key varchar NOT NULL,
                name varchar NOT NULL,
                group_id bigint NULL,
                quota numeric NOT NULL,
                quota_used numeric NOT NULL,
                last_used_at timestamptz NULL,
                expires_at timestamptz NULL,
                status varchar NOT NULL,
                created_at timestamptz NOT NULL,
                deleted_at timestamptz NULL
            ) PARTITION BY RANGE (id)`},
			remove:        []string{`DROP TABLE public.api_keys`},
			grantContract: true,
		},
		{
			name:    "owner privileged view",
			relkind: "v",
			install: []string{
				`CREATE TABLE public.api_keys_relation_source_plan05 (
                    id bigint NOT NULL,
                    key varchar NOT NULL,
                    name varchar NOT NULL,
                    group_id bigint NULL,
                    quota numeric NOT NULL,
                    quota_used numeric NOT NULL,
                    last_used_at timestamptz NULL,
                    expires_at timestamptz NULL,
                    status varchar NOT NULL,
                    created_at timestamptz NOT NULL,
                    deleted_at timestamptz NULL,
                    unrelated_value text NOT NULL
                )`,
				`INSERT INTO public.api_keys_relation_source_plan05
                    (id, key, name, group_id, quota, quota_used, last_used_at, expires_at, status, created_at, deleted_at, unrelated_value)
                 VALUES (1, 'sk-relation-source', 'key-name', NULL, 0, 0, NULL, NULL, 'active', pg_catalog.now(), NULL, 'unrelated-data-secret-sentinel')`,
				`CREATE VIEW public.api_keys AS
                 SELECT id, key, name, group_id, quota, quota_used, last_used_at, expires_at, status, created_at, deleted_at
                 FROM public.api_keys_relation_source_plan05`,
			},
			remove: []string{
				`DROP VIEW public.api_keys`,
				`DROP TABLE public.api_keys_relation_source_plan05`,
			},
			grantContract: true,
		},
		{
			name:    "materialized view",
			relkind: "m",
			install: []string{`CREATE MATERIALIZED VIEW public.api_keys AS
                SELECT id, key, name, group_id, quota, quota_used, last_used_at, expires_at, status, created_at, deleted_at
                FROM public.api_keys_plan05_original
                WITH NO DATA`},
			remove:        []string{`DROP MATERIALIZED VIEW public.api_keys`},
			grantContract: true,
		},
		{
			name:    "foreign table",
			relkind: "f",
			install: []string{
				`CREATE EXTENSION postgres_fdw`,
				`CREATE SERVER api_keys_plan05_server FOREIGN DATA WRAPPER postgres_fdw`,
				`CREATE FOREIGN TABLE public.api_keys (
                    id bigint,
                    key varchar,
                    name varchar,
                    group_id bigint,
                    quota numeric,
                    quota_used numeric,
                    last_used_at timestamptz,
                    expires_at timestamptz,
                    status varchar,
                    created_at timestamptz,
                    deleted_at timestamptz
                ) SERVER api_keys_plan05_server
                OPTIONS (schema_name 'public', table_name 'api_keys_plan05_never_queried')`,
			},
			remove: []string{
				`DROP FOREIGN TABLE public.api_keys`,
				`DROP SERVER api_keys_plan05_server`,
				`DROP EXTENSION postgres_fdw`,
			},
			grantContract: true,
		},
		{
			name:    "composite relation",
			relkind: "c",
			install: []string{`CREATE TYPE public.api_keys AS (
                id bigint,
                key varchar,
                name varchar,
                group_id bigint,
                quota numeric,
                quota_used numeric,
                last_used_at timestamptz,
                expires_at timestamptz,
                status varchar,
                created_at timestamptz,
                deleted_at timestamptz
            )`},
			remove: []string{`DROP TYPE public.api_keys`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness.replaceRequiredRelation(t, "api_keys", tt.install, tt.remove, tt.grantContract)
			if got := harness.requiredRelationKind(t, "api_keys"); got != tt.relkind {
				t.Fatalf("observed relkind = %q, want %q", got, tt.relkind)
			}
			if tt.relkind == "v" {
				var sourceSelectable bool
				err := harness.admin.QueryRow(
					context.Background(),
					`SELECT pg_catalog.has_table_privilege($1, $2, 'SELECT')`,
					integrationRoleName,
					"public.api_keys_relation_source_plan05",
				).Scan(&sourceSelectable)
				require.NoError(t, err)
				if sourceSelectable {
					t.Fatal("viewer role unexpectedly has source-table SELECT")
				}
			}

			err := runCandidatePreflight(t, harness)
			if diagnostics.CodeOf(err) != diagnostics.CodeSchemaCompatibility {
				t.Fatalf("diagnostic code = %q, want %q; error = %v", diagnostics.CodeOf(err), diagnostics.CodeSchemaCompatibility, err)
			}
			for _, sentinel := range []string{
				integrationRoleName,
				integrationRolePassword,
				"accounts_relation_source_plan05",
				"unrelated-data-secret-sentinel",
				"raw-pgx-secret-sentinel",
			} {
				if strings.Contains(err.Error(), sentinel) {
					t.Errorf("diagnostic disclosed sentinel %q", sentinel)
				}
			}
		})
	}

	require.NoError(t, runCandidatePreflight(t, harness), "accepted fixture after relation substitutions")
}

func TestPreflightRejectsUnsafeRealPostgreSQLVariants(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	role := pgx.Identifier{integrationRoleName}.Sanitize()

	tests := []struct {
		name     string
		wantCode diagnostics.Code
		setup    []string
		cleanup  []string
	}{
		{name: "superuser", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER ROLE " + role + " SUPERUSER"}, cleanup: []string{"ALTER ROLE " + role + " NOSUPERUSER"}},
		{name: "inheritance", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER ROLE " + role + " INHERIT"}, cleanup: []string{"ALTER ROLE " + role + " NOINHERIT"}},
		{name: "create role", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER ROLE " + role + " CREATEROLE"}, cleanup: []string{"ALTER ROLE " + role + " NOCREATEROLE"}},
		{name: "create database", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER ROLE " + role + " CREATEDB"}, cleanup: []string{"ALTER ROLE " + role + " NOCREATEDB"}},
		{name: "replication", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER ROLE " + role + " REPLICATION"}, cleanup: []string{"ALTER ROLE " + role + " NOREPLICATION"}},
		{name: "bypass RLS", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER ROLE " + role + " BYPASSRLS"}, cleanup: []string{"ALTER ROLE " + role + " NOBYPASSRLS"}},
		{name: "direct membership", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"CREATE ROLE writer_direct", "GRANT writer_direct TO " + role}, cleanup: []string{"REVOKE writer_direct FROM " + role, "DROP ROLE writer_direct"}},
		{name: "nested membership", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"CREATE ROLE writer_parent", "CREATE ROLE writer_nested", "GRANT writer_parent TO writer_nested", "GRANT writer_nested TO " + role}, cleanup: []string{"REVOKE writer_nested FROM " + role, "REVOKE writer_parent FROM writer_nested", "DROP ROLE writer_nested", "DROP ROLE writer_parent"}},
		{name: "database ownership", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER DATABASE usage_viewer_test OWNER TO " + role}, cleanup: []string{"ALTER DATABASE usage_viewer_test OWNER TO postgres"}},
		{name: "schema ownership", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"CREATE SCHEMA owned_fixture AUTHORIZATION " + role}, cleanup: []string{"DROP SCHEMA owned_fixture"}},
		{name: "relation ownership", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"ALTER TABLE public.other_records OWNER TO " + role}, cleanup: []string{"ALTER TABLE public.other_records OWNER TO postgres"}},
		{name: "routine ownership", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"CREATE FUNCTION public.owned_fixture_function() RETURNS integer LANGUAGE sql AS 'SELECT 1'", "ALTER FUNCTION public.owned_fixture_function() OWNER TO " + role}, cleanup: []string{"DROP FUNCTION public.owned_fixture_function()"}},
		{name: "database create", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT CREATE ON DATABASE usage_viewer_test TO " + role}, cleanup: []string{"REVOKE CREATE ON DATABASE usage_viewer_test FROM " + role}},
		{name: "database temporary", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT TEMP ON DATABASE usage_viewer_test TO " + role}, cleanup: []string{"REVOKE TEMP ON DATABASE usage_viewer_test FROM " + role}},
		{name: "public temporary", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT TEMP ON DATABASE usage_viewer_test TO PUBLIC"}, cleanup: []string{"REVOKE TEMP ON DATABASE usage_viewer_test FROM PUBLIC"}},
		{name: "schema create", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT CREATE ON SCHEMA public TO " + role}, cleanup: []string{"REVOKE CREATE ON SCHEMA public FROM " + role}},
		{name: "public schema create", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT CREATE ON SCHEMA public TO PUBLIC"}, cleanup: []string{"REVOKE CREATE ON SCHEMA public FROM PUBLIC"}},
		{name: "table write", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT INSERT ON public.other_records TO " + role}, cleanup: []string{"REVOKE INSERT ON public.other_records FROM " + role}},
		{name: "public table write", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT UPDATE ON public.other_records TO PUBLIC"}, cleanup: []string{"REVOKE UPDATE ON public.other_records FROM PUBLIC"}},
		{name: "column write", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT UPDATE (internal_secret) ON public.other_records TO " + role}, cleanup: []string{"REVOKE UPDATE (internal_secret) ON public.other_records FROM " + role}},
		{name: "sequence select", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT SELECT ON SEQUENCE public.other_sequence TO " + role}, cleanup: []string{"REVOKE SELECT ON SEQUENCE public.other_sequence FROM " + role}},
		{name: "sequence usage", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT USAGE ON SEQUENCE public.other_sequence TO " + role}, cleanup: []string{"REVOKE USAGE ON SEQUENCE public.other_sequence FROM " + role}},
		{name: "sequence update", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT UPDATE ON SEQUENCE public.other_sequence TO " + role}, cleanup: []string{"REVOKE UPDATE ON SEQUENCE public.other_sequence FROM " + role}},
		{name: "grant option", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT CONNECT ON DATABASE usage_viewer_test TO " + role + " WITH GRANT OPTION"}, cleanup: []string{"REVOKE GRANT OPTION FOR CONNECT ON DATABASE usage_viewer_test FROM " + role}},
		{name: "security definer execution", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"CREATE FUNCTION public.security_definer_fixture() RETURNS integer LANGUAGE sql SECURITY DEFINER AS 'SELECT 1'", "GRANT EXECUTE ON FUNCTION public.security_definer_fixture() TO " + role}, cleanup: []string{"DROP FUNCTION public.security_definer_fixture()"}},
		{name: "large object write", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"SELECT pg_catalog.lo_create(987654)", "GRANT UPDATE ON LARGE OBJECT 987654 TO " + role}, cleanup: []string{"SELECT pg_catalog.lo_unlink(987654)"}},
		{name: "broad select", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT SELECT ON public.api_keys TO " + role}, cleanup: []string{"REVOKE SELECT ON public.api_keys FROM " + role}},
		{name: "public broad select", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT SELECT ON public.other_records TO PUBLIC"}, cleanup: []string{"REVOKE SELECT ON public.other_records FROM PUBLIC"}},
		{name: "unexpected column select", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"GRANT SELECT (internal_secret) ON public.api_keys TO " + role}, cleanup: []string{"REVOKE SELECT (internal_secret) ON public.api_keys FROM " + role}},
		{name: "missing required grant", wantCode: diagnostics.CodeDatabasePrivilege, setup: []string{"REVOKE SELECT (name) ON public.api_keys FROM " + role}, cleanup: []string{"GRANT SELECT (name) ON public.api_keys TO " + role}},
		{name: "read write default", wantCode: diagnostics.CodeDatabaseReadOnly, setup: []string{"ALTER ROLE " + role + " SET default_transaction_read_only = off"}, cleanup: []string{"ALTER ROLE " + role + " SET default_transaction_read_only = on"}},
		{name: "mistyped required column", wantCode: diagnostics.CodeSchemaCompatibility, setup: []string{"ALTER TABLE public.api_keys ALTER COLUMN name TYPE text"}, cleanup: []string{"ALTER TABLE public.api_keys ALTER COLUMN name TYPE varchar"}},
		{name: "wrong nullability", wantCode: diagnostics.CodeSchemaCompatibility, setup: []string{"ALTER TABLE public.api_keys ALTER COLUMN name DROP NOT NULL"}, cleanup: []string{"ALTER TABLE public.api_keys ALTER COLUMN name SET NOT NULL"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness.execAdmin(t, tt.setup...)
			defer harness.execAdmin(t, tt.cleanup...)
			err := runCandidatePreflight(t, harness)
			if diagnostics.CodeOf(err) != tt.wantCode {
				t.Fatalf("diagnostic code = %q, want %q; error = %v", diagnostics.CodeOf(err), tt.wantCode, err)
			}
			for _, sentinel := range []string{integrationRoleName, integrationRolePassword, "internal_secret", "raw-pgx-secret-sentinel"} {
				if strings.Contains(err.Error(), sentinel) {
					t.Errorf("diagnostic disclosed sentinel %q", sentinel)
				}
			}
		})
	}
}

func TestPreflightRejectsMissingRequiredColumn(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	harness.execAdmin(t, `ALTER TABLE public.api_keys DROP COLUMN name`)
	defer func() {
		harness.execAdmin(t, `ALTER TABLE public.api_keys ADD COLUMN name varchar NOT NULL DEFAULT ''`)
		harness.execAdmin(t, `ALTER TABLE public.api_keys ALTER COLUMN name DROP DEFAULT`)
		require.NoError(t, harness.grantContractColumns(context.Background(), integrationRoleName))
	}()
	err := runCandidatePreflight(t, harness)
	if diagnostics.CodeOf(err) != diagnostics.CodeSchemaCompatibility {
		t.Fatalf("diagnostic code = %q; error = %v", diagnostics.CodeOf(err), err)
	}
}

func TestEveryConnectionRunsAdmissionBeforeAcquireReturns(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	var admissions atomic.Int32
	poolConfig, err := buildPoolConfig(cfg, func(ctx context.Context, conn *pgx.Conn, role string) error {
		if err := CheckConnectionAdmission(ctx, conn, role); err != nil {
			return err
		}
		admissions.Add(1)
		return nil
	})
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	require.NoError(t, err)
	defer pool.Close()

	type acquireResult struct {
		err error
	}
	results := make(chan acquireResult, 4)
	release := make(chan struct{})
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			connection, acquireErr := pool.Acquire(context.Background())
			if acquireErr != nil {
				results <- acquireResult{err: acquireErr}
				return
			}
			results <- acquireResult{}
			<-release
			connection.Release()
		}()
	}
	acquireErrors := make([]error, 0, 4)
	for range 4 {
		acquireErrors = append(acquireErrors, (<-results).err)
	}
	got := admissions.Load()
	close(release)
	group.Wait()
	for _, acquireErr := range acquireErrors {
		require.NoError(t, acquireErr)
	}
	if got != 4 {
		t.Fatalf("admission completions = %d, want 4", got)
	}
}

func runCandidatePreflight(t *testing.T, harness *postgresIntegrationHarness) error {
	t.Helper()
	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	return RunPreflight(context.Background(), pool, cfg)
}
