//go:build integration

package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
)

const (
	defaultIntegrationPostgresImage = "postgres:15-alpine"
	integrationDatabaseName         = "usage_viewer_test"
	integrationRoleName             = "viewer_exact_role"
	integrationRolePassword         = "integration-password-secret-sentinel"
)

type postgresIntegrationHarness struct {
	container *tcpostgres.PostgresContainer
	admin     *pgx.Conn
	adminURL  string
}

var (
	integrationHarnessMu sync.Mutex
	sharedHarness        *postgresIntegrationHarness
)

func TestMain(m *testing.M) {
	code := m.Run()
	integrationHarnessMu.Lock()
	harness := sharedHarness
	sharedHarness = nil
	integrationHarnessMu.Unlock()
	if harness != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = harness.admin.Close(ctx)
		_ = harness.container.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

func requirePostgresIntegrationHarness(t *testing.T) *postgresIntegrationHarness {
	t.Helper()
	if !dockerAvailable() {
		if os.Getenv("CI") != "" {
			t.Fatal("Docker is required for integration tests when CI is set")
		}
		t.Skip("Docker is unavailable; skipping disposable PostgreSQL integration test")
	}

	integrationHarnessMu.Lock()
	defer integrationHarnessMu.Unlock()
	if sharedHarness != nil {
		return sharedHarness
	}
	harness, err := startPostgresIntegrationHarness(context.Background())
	require.NoError(t, err, "start isolated PostgreSQL integration harness")
	sharedHarness = harness
	return harness
}

func startPostgresIntegrationHarness(ctx context.Context) (*postgresIntegrationHarness, error) {
	image := strings.TrimSpace(os.Getenv("SUB2API_USAGE_VIEWER_TEST_POSTGRES_IMAGE"))
	if image == "" {
		image = defaultIntegrationPostgresImage
	}
	container, err := tcpostgres.Run(
		ctx,
		image,
		tcpostgres.WithDatabase(integrationDatabaseName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("start PostgreSQL test container: %w", err)
	}

	adminURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("resolve PostgreSQL test connection: %w", err)
	}
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("connect PostgreSQL test administrator: %w", err)
	}
	harness := &postgresIntegrationHarness{container: container, admin: admin, adminURL: adminURL}
	if err := harness.createBaseFixture(ctx); err != nil {
		_ = admin.Close(ctx)
		_ = container.Terminate(ctx)
		return nil, err
	}
	return harness, nil
}

func (h *postgresIntegrationHarness) createBaseFixture(ctx context.Context) error {
	statements := []string{
		`REVOKE CREATE, TEMP ON DATABASE usage_viewer_test FROM PUBLIC`,
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		`CREATE TABLE public.accounts (
            id bigint NOT NULL,
            name varchar NOT NULL,
            platform varchar NOT NULL,
            status varchar NOT NULL,
            deleted_at timestamptz NULL,
            internal_secret text NULL
        )`,
		`CREATE TABLE public.users (
            id bigint NOT NULL,
            email varchar NOT NULL,
            username varchar NOT NULL,
            status varchar NOT NULL,
            deleted_at timestamptz NULL,
            password_hash text NULL
        )`,
		`CREATE TABLE public.usage_logs (
            id bigint NOT NULL,
            user_id bigint NOT NULL,
            account_id bigint NOT NULL,
            request_id varchar NULL,
            model varchar NOT NULL,
            requested_model varchar NULL,
            input_tokens integer NOT NULL,
            output_tokens integer NOT NULL,
            cache_creation_tokens integer NOT NULL,
            cache_read_tokens integer NOT NULL,
            total_cost numeric NOT NULL,
            actual_cost numeric NOT NULL,
            account_stats_cost numeric NULL,
            account_rate_multiplier numeric NULL,
            created_at timestamptz NOT NULL,
            internal_secret text NULL
        )`,
		`CREATE TABLE public.other_records (id bigint NOT NULL, internal_secret text NULL)`,
		`CREATE SEQUENCE public.other_sequence`,
		`CREATE ROLE viewer_exact_role LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD 'integration-password-secret-sentinel'`,
		`ALTER ROLE viewer_exact_role SET default_transaction_read_only = on`,
		`GRANT CONNECT ON DATABASE usage_viewer_test TO viewer_exact_role`,
		`GRANT USAGE ON SCHEMA public TO viewer_exact_role`,
	}
	for _, statement := range statements {
		if _, err := h.admin.Exec(ctx, statement); err != nil {
			return fmt.Errorf("create PostgreSQL test fixture: %w", err)
		}
	}
	return h.grantContractColumns(ctx, integrationRoleName)
}

