//go:build integration

package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	viewerpostgres "github.com/Wei-Shaw/sub2api/usage-viewer/internal/postgres"
)

const (
	runtimeDatabaseName = "usage_viewer_runtime_test"
	runtimeRoleName     = "viewer_runtime_role_sentinel"
	runtimeRolePassword = "runtime-password-secret-sentinel"
	postgresImageEnv    = "SUB2API_USAGE_VIEWER_TEST_POSTGRES_IMAGE"
)

type runtimePostgresHarness struct {
	container *tcpostgres.PostgresContainer
	admin     *pgx.Conn
	adminURL  string
}

var (
	runtimeHarnessMu sync.Mutex
	runtimeHarness   *runtimePostgresHarness
)

func TestMain(m *testing.M) {
	code := m.Run()
	runtimeHarnessMu.Lock()
	harness := runtimeHarness
	runtimeHarness = nil
	runtimeHarnessMu.Unlock()
	if harness != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = harness.admin.Close(ctx)
		_ = harness.container.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

func TestRuntimeConfigurationBindAndConnectivityFailuresNeverOpenSocket(t *testing.T) {
	binary := buildViewerBinary(t)

	t.Run("invalid configuration", func(t *testing.T) {
		listenAddress := reserveLoopbackAddress(t)
		environment := runtimeEnvironment("", "", listenAddress)
		delete(environment, "SUB2API_USAGE_VIEWER_DATABASE_URL")
		delete(environment, "SUB2API_USAGE_VIEWER_DATABASE_ROLE")
		assertFailedProcess(t, binary, environment, listenAddress, "UV-CFG-001", []string{listenAddress})
	})

	t.Run("one-sided non-loopback unlock", func(t *testing.T) {
		listenAddress := reserveLoopbackAddress(t)
		_, port, err := net.SplitHostPort(listenAddress)
		require.NoError(t, err)
		configuredAddress := net.JoinHostPort("0.0.0.0", port)
		environment := runtimeEnvironment(
			"postgres://runtime-role:runtime-password-secret-sentinel@127.0.0.1/unreached?sslmode=disable",
			"runtime-role-sentinel",
			configuredAddress,
		)
		assertFailedProcess(t, binary, environment, listenAddress, "UV-BIND-001", []string{configuredAddress, "runtime-role-sentinel", "runtime-password-secret-sentinel"})
	})

	t.Run("unreachable database", func(t *testing.T) {
		listenAddress := reserveLoopbackAddress(t)
		databaseAddress := reserveLoopbackAddress(t)
		databaseURL := "postgres://runtime-role:runtime-password-secret-sentinel@" + databaseAddress + "/unreachable?sslmode=disable"
		environment := runtimeEnvironment(databaseURL, "runtime-role-sentinel", listenAddress)
		assertFailedProcess(t, binary, environment, listenAddress, "UV-DB-001", []string{databaseURL, databaseAddress, listenAddress, "runtime-role-sentinel", "runtime-password-secret-sentinel"})
	})
}

func TestRuntimeDatabaseAdmissionFailuresNeverOpenSocket(t *testing.T) {
	harness := requireRuntimePostgres(t)
	binary := buildViewerBinary(t)
	role := pgx.Identifier{runtimeRoleName}.Sanitize()

	tests := []struct {
		name    string
		code    string
		setup   string
		cleanup string
	}{
		{name: "unsafe role", code: "UV-ROLE-001", setup: "ALTER ROLE " + role + " CREATEROLE", cleanup: "ALTER ROLE " + role + " NOCREATEROLE"},
		{name: "read-write default", code: "UV-RO-001", setup: "ALTER ROLE " + role + " SET default_transaction_read_only = off", cleanup: "ALTER ROLE " + role + " SET default_transaction_read_only = on"},
		{name: "incompatible schema", code: "UV-SCHEMA-001", setup: "ALTER TABLE public.accounts ALTER COLUMN name TYPE text", cleanup: "ALTER TABLE public.accounts ALTER COLUMN name TYPE varchar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness.execAdmin(t, tt.setup)
			defer harness.execAdmin(t, tt.cleanup)
			listenAddress := reserveLoopbackAddress(t)
			databaseURL := harness.candidateURL(t)
			environment := runtimeEnvironment(databaseURL, runtimeRoleName, listenAddress)
			assertFailedProcess(t, binary, environment, listenAddress, tt.code, []string{
				databaseURL,
				listenAddress,
				runtimeRoleName,
				runtimeRolePassword,
				"runtime-object-secret-sentinel",
				"raw-postgres-secret-sentinel",
			})
		})
	}
}

