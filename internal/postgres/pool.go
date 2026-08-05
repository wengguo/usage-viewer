package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/config"
	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

const hardMaximumPoolConnections int32 = 4

type admissionCheck func(context.Context, *pgx.Conn, string) error

func OpenPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	if ctx == nil {
		return nil, diagnosticForStage(stageConnectivity, errors.New("pool context unavailable"))
	}
	poolConfig, err := buildPoolConfig(cfg, CheckConnectionAdmission)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, diagnosticForStage(stageConnectivity, err)
	}
	return pool, nil
}

// OpenLightPool opens a pool without per-connection role admission. It is used
// when the viewer shares database credentials with the main application, so
// the dedicated read-only role constraints do not apply.
func OpenLightPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	if ctx == nil {
		return nil, diagnosticForStage(stageConnectivity, errors.New("pool context unavailable"))
	}
	lightAdmit := func(ctx context.Context, conn *pgx.Conn, _ string) error {
		return CheckLightAdmission(ctx, conn)
	}
	poolConfig, err := buildPoolConfig(cfg, lightAdmit)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, diagnosticForStage(stageConnectivity, err)
	}
	return pool, nil
}

func buildPoolConfig(cfg config.Config, admit admissionCheck) (*pgxpool.Config, error) {
	if admit == nil || cfg.DatabasePoolMaxConns < 1 || cfg.DatabasePoolMaxConns > hardMaximumPoolConnections ||
		cfg.DatabasePoolMinConns < 0 || cfg.DatabasePoolMinConns > cfg.DatabasePoolMaxConns ||
		cfg.DatabaseConnectTimeout <= 0 || cfg.DatabaseAcquireTimeout <= 0 || cfg.DatabaseQueryTimeout < time.Millisecond ||
		cfg.DatabaseMaxConnLifetime <= 0 || cfg.DatabaseMaxConnIdleTime <= 0 || cfg.DatabaseHealthCheckPeriod <= 0 {
		return nil, diagnostics.New(diagnostics.CodeConfiguration, diagnostics.CategoryConfiguration, "runtime configuration is invalid")
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, diagnosticForStage(stageConnectivity, err)
	}
	poolConfig.MaxConns = cfg.DatabasePoolMaxConns
	poolConfig.MinConns = cfg.DatabasePoolMinConns
	poolConfig.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.DatabaseMaxConnIdleTime
	poolConfig.HealthCheckPeriod = cfg.DatabaseHealthCheckPeriod
	poolConfig.ConnConfig.ConnectTimeout = cfg.DatabaseConnectTimeout
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "sub2api-usage-viewer"
	timeoutMilliseconds := strconv.FormatInt(cfg.DatabaseQueryTimeout.Milliseconds(), 10)
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = timeoutMilliseconds
	poolConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = timeoutMilliseconds
	poolConfig.AfterConnect = boundedAfterConnect(cfg, admit)
	return poolConfig, nil
}

func boundedAfterConnect(cfg config.Config, admit admissionCheck) func(context.Context, *pgx.Conn) error {
	return func(ctx context.Context, conn *pgx.Conn) error {
		admissionCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseAcquireTimeout+cfg.DatabaseQueryTimeout)
		defer cancel()
		err := admit(admissionCtx, conn, cfg.ExpectedDatabaseRole)
		if err == nil {
			return nil
		}
		var diagnostic *diagnostics.Diagnostic
		if errors.As(err, &diagnostic) {
			return diagnostic
		}
		return diagnosticForStage(stageConnectivity, err)
	}
}

func RunPreflight(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) error {
	if pool == nil {
		return diagnosticForStage(stageConnectivity, errors.New("pool unavailable"))
	}
	return runPreflight(ctx, nativePoolAcquirer{pool: pool}, cfg, CheckConnectionAdmission)
}

func RunLightPreflight(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) error {
	if pool == nil {
		return diagnosticForStage(stageConnectivity, errors.New("pool unavailable"))
	}
	return runLightPreflight(ctx, nativePoolAcquirer{pool: pool}, cfg)
}

