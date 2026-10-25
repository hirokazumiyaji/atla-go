package atla

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// TxExecutor is the Executor handed to fn inside a transaction.
// It wraps *sql.Tx and owns the savepoint counter.
type TxExecutor struct {
	tx    *sql.Tx
	depth int
}

// QueryContext runs a query inside the transaction.
func (t *TxExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

// ExecContext runs a statement inside the transaction.
func (t *TxExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

// Tx returns the underlying transaction, an escape hatch for raw database/sql
// use. Do not commit or roll it back from inside fn.
func (t *TxExecutor) Tx() *sql.Tx { return t.tx }

// TxRunner is an optional capability for Executor wrappers (hooks, routers).
// RunTx delegates to it, so the wrapper can begin on the right underlying
// executor and re-wrap the transaction executor it hands to fn.
type TxRunner interface {
	RunTx(ctx context.Context, fn func(Executor) error, opts ...TxOption) error
}

// TxOption configures a transaction started by [RunTx].
type TxOption func(*sql.TxOptions)

// WithIsolation sets the isolation level of the transaction.
func WithIsolation(l sql.IsolationLevel) TxOption {
	return func(o *sql.TxOptions) { o.Isolation = l }
}

// ReadOnly makes the transaction read-only.
func ReadOnly() TxOption {
	return func(o *sql.TxOptions) { o.ReadOnly = true }
}

// RunTx runs fn inside a transaction on ex. It chooses the first case that
// matches ex:
//
//  1. A [TxRunner]: RunTx delegates to it.
//  2. A *[TxExecutor] (a nested call): fn runs inside a savepoint and receives a
//     child TxExecutor. Options are rejected with [ErrTxOptionsNested]. The
//     savepoint is released on success, and rolled back to and then released
//     on error, on panic, or when the release fails. Ending the savepoint
//     ignores cancellation of ctx.
//  3. A *sql.Tx: it is wrapped at depth 0 and handled as in case 2; atla never
//     commits or rolls back a transaction it did not begin.
//  4. A [TxBeginner]: RunTx begins a transaction, commits it when fn returns
//     nil, and rolls it back when fn returns an error or panics.
//  5. Anything else: RunTx returns [ErrTxUnsupported].
//
// When fn returns an error, the result is that error joined with any rollback
// error. When fn panics, RunTx rolls back and lets the panic continue.
func RunTx(ctx context.Context, ex Executor, fn func(Executor) error, opts ...TxOption) error {
	switch ex := ex.(type) {
	case TxRunner:
		return ex.RunTx(ctx, fn, opts...)
	case *TxExecutor:
		return ex.runNested(ctx, fn, opts)
	case *sql.Tx:
		return (&TxExecutor{tx: ex}).runNested(ctx, fn, opts)
	case TxBeginner:
		var o sql.TxOptions
		for _, opt := range opts {
			opt(&o)
		}
		tx, err := ex.BeginTx(ctx, &o)
		if err != nil {
			return err
		}
		return finish(&TxExecutor{tx: tx}, fn, tx.Commit, tx.Rollback)
	default:
		return ErrTxUnsupported
	}
}

// runNested runs fn inside the savepoint atla_sp_<depth+1>.
func (t *TxExecutor) runNested(ctx context.Context, fn func(Executor) error, opts []TxOption) error {
	if len(opts) > 0 {
		return ErrTxOptionsNested
	}
	// The name is built from the depth only, never from user input.
	name := "atla_sp_" + strconv.Itoa(t.depth+1)
	if _, err := t.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return err
	}
	// Ending the savepoint must not depend on ctx: if fn cancelled it, a skipped
	// ROLLBACK TO would let the outer transaction commit fn's writes.
	endCtx := context.WithoutCancel(ctx)
	exec := func(query string) error {
		_, err := t.tx.ExecContext(endCtx, query)
		return err
	}
	// ROLLBACK TO keeps the savepoint open, so it is released afterwards;
	// otherwise failed sibling calls would accumulate open savepoints.
	rollback := func() error {
		if err := exec("ROLLBACK TO SAVEPOINT " + name); err != nil {
			return err
		}
		return exec("RELEASE SAVEPOINT " + name)
	}
	// A failed RELEASE (for example on Postgres after fn swallowed an error)
	// leaves the savepoint open; roll it back so the outer transaction stays
	// usable.
	release := func() error {
		if err := exec("RELEASE SAVEPOINT " + name); err != nil {
			return errors.Join(err, rollback())
		}
		return nil
	}
	child := &TxExecutor{tx: t.tx, depth: t.depth + 1}
	return finish(child, fn, release, rollback)
}

// finish runs fn with ex, then calls commit when fn returns nil and rollback
// when fn returns an error or panics. A panic continues after the rollback
// with its original value.
func finish(ex *TxExecutor, fn func(Executor) error, commit, rollback func() error) error {
	done := false
	defer func() {
		if !done {
			_ = rollback()
		}
	}()
	err := fn(ex)
	done = true
	if err != nil {
		if rbErr := rollback(); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err // unwrapped, so err == fnErr still holds
	}
	return commit()
}