func TestRuntimeRelationKindFailuresNeverOpenSocket(t *testing.T) {
	harness := requireRuntimePostgres(t)
	binary := buildViewerBinary(t)

	tests := []struct {
		name    string
		relkind string
		install []string
		remove  []string
	}{
		{
			name:    "owner privileged view",
			relkind: "v",
			install: []string{
				`CREATE TABLE public.accounts_relation_source_plan05 (
                    id bigint NOT NULL,
                    name varchar NOT NULL,
                    platform varchar NOT NULL,
                    status varchar NOT NULL,
                    deleted_at timestamptz NULL,
                    unrelated_value text NOT NULL
                )`,
				`INSERT INTO public.accounts_relation_source_plan05
                    (id, name, platform, status, deleted_at, unrelated_value)
                 VALUES (1, 'account-name', 'platform-name', 'active', NULL, 'runtime-unrelated-data-secret-sentinel')`,
				`CREATE VIEW public.accounts AS
                 SELECT id, name, platform, status, deleted_at
                 FROM public.accounts_relation_source_plan05`,
			},
			remove: []string{
				`DROP VIEW public.accounts`,
				`DROP TABLE public.accounts_relation_source_plan05`,
			},
		},
		{
			name:    "foreign table",
			relkind: "f",
			install: []string{
				`CREATE EXTENSION postgres_fdw`,
				`CREATE SERVER accounts_plan05_server FOREIGN DATA WRAPPER postgres_fdw`,
				`CREATE FOREIGN TABLE public.accounts (
                    id bigint,
                    name varchar,
                    platform varchar,
                    status varchar,
                    deleted_at timestamptz
                ) SERVER accounts_plan05_server
                OPTIONS (schema_name 'public', table_name 'accounts_plan05_never_queried')`,
			},
			remove: []string{
				`DROP FOREIGN TABLE public.accounts`,
				`DROP SERVER accounts_plan05_server`,
				`DROP EXTENSION postgres_fdw`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness.replaceRequiredRelation(t, "accounts", tt.install, tt.remove)
			if got := harness.requiredRelationKind(t, "accounts"); got != tt.relkind {
				t.Fatalf("observed relkind = %q, want %q", got, tt.relkind)
			}
			listenAddress := reserveLoopbackAddress(t)
			databaseURL := harness.candidateURL(t)
			environment := runtimeEnvironment(databaseURL, runtimeRoleName, listenAddress)
			assertFailedProcess(t, binary, environment, listenAddress, "UV-SCHEMA-001", []string{
				databaseURL,
				listenAddress,
				runtimeRoleName,
				runtimeRolePassword,
				"accounts_relation_source_plan05",
				"runtime-unrelated-data-secret-sentinel",
				"raw-postgres-secret-sentinel",
			})
		})
	}
}

func TestRuntimeAdmittedProcessServesHealthAndShutsDown(t *testing.T) {
	harness := requireRuntimePostgres(t)
	binary := buildViewerBinary(t)
	listenAddress := reserveLoopbackAddress(t)
	databaseURL := harness.candidateURL(t)
	environment := runtimeEnvironment(databaseURL, runtimeRoleName, listenAddress)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = environmentList(environment)
	output := &lockedBuffer{}
	command.Stdout = output
	command.Stderr = output
	require.NoError(t, command.Start())
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()

	waitForReadyEvent(t, output, waitResult)
	for _, path := range []string{"/livez", "/readyz"} {
		response, err := http.Get("http://" + listenAddress + path)
		require.NoError(t, err)
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		require.NoError(t, readErr)
		if response.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
			t.Fatalf("%s status=%d body=%q", path, response.StatusCode, body)
		}
	}

	require.NoError(t, command.Process.Signal(syscall.SIGTERM))
	select {
	case err := <-waitResult:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("viewer did not terminate within shutdown timeout")
	}
	waitForSocketClosed(t, listenAddress)
	harness.waitForCandidateSessions(t, 0)

	text := output.String()
	if strings.Count(text, `"event":"ready"`) != 1 || strings.Count(text, `"event":"stopping"`) != 1 || !strings.Contains(text, `"address_class":"loopback"`) {
		t.Fatalf("lifecycle output = %q", text)
	}
	assertSentinelsAbsent(t, text, databaseURL, listenAddress, runtimeRoleName, runtimeRolePassword, "runtime-object-secret-sentinel")
}