type poolAcquirer interface {
	Acquire(ctx context.Context) (pooledConnection, error)
}

type pooledConnection interface {
	transactionBeginner
	Raw() *pgx.Conn
	Release()
}

type nativePoolAcquirer struct {
	pool *pgxpool.Pool
}

func (acquirer nativePoolAcquirer) Acquire(ctx context.Context) (pooledConnection, error) {
	connection, err := acquirer.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &nativePooledConnection{connection: connection}, nil
}

type nativePooledConnection struct {
	connection *pgxpool.Conn
}

func (connection *nativePooledConnection) Raw() *pgx.Conn {
	return connection.connection.Conn()
}

func (connection *nativePooledConnection) BeginTx(ctx context.Context, options pgx.TxOptions) (transaction, error) {
	tx, err := connection.connection.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &nativeTransaction{tx: tx}, nil
}

func (connection *nativePooledConnection) Release() {
	connection.connection.Release()
}

func runLightPreflight(ctx context.Context, acquirer poolAcquirer, cfg config.Config) error {
	if ctx == nil || acquirer == nil || cfg.DatabaseAcquireTimeout <= 0 || cfg.DatabaseQueryTimeout <= 0 {
		return diagnosticForStage(stageConnectivity, errors.New("preflight configuration invalid"))
	}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, cfg.DatabaseAcquireTimeout)
	defer acquireCancel()
	connection, err := acquirer.Acquire(acquireCtx)
	if err != nil {
		return sanitizeConnectionError(err)
	}
	defer connection.Release()

	admissionCtx, admissionCancel := context.WithTimeout(ctx, cfg.DatabaseAcquireTimeout+cfg.DatabaseQueryTimeout)
	err = CheckLightAdmission(admissionCtx, connection.Raw())
	admissionCancel()
	if err != nil {
		var diagnostic *diagnostics.Diagnostic
		if errors.As(err, &diagnostic) {
			return diagnostic
		}
		return diagnosticForStage(stageConnectivity, err)
	}
	return nil
}

func runPreflight(ctx context.Context, acquirer poolAcquirer, cfg config.Config, admit admissionCheck) error {
	if ctx == nil || acquirer == nil || admit == nil || cfg.DatabaseAcquireTimeout <= 0 || cfg.DatabaseQueryTimeout <= 0 {
		return diagnosticForStage(stageConnectivity, errors.New("preflight configuration invalid"))
	}
	acquireCtx, acquireCancel := context.WithTimeout(ctx, cfg.DatabaseAcquireTimeout)
	defer acquireCancel()
	connection, err := acquirer.Acquire(acquireCtx)
	if err != nil {
		return sanitizeConnectionError(err)
	}
	defer connection.Release()

	admissionCtx, admissionCancel := context.WithTimeout(ctx, cfg.DatabaseAcquireTimeout+cfg.DatabaseQueryTimeout)
	err = admit(admissionCtx, connection.Raw(), cfg.ExpectedDatabaseRole)
	admissionCancel()
	if err != nil {
		var diagnostic *diagnostics.Diagnostic
		if errors.As(err, &diagnostic) {
			return diagnostic
		}
		return diagnosticForStage(stageConnectivity, err)
	}

	return runReadOnlyTransaction(ctx, connection, cfg.DatabaseQueryTimeout, func(queryCtx context.Context, tx transaction) error {
		var probe int
		if err := tx.QueryRow(queryCtx, connectivityProbeSQL).Scan(&probe); err != nil {
			return err
		}
		if probe != 1 {
			return errors.New("transaction probe returned unexpected evidence")
		}
		return nil
	})
}

func sanitizeConnectionError(err error) error {
	var diagnostic *diagnostics.Diagnostic
	if errors.As(err, &diagnostic) {
		return diagnostic
	}
	return diagnosticForStage(stageConnectivity, err)
}
