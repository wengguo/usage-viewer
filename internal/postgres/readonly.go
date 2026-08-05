package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const transactionReadOnlySQL = `
SELECT pg_catalog.current_setting('transaction_read_only')::boolean
`

type rowScanner interface {
	Scan(destinations ...any) error
}

type transaction interface {
	QueryRow(ctx context.Context, sql string, arguments ...any) rowScanner
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type transactionBeginner interface {
	BeginTx(ctx context.Context, options pgx.TxOptions) (transaction, error)
}

type nativeTransaction struct {
	tx pgx.Tx
}

func (tx *nativeTransaction) QueryRow(ctx context.Context, sql string, arguments ...any) rowScanner {
	return tx.tx.QueryRow(ctx, sql, arguments...)
}

func (tx *nativeTransaction) Commit(ctx context.Context) error {
	return tx.tx.Commit(ctx)
}

func (tx *nativeTransaction) Rollback(ctx context.Context) error {
	return tx.tx.Rollback(ctx)
}

type poolTransactionBeginner struct {
	pool *pgxpool.Pool
}

func (beginner poolTransactionBeginner) BeginTx(ctx context.Context, options pgx.TxOptions) (transaction, error) {
	tx, err := beginner.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &nativeTransaction{tx: tx}, nil
}

func withReadOnlyTx(
	ctx context.Context,
	pool *pgxpool.Pool,
	timeout time.Duration,
	fn func(context.Context, pgx.Tx) error,
) error {
	return withReadOnlyTxIsolation(ctx, pool, timeout, pgx.ReadCommitted, fn)
}

func withRepeatableReadOnlyTx(
	ctx context.Context,
	pool *pgxpool.Pool,
	timeout time.Duration,
	fn func(context.Context, pgx.Tx) error,
) error {
	return withReadOnlyTxIsolation(ctx, pool, timeout, pgx.RepeatableRead, fn)
}

func withReadOnlyTxIsolation(
	ctx context.Context,
	pool *pgxpool.Pool,
	timeout time.Duration,
	isolation pgx.TxIsoLevel,
	fn func(context.Context, pgx.Tx) error,
) error {
	if pool == nil || fn == nil {
		return diagnosticForStage(stageConnectivity, errors.New("read-only transaction dependency unavailable"))
	}
	return runReadOnlyTransactionAtIsolation(ctx, poolTransactionBeginner{pool: pool}, timeout, isolation, func(queryCtx context.Context, tx transaction) error {
		native, ok := tx.(*nativeTransaction)
		if !ok || native.tx == nil {
			return errors.New("native read-only transaction unavailable")
		}
		return fn(queryCtx, native.tx)
	})
}

func runReadOnlyTransaction(
	ctx context.Context,
	beginner transactionBeginner,
	timeout time.Duration,
	fn func(context.Context, transaction) error,
) (returnErr error) {
	return runReadOnlyTransactionAtIsolation(ctx, beginner, timeout, pgx.ReadCommitted, fn)
}

func runReadOnlyTransactionAtIsolation(
	ctx context.Context,
	beginner transactionBeginner,
	timeout time.Duration,
	isolation pgx.TxIsoLevel,
	fn func(context.Context, transaction) error,
) (returnErr error) {
	if ctx == nil || beginner == nil || fn == nil || timeout <= 0 {
		return diagnosticForStage(stageConnectivity, errors.New("read-only transaction configuration invalid"))
	}
	if isolation != pgx.ReadCommitted && isolation != pgx.RepeatableRead {
		return diagnosticForStage(stageConnectivity, errors.New("read-only transaction isolation invalid"))
	}

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := queryCtx.Err(); err != nil {
		return diagnosticForStage(stageConnectivity, err)
	}

	options := pgx.TxOptions{IsoLevel: isolation, AccessMode: pgx.ReadOnly}
	tx, err := beginner.BeginTx(queryCtx, options)
	if err != nil {
		return diagnosticForStage(stageConnectivity, err)
	}

	committed := false
	defer func() {
		if !committed {
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), cleanupTimeout(timeout))
			_ = tx.Rollback(rollbackCtx)
			rollbackCancel()
		}
		if recovered := recover(); recovered != nil {
			panic(recovered)
		}
	}()

	var readOnly bool
	if err := tx.QueryRow(queryCtx, transactionReadOnlySQL).Scan(&readOnly); err != nil {
		return diagnosticForStage(stageReadOnly, err)
	}
	if !readOnly {
		return diagnosticForStage(stageReadOnly, errors.New("transaction is not read only"))
	}
	if err := fn(queryCtx, tx); err != nil {
		return diagnosticForStage(stageConnectivity, err)
	}
	if err := tx.Commit(queryCtx); err != nil {
		return diagnosticForStage(stageConnectivity, err)
	}
	committed = true
	return nil
}

func cleanupTimeout(timeout time.Duration) time.Duration {
	const maximumCleanupTimeout = time.Second
	if timeout < maximumCleanupTimeout {
		return timeout
	}
	return maximumCleanupTimeout
}
