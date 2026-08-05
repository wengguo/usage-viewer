//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestReadOnlyTransactionStateAndMutationDenial(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))

	err = withReadOnlyTx(context.Background(), pool, cfg.DatabaseQueryTimeout, func(ctx context.Context, tx pgx.Tx) error {
		var readOnly bool
		if err := tx.QueryRow(ctx, `SELECT pg_catalog.current_setting('transaction_read_only')::boolean`).Scan(&readOnly); err != nil {
			return err
		}
		if !readOnly {
			return errors.New("transaction is writable")
		}
		return nil
	})
	require.NoError(t, err)

	mutationTests := []struct {
		name string
		sql  string
	}{
		{name: "insert", sql: `INSERT INTO public.other_records (id) VALUES (1)`},
		{name: "temporary table", sql: `CREATE TEMP TABLE mutation_canary (id bigint)`},
	}
	for _, tt := range mutationTests {
		t.Run(tt.name, func(t *testing.T) {
			err := withReadOnlyTx(context.Background(), pool, cfg.DatabaseQueryTimeout, func(ctx context.Context, tx pgx.Tx) error {
				_, mutationErr := tx.Exec(ctx, tt.sql)
				return mutationErr
			})
			if diagnostics.CodeOf(err) != diagnostics.CodeDatabaseConnectivity {
				t.Fatalf("diagnostic code = %q; error = %v", diagnostics.CodeOf(err), err)
			}
		})
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("acquired connections = %d, want 0", acquired)
	}
}

func TestReadOnlyCancellationRemovesActivityAndReleasesPool(t *testing.T) {
	harness := requirePostgresIntegrationHarness(t)
	cfg := harness.candidateConfig(integrationRoleName, integrationRolePassword)
	pool, err := OpenPool(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, RunPreflight(context.Background(), pool, cfg))

	ctx, cancel := context.WithCancel(context.Background())
	backendPID := make(chan int32, 1)
	result := make(chan error, 1)
	go func() {
		result <- withReadOnlyTx(ctx, pool, 20*time.Second, func(queryCtx context.Context, tx pgx.Tx) error {
			var pid int32
			if err := tx.QueryRow(queryCtx, `SELECT pg_catalog.pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			backendPID <- pid
			_, err := tx.Exec(queryCtx, `SELECT pg_catalog.pg_sleep(30)`)
			return err
		})
	}()

	pid := <-backendPID
	cancel()
	err = <-result
	if diagnostics.CodeOf(err) != diagnostics.CodeDatabaseConnectivity {
		t.Fatalf("diagnostic code = %q; error = %v", diagnostics.CodeOf(err), err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var active int
		err := harness.admin.QueryRow(
			context.Background(),
			`SELECT pg_catalog.count(*) FROM pg_catalog.pg_stat_activity WHERE pid = $1 AND state <> 'idle'`,
			pid,
		).Scan(&active)
		require.NoError(t, err)
		if active == 0 && pool.Stat().AcquiredConns() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("canceled backend remains active=%d acquired=%d", active, pool.Stat().AcquiredConns())
		}
		time.Sleep(25 * time.Millisecond)
	}
}
