package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Wei-Shaw/sub2api/usage-viewer/internal/diagnostics"
)

func TestWithReadOnlyTxCommitsSuccessfulCallback(t *testing.T) {
	tx := &fakeTransaction{rows: []*fakeRow{{value: true}}}
	beginner := &fakeTransactionBeginner{tx: tx}
	callbackCalled := false

	err := runReadOnlyTransaction(context.Background(), beginner, time.Second, func(ctx context.Context, got transaction) error {
		callbackCalled = true
		if got != tx {
			t.Fatal("callback received a different transaction")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("callback context has no deadline")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runReadOnlyTransaction() error = %v", err)
	}
	if !callbackCalled || tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("callback=%t commits=%d rollbacks=%d", callbackCalled, tx.commits, tx.rollbacks)
	}
	assertReadOnlyOptions(t, beginner.options)
}

func TestReadOnlyTransactionIsolationIsClosed(t *testing.T) {
	for _, tt := range []struct {
		name      string
		isolation pgx.TxIsoLevel
	}{
		{name: "search", isolation: pgx.ReadCommitted},
		{name: "initial report", isolation: pgx.RepeatableRead},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTransaction{rows: []*fakeRow{{value: true}}}
			beginner := &fakeTransactionBeginner{tx: tx}
			err := runReadOnlyTransactionAtIsolation(context.Background(), beginner, time.Second, tt.isolation, successfulCallback)
			if err != nil {
				t.Fatal(err)
			}
			if beginner.options.IsoLevel != tt.isolation || beginner.options.AccessMode != pgx.ReadOnly {
				t.Fatalf("options=%#v", beginner.options)
			}
		})
	}

	tx := &fakeTransaction{}
	beginner := &fakeTransactionBeginner{tx: tx}
	if err := runReadOnlyTransactionAtIsolation(context.Background(), beginner, time.Second, pgx.Serializable, successfulCallback); diagnostics.CodeOf(err) != diagnostics.CodeDatabaseConnectivity || beginner.calls != 0 {
		t.Fatalf("invalid isolation error=%v begin calls=%d", err, beginner.calls)
	}
}

func TestWithReadOnlyTxRollsBackEveryFailurePath(t *testing.T) {
	tests := []struct {
		name      string
		beginErr  error
		stateRow  *fakeRow
		callback  func(context.Context, transaction) error
		wantCode  diagnostics.Code
		rollbacks int
	}{
		{name: "begin failure", beginErr: errors.New("begin-raw-secret"), wantCode: diagnostics.CodeDatabaseConnectivity},
		{name: "state query failure", stateRow: &fakeRow{err: errors.New("state-raw-secret")}, callback: successfulCallback, wantCode: diagnostics.CodeDatabaseReadOnly, rollbacks: 1},
		{name: "state is writable", stateRow: &fakeRow{value: false}, callback: successfulCallback, wantCode: diagnostics.CodeDatabaseReadOnly, rollbacks: 1},
		{name: "callback failure", stateRow: &fakeRow{value: true}, callback: func(context.Context, transaction) error { return errors.New("callback-raw-secret") }, wantCode: diagnostics.CodeDatabaseConnectivity, rollbacks: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTransaction{}
			if tt.stateRow != nil {
				tx.rows = []*fakeRow{tt.stateRow}
			}
			beginner := &fakeTransactionBeginner{tx: tx, err: tt.beginErr}
			err := runReadOnlyTransaction(context.Background(), beginner, time.Second, tt.callback)
			if diagnostics.CodeOf(err) != tt.wantCode {
				t.Fatalf("diagnostic code = %q, want %q; error = %v", diagnostics.CodeOf(err), tt.wantCode, err)
			}
			if tx.commits != 0 || tx.rollbacks != tt.rollbacks {
				t.Fatalf("commits=%d rollbacks=%d, want 0/%d", tx.commits, tx.rollbacks, tt.rollbacks)
			}
			for _, sentinel := range []string{"begin-raw-secret", "state-raw-secret", "callback-raw-secret"} {
				if strings.Contains(err.Error(), sentinel) {
					t.Errorf("diagnostic disclosed %q", sentinel)
				}
			}
		})
	}
}

