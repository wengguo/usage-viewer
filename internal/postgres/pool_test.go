package postgres

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestPoolConfigUsesBoundedRuntimeSettings(t *testing.T) {
	cfg := poolTestConfig()
	poolConfig, err := buildPoolConfig(cfg, func(context.Context, *pgx.Conn, string) error { return nil })
	if err != nil {
		t.Fatalf("buildPoolConfig() error = %v", err)
	}
	if poolConfig.MaxConns != cfg.DatabasePoolMaxConns || poolConfig.MinConns != cfg.DatabasePoolMinConns {
		t.Fatalf("pool bounds = %d/%d", poolConfig.MinConns, poolConfig.MaxConns)
	}
	if poolConfig.MaxConns > 4 {
		t.Fatalf("MaxConns = %d, exceeds hard maximum", poolConfig.MaxConns)
	}
	if poolConfig.MaxConnLifetime != cfg.DatabaseMaxConnLifetime || poolConfig.MaxConnIdleTime != cfg.DatabaseMaxConnIdleTime || poolConfig.HealthCheckPeriod != cfg.DatabaseHealthCheckPeriod {
		t.Fatal("pool lifecycle settings do not match Config")
	}
	if poolConfig.ConnConfig.ConnectTimeout != cfg.DatabaseConnectTimeout {
		t.Fatalf("ConnectTimeout = %s", poolConfig.ConnConfig.ConnectTimeout)
	}
	wantMilliseconds := strconv.FormatInt(cfg.DatabaseQueryTimeout.Milliseconds(), 10)
	if poolConfig.ConnConfig.RuntimeParams["application_name"] != "sub2api-usage-viewer" ||
		poolConfig.ConnConfig.RuntimeParams["statement_timeout"] != wantMilliseconds ||
		poolConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] != wantMilliseconds {
		t.Fatalf("runtime params = %#v", poolConfig.ConnConfig.RuntimeParams)
	}
	if _, exists := poolConfig.ConnConfig.RuntimeParams["default_transaction_read_only"]; exists {
		t.Fatal("pool config must observe, not set, the read-only default")
	}
	if poolConfig.AfterConnect == nil {
		t.Fatal("AfterConnect is nil")
	}
}

func TestPoolConfigRejectsConnectionsAboveHardMaximum(t *testing.T) {
	cfg := poolTestConfig()
	cfg.DatabasePoolMaxConns = 5
	_, err := buildPoolConfig(cfg, func(context.Context, *pgx.Conn, string) error { return nil })
	if diagnostics.CodeOf(err) != diagnostics.CodeConfiguration {
		t.Fatalf("diagnostic code = %q, want %q", diagnostics.CodeOf(err), diagnostics.CodeConfiguration)
	}
}