func assertFailedProcess(t *testing.T, binary string, environment map[string]string, dialAddress, code string, sentinels []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = environmentList(environment)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	require.NoError(t, command.Start())

	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	accepted := false
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var processErr error
	for processErr == nil {
		if socketAccepts(dialAddress) {
			accepted = true
		}
		select {
		case processErr = <-waitResult:
			if processErr == nil {
				processErr = errors.New("process exited successfully")
			}
		case <-ticker.C:
		case <-ctx.Done():
			_ = command.Process.Kill()
			processErr = <-waitResult
			if processErr == nil {
				processErr = ctx.Err()
			}
		}
	}
	if socketAccepts(dialAddress) {
		accepted = true
	}
	if accepted {
		t.Fatalf("process accepted a TCP connection on %s before failing", dialAddress)
	}
	exitError, ok := processErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("process error = %T %v", processErr, processErr)
	}
	wantExit := map[string]int{
		"UV-CFG-001":    2,
		"UV-BIND-001":   2,
		"UV-DB-001":     3,
		"UV-ROLE-001":   4,
		"UV-RO-001":     5,
		"UV-SCHEMA-001": 6,
	}[code]
	if exitError.ExitCode() != wantExit {
		t.Fatalf("exit = %d, want %d; stderr=%q", exitError.ExitCode(), wantExit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	text := stderr.String()
	if strings.Count(text, code) != 1 || strings.Count(text, publicMessage(code)) != 1 {
		t.Fatalf("diagnostic output = %q", text)
	}
	assertSentinelsAbsent(t, text, sentinels...)
}

func publicMessage(code string) string {
	switch code {
	case "UV-CFG-001":
		return "runtime configuration is invalid"
	case "UV-BIND-001":
		return "listen address is not safely acknowledged"
	case "UV-DB-001":
		return "database connection could not be established"
	case "UV-ROLE-001":
		return "database role is not admitted"
	case "UV-RO-001":
		return "database read-only verification failed"
	case "UV-SCHEMA-001":
		return "database schema is not compatible"
	default:
		return "server could not start safely"
	}
}

func buildViewerBinary(t *testing.T) string {
	t.Helper()
	moduleRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	output := filepath.Join(t.TempDir(), "sub2api-usage-viewer")
	cache := filepath.Join(t.TempDir(), "go-cache")
	require.NoError(t, os.MkdirAll(cache, 0o755))
	command := exec.Command("go", "build", "-trimpath", "-o", output, "./cmd/viewer")
	command.Dir = moduleRoot
	command.Env = replaceEnvironment(os.Environ(), "GOCACHE", cache)
	combined, buildErr := command.CombinedOutput()
	if buildErr != nil {
		t.Fatalf("build viewer: %v\n%s", buildErr, combined)
	}
	if filepath.Base(output) != "sub2api-usage-viewer" {
		t.Fatalf("binary name = %q", filepath.Base(output))
	}
	return output
}

func runtimeEnvironment(databaseURL, role, listenAddress string) map[string]string {
	return map[string]string{
		"PATH":                                          os.Getenv("PATH"),
		"SUB2API_USAGE_VIEWER_DATABASE_URL":             databaseURL,
		"SUB2API_USAGE_VIEWER_DATABASE_ROLE":            role,
		"SUB2API_USAGE_VIEWER_LISTEN_ADDR":              listenAddress,
		"SUB2API_USAGE_VIEWER_DB_CONNECT_TIMEOUT":       "500ms",
		"SUB2API_USAGE_VIEWER_DB_ACQUIRE_TIMEOUT":       "1s",
		"SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT":         "2s",
		"SUB2API_USAGE_VIEWER_DB_POOL_MAX_CONNS":        "2",
		"SUB2API_USAGE_VIEWER_DB_POOL_MIN_CONNS":        "0",
		"SUB2API_USAGE_VIEWER_DB_MAX_CONN_LIFETIME":     "5m",
		"SUB2API_USAGE_VIEWER_DB_MAX_CONN_IDLE_TIME":    "1m",
		"SUB2API_USAGE_VIEWER_DB_HEALTH_CHECK_PERIOD":   "30s",
		"SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE":          "24h",
		"SUB2API_USAGE_VIEWER_HTTP_READ_HEADER_TIMEOUT": "1s",
		"SUB2API_USAGE_VIEWER_HTTP_READ_TIMEOUT":        "2s",
		"SUB2API_USAGE_VIEWER_HTTP_WRITE_TIMEOUT":       "2s",
		"SUB2API_USAGE_VIEWER_HTTP_IDLE_TIMEOUT":        "5s",
		"SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT":         "2s",
		"SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK": "false",
	}
}

func environmentList(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(values))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func reserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func socketAccepts(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 20*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func waitForReadyEvent(t *testing.T, output *lockedBuffer, processDone <-chan error) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(output.String(), `"event":"ready"`) {
			return
		}
		select {
		case err := <-processDone:
			t.Fatalf("viewer exited before ready: %v; output=%q", err, output.String())
		case <-deadline.C:
			t.Fatalf("viewer did not report ready; output=%q", output.String())
		case <-ticker.C:
		}
	}
}