func (h *postgresIntegrationHarness) grantContractColumns(ctx context.Context, role string) error {
	columnsByTable := make(map[string][]string)
	for _, column := range CurrentContract().Columns {
		columnsByTable[column.Table] = append(columnsByTable[column.Table], column.Column)
	}
	tables := make([]string, 0, len(columnsByTable))
	for table := range columnsByTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		if err := h.grantContractRelationColumns(ctx, role, table); err != nil {
			return fmt.Errorf("grant PostgreSQL test contract: %w", err)
		}
	}
	return nil
}

func (h *postgresIntegrationHarness) grantContractRelationColumns(ctx context.Context, role, relation string) error {
	statement, ok := contractColumnPrivilegeStatement("GRANT", role, relation)
	if !ok {
		return fmt.Errorf("relation %q has no contract columns", relation)
	}
	_, err := h.admin.Exec(ctx, statement)
	return err
}

func (h *postgresIntegrationHarness) replaceRequiredRelation(
	t *testing.T,
	relation string,
	install []string,
	remove []string,
	grantContract bool,
) {
	t.Helper()
	revoke, ok := contractColumnPrivilegeStatement("REVOKE", integrationRoleName, relation)
	require.True(t, ok, "required relation has contract columns")
	original := relation + "_plan05_original"
	h.execAdmin(t,
		revoke,
		fmt.Sprintf(
			"ALTER TABLE %s RENAME TO %s",
			pgx.Identifier{"public", relation}.Sanitize(),
			pgx.Identifier{original}.Sanitize(),
		),
	)
	t.Cleanup(func() {
		for _, statement := range remove {
			if _, err := h.admin.Exec(context.Background(), statement); err != nil {
				t.Errorf("remove isolated relation substitute: %v", err)
			}
		}
		if _, err := h.admin.Exec(
			context.Background(),
			fmt.Sprintf(
				"ALTER TABLE %s RENAME TO %s",
				pgx.Identifier{"public", original}.Sanitize(),
				pgx.Identifier{relation}.Sanitize(),
			),
		); err != nil {
			t.Errorf("restore isolated ordinary table: %v", err)
			return
		}
		if err := h.grantContractRelationColumns(context.Background(), integrationRoleName, relation); err != nil {
			t.Errorf("restore isolated relation grants: %v", err)
		}
	})

	h.execAdmin(t, install...)
	if grantContract {
		require.NoError(t, h.grantContractRelationColumns(context.Background(), integrationRoleName, relation))
	}
}

func (h *postgresIntegrationHarness) requiredRelationKind(t *testing.T, relation string) string {
	t.Helper()
	var relkind string
	err := h.admin.QueryRow(
		context.Background(),
		`SELECT relation.relkind::text
         FROM pg_catalog.pg_class AS relation
         JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
         WHERE namespace.nspname = 'public' AND relation.relname = $1`,
		relation,
	).Scan(&relkind)
	require.NoError(t, err)
	return relkind
}

func contractColumnPrivilegeStatement(action, role, relation string) (string, bool) {
	columns := make([]string, 0)
	for _, column := range CurrentContract().Columns {
		if column.Table == relation {
			columns = append(columns, pgx.Identifier{column.Column}.Sanitize())
		}
	}
	if len(columns) == 0 {
		return "", false
	}
	return fmt.Sprintf(
		"%s SELECT (%s) ON TABLE %s %s %s",
		action,
		strings.Join(columns, ", "),
		pgx.Identifier{"public", relation}.Sanitize(),
		map[string]string{"GRANT": "TO", "REVOKE": "FROM"}[action],
		pgx.Identifier{role}.Sanitize(),
	), true
}

func (h *postgresIntegrationHarness) candidateConfig(role, password string) config.Config {
	parsed, err := url.Parse(h.adminURL)
	if err != nil {
		panic("Testcontainers returned an invalid PostgreSQL URL")
	}
	parsed.User = url.UserPassword(role, password)
	return config.Config{
		DatabaseURL:               parsed.String(),
		ExpectedDatabaseRole:      role,
		DatabaseConnectTimeout:    5 * time.Second,
		DatabaseAcquireTimeout:    3 * time.Second,
		DatabaseQueryTimeout:      5 * time.Second,
		DatabasePoolMaxConns:      4,
		DatabasePoolMinConns:      0,
		DatabaseMaxConnLifetime:   30 * time.Minute,
		DatabaseMaxConnIdleTime:   5 * time.Minute,
		DatabaseHealthCheckPeriod: time.Minute,
	}
}

func (h *postgresIntegrationHarness) execAdmin(t *testing.T, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		_, err := h.admin.Exec(context.Background(), statement)
		require.NoError(t, err, "execute isolated PostgreSQL fixture statement")
	}
}

func dockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "info")
	command.Env = os.Environ()
	return command.Run() == nil
}