func TestPoolConfigAfterConnectAlwaysRunsAdmission(t *testing.T) {
	cfg := poolTestConfig()
	calls := 0
	admit := func(ctx context.Context, conn *pgx.Conn, expectedRole string) error {
		calls++
		if conn != nil || expectedRole != cfg.ExpectedDatabaseRole {
			t.Fatalf("conn=%v role=%q", conn, expectedRole)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > cfg.DatabaseAcquireTimeout+cfg.DatabaseQueryTimeout {
			t.Fatal("admission context is not bounded")
		}
		return errors.New("admission-rejection-secret")
	}
	poolConfig, err := buildPoolConfig(cfg, admit)
	if err != nil {
		t.Fatal(err)
	}
	err = poolConfig.AfterConnect(context.Background(), nil)
	if calls != 1 || err == nil {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestRunPreflightUsesReadOnlyTransactionAndReleasesConnection(t *testing.T) {
	cfg := poolTestConfig()
	tx := &fakeTransaction{rows: []*fakeRow{{value: true}, {value: 1}}}
	connection := &fakePooledConnection{beginner: &fakeTransactionBeginner{tx: tx}}
	acquirer := &fakePoolAcquirer{connection: connection}
	admissionCalls := 0

	err := runPreflight(context.Background(), acquirer, cfg, func(context.Context, *pgx.Conn, string) error {
		admissionCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("runPreflight() error = %v", err)
	}
	if admissionCalls != 1 || !connection.released || tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("admissions=%d released=%t commits=%d rollbacks=%d", admissionCalls, connection.released, tx.commits, tx.rollbacks)
	}
	assertReadOnlyOptions(t, connection.beginner.options)
	if len(tx.queries) != 2 || tx.queries[0] != transactionReadOnlySQL || tx.queries[1] != connectivityProbeSQL {
		t.Fatalf("transaction queries = %#v", tx.queries)
	}
}

func TestRunPreflightReleasesConnectionOnAdmissionAndTransactionFailures(t *testing.T) {
	tests := []struct {
		name     string
		admitErr error
		beginner *fakeTransactionBeginner
		wantCode diagnostics.Code
	}{
		{name: "admission", admitErr: diagnostics.New(diagnostics.CodeDatabasePrivilege, diagnostics.CategoryDatabasePrivilege, ""), beginner: &fakeTransactionBeginner{}, wantCode: diagnostics.CodeDatabasePrivilege},
		{name: "begin", beginner: &fakeTransactionBeginner{err: errors.New("begin-secret")}, wantCode: diagnostics.CodeDatabaseConnectivity},
		{name: "state", beginner: &fakeTransactionBeginner{tx: &fakeTransaction{rows: []*fakeRow{{value: false}}}}, wantCode: diagnostics.CodeDatabaseReadOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connection := &fakePooledConnection{beginner: tt.beginner}
			err := runPreflight(context.Background(), &fakePoolAcquirer{connection: connection}, poolTestConfig(), func(context.Context, *pgx.Conn, string) error {
				return tt.admitErr
			})
			if diagnostics.CodeOf(err) != tt.wantCode || !connection.released {
				t.Fatalf("code=%q released=%t error=%v", diagnostics.CodeOf(err), connection.released, err)
			}
		})
	}
}

func TestRunPreflightPreservesAcquireDiagnostic(t *testing.T) {
	want := diagnostics.New(diagnostics.CodeDatabasePrivilege, diagnostics.CategoryDatabasePrivilege, "")
	err := runPreflight(context.Background(), &fakePoolAcquirer{err: want}, poolTestConfig(), func(context.Context, *pgx.Conn, string) error { return nil })
	if diagnostics.CodeOf(err) != diagnostics.CodeDatabasePrivilege {
		t.Fatalf("diagnostic code = %q", diagnostics.CodeOf(err))
	}
}

func poolTestConfig() config.Config {
	return config.Config{
		DatabaseURL:               "postgres://viewer@127.0.0.1/sub2api?sslmode=disable",
		ExpectedDatabaseRole:      "viewer-role-secret",
		DatabaseConnectTimeout:    3 * time.Second,
		DatabaseAcquireTimeout:    2 * time.Second,
		DatabaseQueryTimeout:      4 * time.Second,
		DatabasePoolMaxConns:      4,
		DatabasePoolMinConns:      1,
		DatabaseMaxConnLifetime:   30 * time.Minute,
		DatabaseMaxConnIdleTime:   5 * time.Minute,
		DatabaseHealthCheckPeriod: time.Minute,
	}
}

type fakePoolAcquirer struct {
	connection pooledConnection
	err        error
}

func (a *fakePoolAcquirer) Acquire(context.Context) (pooledConnection, error) {
	return a.connection, a.err
}

type fakePooledConnection struct {
	beginner *fakeTransactionBeginner
	released bool
}

func (*fakePooledConnection) Raw() *pgx.Conn {
	return nil
}

func (connection *fakePooledConnection) BeginTx(ctx context.Context, options pgx.TxOptions) (transaction, error) {
	return connection.beginner.BeginTx(ctx, options)
}

func (connection *fakePooledConnection) Release() {
	connection.released = true
}