func waitForSocketClosed(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for socketAccepts(address) {
		if time.Now().After(deadline) {
			t.Fatalf("socket remains open on %s", address)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertSentinelsAbsent(t *testing.T, text string, sentinels ...string) {
	t.Helper()
	for _, sentinel := range sentinels {
		if sentinel != "" && strings.Contains(text, sentinel) {
			t.Errorf("output disclosed sentinel %q", sentinel)
		}
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func requireRuntimePostgres(t *testing.T) *runtimePostgresHarness {
	t.Helper()
	if !dockerAvailable() {
		if os.Getenv("CI") != "" {
			t.Fatal("Docker is required for integration tests when CI is set")
		}
		t.Skip("Docker is unavailable; skipping disposable PostgreSQL runtime test")
	}
	runtimeHarnessMu.Lock()
	defer runtimeHarnessMu.Unlock()
	if runtimeHarness != nil {
		return runtimeHarness
	}
	harness, err := startRuntimePostgres(context.Background())
	require.NoError(t, err)
	runtimeHarness = harness
	return harness
}

func dockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "info")
	command.Env = os.Environ()
	return command.Run() == nil
}

func startRuntimePostgres(ctx context.Context) (*runtimePostgresHarness, error) {
	image := strings.TrimSpace(os.Getenv(postgresImageEnv))
	if image == "" {
		image = "postgres:15-alpine"
	}
	container, err := tcpostgres.Run(
		ctx,
		image,
		tcpostgres.WithDatabase(runtimeDatabaseName),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("start disposable PostgreSQL: %w", err)
	}
	adminURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("resolve disposable PostgreSQL URL: %w", err)
	}
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("connect disposable PostgreSQL: %w", err)
	}
	harness := &runtimePostgresHarness{container: container, admin: admin, adminURL: adminURL}
	if err := harness.createFixture(ctx); err != nil {
		_ = admin.Close(ctx)
		_ = container.Terminate(ctx)
		return nil, err
	}
	return harness, nil
}

func (harness *runtimePostgresHarness) createFixture(ctx context.Context) error {
	statements := []string{
		`REVOKE CREATE, TEMP ON DATABASE usage_viewer_runtime_test FROM PUBLIC`,
		`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
		`CREATE TABLE public.accounts (id bigint NOT NULL, name varchar NOT NULL, platform varchar NOT NULL, status varchar NOT NULL, deleted_at timestamptz NULL, runtime_object_secret text NULL)`,
		`CREATE TABLE public.users (id bigint NOT NULL, email varchar NOT NULL, username varchar NOT NULL, status varchar NOT NULL, deleted_at timestamptz NULL, password_hash text NULL)`,
		`CREATE TABLE public.usage_logs (id bigint NOT NULL, user_id bigint NOT NULL, account_id bigint NOT NULL, request_id varchar NULL, model varchar NOT NULL, requested_model varchar NULL, input_tokens integer NOT NULL, output_tokens integer NOT NULL, cache_creation_tokens integer NOT NULL, cache_read_tokens integer NOT NULL, total_cost numeric NOT NULL, actual_cost numeric NOT NULL, account_stats_cost numeric NULL, account_rate_multiplier numeric NULL, created_at timestamptz NOT NULL, runtime_object_secret text NULL)`,
		`CREATE ROLE viewer_runtime_role_sentinel LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD 'runtime-password-secret-sentinel'`,
		`ALTER ROLE viewer_runtime_role_sentinel SET default_transaction_read_only = on`,
		`GRANT CONNECT ON DATABASE usage_viewer_runtime_test TO viewer_runtime_role_sentinel`,
		`GRANT USAGE ON SCHEMA public TO viewer_runtime_role_sentinel`,
	}
	for _, statement := range statements {
		if _, err := harness.admin.Exec(ctx, statement); err != nil {
			return fmt.Errorf("create runtime fixture: %w", err)
		}
	}
	return harness.grantContract(ctx)
}

func (harness *runtimePostgresHarness) grantContract(ctx context.Context) error {
	columnsByTable := make(map[string][]string)
	for _, column := range viewerpostgres.CurrentContract().Columns {
		columnsByTable[column.Table] = append(columnsByTable[column.Table], column.Column)
	}
	tables := make([]string, 0, len(columnsByTable))
	for table := range columnsByTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		columns := columnsByTable[table]
		quotedColumns := make([]string, len(columns))
		for index, column := range columns {
			quotedColumns[index] = pgx.Identifier{column}.Sanitize()
		}
		statement := fmt.Sprintf(
			"GRANT SELECT (%s) ON TABLE %s TO %s",
			strings.Join(quotedColumns, ", "),
			pgx.Identifier{"public", table}.Sanitize(),
			pgx.Identifier{runtimeRoleName}.Sanitize(),
		)
		if _, err := harness.admin.Exec(ctx, statement); err != nil {
			return fmt.Errorf("grant runtime contract: %w", err)
		}
	}
	return nil
}

func (harness *runtimePostgresHarness) replaceRequiredRelation(
	t *testing.T,
	relation string,
	install []string,
	remove []string,
) {
	t.Helper()
	revoke, ok := runtimeContractPrivilegeStatement("REVOKE", relation)
	require.True(t, ok, "required relation has contract columns")
	original := relation + "_plan05_original"
	harness.execAdmin(t, revoke)
	harness.execAdmin(t, fmt.Sprintf(
		"ALTER TABLE %s RENAME TO %s",
		pgx.Identifier{"public", relation}.Sanitize(),
		pgx.Identifier{original}.Sanitize(),
	))
	t.Cleanup(func() {
		for _, statement := range remove {
			if _, err := harness.admin.Exec(context.Background(), statement); err != nil {
				t.Errorf("remove isolated runtime relation substitute: %v", err)
			}
		}
		if _, err := harness.admin.Exec(
			context.Background(),
			fmt.Sprintf(
				"ALTER TABLE %s RENAME TO %s",
				pgx.Identifier{"public", original}.Sanitize(),
				pgx.Identifier{relation}.Sanitize(),
			),
		); err != nil {
			t.Errorf("restore isolated runtime ordinary table: %v", err)
			return
		}
		grant, ok := runtimeContractPrivilegeStatement("GRANT", relation)
		if !ok {
			t.Errorf("restored relation %q has no contract columns", relation)
			return
		}
		if _, err := harness.admin.Exec(context.Background(), grant); err != nil {
			t.Errorf("restore isolated runtime relation grants: %v", err)
		}
	})

	for _, statement := range install {
		harness.execAdmin(t, statement)
	}
	grant, ok := runtimeContractPrivilegeStatement("GRANT", relation)
	require.True(t, ok, "substitute relation has contract columns")
	harness.execAdmin(t, grant)
}

func (harness *runtimePostgresHarness) requiredRelationKind(t *testing.T, relation string) string {
	t.Helper()
	var relkind string
	err := harness.admin.QueryRow(
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

func runtimeContractPrivilegeStatement(action, relation string) (string, bool) {
	columns := make([]string, 0)
	for _, column := range viewerpostgres.CurrentContract().Columns {
		if column.Table == relation {
			columns = append(columns, pgx.Identifier{column.Column}.Sanitize())
		}
	}
	if len(columns) == 0 {
		return "", false
	}
	direction := map[string]string{"GRANT": "TO", "REVOKE": "FROM"}[action]
	return fmt.Sprintf(
		"%s SELECT (%s) ON TABLE %s %s %s",
		action,
		strings.Join(columns, ", "),
		pgx.Identifier{"public", relation}.Sanitize(),
		direction,
		pgx.Identifier{runtimeRoleName}.Sanitize(),
	), true
}

func (harness *runtimePostgresHarness) candidateURL(t *testing.T) string {
	t.Helper()
	parsed, err := url.Parse(harness.adminURL)
	require.NoError(t, err)
	parsed.User = url.UserPassword(runtimeRoleName, runtimeRolePassword)
	return parsed.String()
}

func (harness *runtimePostgresHarness) execAdmin(t *testing.T, statement string) {
	t.Helper()
	_, err := harness.admin.Exec(context.Background(), statement)
	require.NoError(t, err)
}

func (harness *runtimePostgresHarness) waitForCandidateSessions(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		err := harness.admin.QueryRow(
			context.Background(),
			`SELECT pg_catalog.count(*) FROM pg_catalog.pg_stat_activity WHERE usename = $1`,
			runtimeRoleName,
		).Scan(&count)
		require.NoError(t, err)
		if count == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("candidate sessions = %d, want %d", count, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