func TestWithReadOnlyTxPropagatesCancellationAndDeadline(t *testing.T) {
	t.Run("parent cancellation prevents begin", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		beginner := &fakeTransactionBeginner{tx: &fakeTransaction{}}
		err := runReadOnlyTransaction(ctx, beginner, time.Second, successfulCallback)
		if diagnostics.CodeOf(err) != diagnostics.CodeDatabaseConnectivity || beginner.calls != 0 {
			t.Fatalf("code=%q begin calls=%d", diagnostics.CodeOf(err), beginner.calls)
		}
	})

	t.Run("configured deadline reaches callback", func(t *testing.T) {
		tx := &fakeTransaction{rows: []*fakeRow{{value: true}}}
		beginner := &fakeTransactionBeginner{tx: tx}
		started := time.Now()
		err := runReadOnlyTransaction(context.Background(), beginner, 20*time.Millisecond, func(ctx context.Context, _ transaction) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if diagnostics.CodeOf(err) != diagnostics.CodeDatabaseConnectivity {
			t.Fatalf("diagnostic code = %q", diagnostics.CodeOf(err))
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("deadline took too long: %s", elapsed)
		}
		if tx.rollbacks != 1 || tx.commits != 0 {
			t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
		}
	})
}

func TestWithReadOnlyTxRollsBackAndRepanics(t *testing.T) {
	tx := &fakeTransaction{rows: []*fakeRow{{value: true}}}
	beginner := &fakeTransactionBeginner{tx: tx}
	defer func() {
		if recovered := recover(); recovered != "panic-secret" {
			t.Fatalf("recovered = %#v", recovered)
		}
		if tx.rollbacks != 1 || tx.commits != 0 {
			t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
		}
	}()
	_ = runReadOnlyTransaction(context.Background(), beginner, time.Second, func(context.Context, transaction) error {
		panic("panic-secret")
	})
}

func successfulCallback(context.Context, transaction) error {
	return nil
}

func assertReadOnlyOptions(t *testing.T, options pgx.TxOptions) {
	t.Helper()
	if options.IsoLevel != pgx.ReadCommitted || options.AccessMode != pgx.ReadOnly {
		t.Fatalf("transaction options = %#v", options)
	}
}

type fakeTransactionBeginner struct {
	tx      transaction
	err     error
	calls   int
	options pgx.TxOptions
}

func (b *fakeTransactionBeginner) BeginTx(_ context.Context, options pgx.TxOptions) (transaction, error) {
	b.calls++
	b.options = options
	return b.tx, b.err
}

type fakeTransaction struct {
	rows      []*fakeRow
	commits   int
	rollbacks int
	queries   []string
	commitErr error
}

func (tx *fakeTransaction) QueryRow(_ context.Context, sql string, _ ...any) rowScanner {
	tx.queries = append(tx.queries, sql)
	if len(tx.rows) == 0 {
		return &fakeRow{err: errors.New("unexpected query")}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *fakeTransaction) Commit(context.Context) error {
	tx.commits++
	return tx.commitErr
}

func (tx *fakeTransaction) Rollback(context.Context) error {
	tx.rollbacks++
	return nil
}

type fakeRow struct {
	value any
	err   error
}

func (row *fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return errors.New("unexpected scan destination count")
	}
	switch destination := destinations[0].(type) {
	case *bool:
		value, ok := row.value.(bool)
		if !ok {
			return errors.New("unexpected bool value")
		}
		*destination = value
	case *int:
		value, ok := row.value.(int)
		if !ok {
			return errors.New("unexpected int value")
		}
		*destination = value
	default:
		return errors.New("unsupported scan destination")
	}
	return nil
}
