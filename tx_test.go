package atla_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	atla "github.com/hirokazumiyaji/atla-go"
	"github.com/hirokazumiyaji/atla-go/internal/fakedb"
)

var errX = errors.New("x")

// checkLog asserts the script log and that every expectation was consumed.
func checkLog(t *testing.T, s *fakedb.Script, want ...string) {
	t.Helper()
	if got := s.Log(); !slices.Equal(got, want) {
		t.Errorf("log:\n got %q\nwant %q", got, want)
	}
	s.Done(t)
}

// mustPanic runs f and returns the value it panicked with.
func mustPanic(t *testing.T, f func()) (v any) {
	t.Helper()
	defer func() { v = recover() }()
	f()
	t.Fatal("did not panic")
	return nil
}

func TestRunTxCommit(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectExec(`INSERT INTO "users" ("name") VALUES ($1)`, "alice").Result(1)
	s.ExpectQuery(`SELECT 1`).Rows([]string{"n"})

	err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
		tx, ok := ex.(*atla.TxExecutor)
		if !ok {
			t.Fatalf("fn got %T, want *atla.TxExecutor", ex)
		}
		if tx.Tx() == nil {
			t.Fatal("Tx() = nil")
		}
		if _, err := ex.ExecContext(ctx, `INSERT INTO "users" ("name") VALUES ($1)`, "alice"); err != nil {
			return err
		}
		rows, err := ex.QueryContext(ctx, `SELECT 1`)
		if err != nil {
			return err
		}
		return rows.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	checkLog(t, s, "BEGIN", `EXEC INSERT INTO "users" ("name") VALUES ($1)`, "QUERY SELECT 1", "ROWS CLOSED", "COMMIT")
}

func TestRunTxCommitError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, _ := fakedb.Open(t)

	err := atla.RunTx(ctx, db, func(atla.Executor) error {
		cancel() // database/sql refuses to commit once the context is done
		return nil
	})
	if !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("RunTx() = %v, want the commit error", err)
	}
}

func TestRunTxRollbackOnError(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)

	err := atla.RunTx(ctx, db, func(atla.Executor) error { return errX })
	if err != errX { //nolint:errorlint // a clean rollback returns fn's error unwrapped
		t.Fatalf("RunTx() = %v, want %v", err, errX)
	}
	checkLog(t, s, "BEGIN", "ROLLBACK")
}

func TestRunTxRollbackOnPanic(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)

	v := mustPanic(t, func() {
		_ = atla.RunTx(ctx, db, func(atla.Executor) error { panic("boom") })
	})
	if v != "boom" {
		t.Fatalf("recover() = %v, want boom", v)
	}
	checkLog(t, s, "BEGIN", "ROLLBACK")
}

func TestRunTxOptions(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)

	err := atla.RunTx(ctx, db, func(atla.Executor) error { return nil },
		atla.WithIsolation(sql.LevelSerializable), atla.ReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	checkLog(t, s, "BEGIN iso=6 ro=true", "COMMIT")
}

func TestRunTxBeginError(t *testing.T) {
	called := false
	err := atla.RunTx(context.Background(), failingBeginner{}, func(atla.Executor) error {
		called = true
		return nil
	})
	if !errors.Is(err, errX) || called {
		t.Fatalf("RunTx() = %v, fn called = %v; want %v, false", err, called, errX)
	}
}

func TestRunTxNestedSavepoint(t *testing.T) {
	ctx := context.Background()

	t.Run("inner error", func(t *testing.T) {
		db, s := fakedb.Open(t)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			if err := atla.RunTx(ctx, ex, func(atla.Executor) error { return errX }); !errors.Is(err, errX) {
				t.Errorf("inner RunTx() = %v, want %v", err, errX)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"COMMIT")
	})

	// A cancelled inner context must not skip the savepoint rollback, or the
	// inner writes would be committed by the outer transaction.
	t.Run("inner error with cancelled ctx", func(t *testing.T) {
		db, s := fakedb.Open(t)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			inner, cancel := context.WithCancel(ctx)
			defer cancel()
			err := atla.RunTx(inner, ex, func(atla.Executor) error {
				cancel()
				return errX
			})
			if !errors.Is(err, errX) {
				t.Errorf("inner RunTx() = %v, want %v", err, errX)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"COMMIT")
	})

	t.Run("inner panic with cancelled ctx", func(t *testing.T) {
		db, s := fakedb.Open(t)
		v := mustPanic(t, func() {
			_ = atla.RunTx(ctx, db, func(ex atla.Executor) error {
				inner, cancel := context.WithCancel(ctx)
				defer cancel()
				return atla.RunTx(inner, ex, func(atla.Executor) error {
					cancel()
					panic("boom")
				})
			})
		})
		if v != "boom" {
			t.Fatalf("recover() = %v, want boom", v)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"ROLLBACK")
	})

	// A failed RELEASE (e.g. Postgres in an aborted state) rolls the savepoint
	// back so the outer transaction stays usable.
	t.Run("release fails", func(t *testing.T) {
		db, s := fakedb.Open(t)
		errRelease := errors.New("release failed")
		s.FailNext("RELEASE SAVEPOINT", errRelease)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			err := atla.RunTx(ctx, ex, func(atla.Executor) error { return nil })
			if !errors.Is(err, errRelease) {
				t.Errorf("inner RunTx() = %v, want %v", err, errRelease)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"COMMIT")
	})

	t.Run("release and rollback fail", func(t *testing.T) {
		db, s := fakedb.Open(t)
		errRelease := errors.New("release failed")
		errRollback := errors.New("rollback failed")
		s.FailNext("RELEASE SAVEPOINT", errRelease)
		s.FailNext("ROLLBACK TO SAVEPOINT", errRollback)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			return atla.RunTx(ctx, ex, func(atla.Executor) error { return nil })
		})
		if !errors.Is(err, errRelease) || !errors.Is(err, errRollback) {
			t.Fatalf("RunTx() = %v, want both %v and %v", err, errRelease, errRollback)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
			"ROLLBACK")
	})

	t.Run("inner ok", func(t *testing.T) {
		db, s := fakedb.Open(t)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			return atla.RunTx(ctx, ex, func(inner atla.Executor) error {
				if _, ok := inner.(*atla.TxExecutor); !ok {
					t.Errorf("inner fn got %T, want *atla.TxExecutor", inner)
				}
				return nil
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		checkLog(t, s, "BEGIN", "EXEC SAVEPOINT atla_sp_1", "EXEC RELEASE SAVEPOINT atla_sp_1", "COMMIT")
	})

	t.Run("doubly nested", func(t *testing.T) {
		db, s := fakedb.Open(t)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			return atla.RunTx(ctx, ex, func(ex atla.Executor) error {
				return atla.RunTx(ctx, ex, func(atla.Executor) error { return nil })
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC SAVEPOINT atla_sp_2",
			"EXEC RELEASE SAVEPOINT atla_sp_2",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"COMMIT")
	})

	t.Run("inner panic", func(t *testing.T) {
		db, s := fakedb.Open(t)
		v := mustPanic(t, func() {
			_ = atla.RunTx(ctx, db, func(ex atla.Executor) error {
				return atla.RunTx(ctx, ex, func(atla.Executor) error { panic("boom") })
			})
		})
		if v != "boom" {
			t.Fatalf("recover() = %v, want boom", v)
		}
		checkLog(t, s, "BEGIN",
			"EXEC SAVEPOINT atla_sp_1",
			"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
			"EXEC RELEASE SAVEPOINT atla_sp_1",
			"ROLLBACK")
	})

	t.Run("options", func(t *testing.T) {
		db, s := fakedb.Open(t)
		err := atla.RunTx(ctx, db, func(ex atla.Executor) error {
			called := false
			err := atla.RunTx(ctx, ex, func(atla.Executor) error {
				called = true
				return nil
			}, atla.WithIsolation(sql.LevelSerializable))
			if !errors.Is(err, atla.ErrTxOptionsNested) || called {
				t.Errorf("nested RunTx() = %v, fn called = %v; want %v, false", err, called, atla.ErrTxOptionsNested)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		checkLog(t, s, "BEGIN", "COMMIT")
	})
}

func TestRunTxRawSQLTx(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = atla.RunTx(ctx, tx, func(ex atla.Executor) error {
		te, ok := ex.(*atla.TxExecutor)
		if !ok || te.Tx() != tx {
			t.Errorf("fn got %T wrapping the wrong transaction", ex)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := atla.RunTx(ctx, tx, func(atla.Executor) error { return errX }); !errors.Is(err, errX) {
		t.Fatalf("RunTx() = %v, want %v", err, errX)
	}
	if err := atla.RunTx(ctx, tx, func(atla.Executor) error { return nil }, atla.ReadOnly()); !errors.Is(err, atla.ErrTxOptionsNested) {
		t.Fatalf("RunTx(ReadOnly) = %v, want %v", err, atla.ErrTxOptionsNested)
	}
	// atla committed nothing: the transaction is still the caller's to finish.
	checkLog(t, s, "BEGIN",
		"EXEC SAVEPOINT atla_sp_1",
		"EXEC RELEASE SAVEPOINT atla_sp_1",
		"EXEC SAVEPOINT atla_sp_1",
		"EXEC ROLLBACK TO SAVEPOINT atla_sp_1",
		"EXEC RELEASE SAVEPOINT atla_sp_1")
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() = %v; atla must not finish the caller's transaction", err)
	}
}

func TestRunTxDelegatesToTxRunner(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	r := &fakeRunner{err: errX}
	fn := func(atla.Executor) error { return nil }

	err := atla.RunTx(ctx, r, fn, atla.ReadOnly())
	if !errors.Is(err, errX) {
		t.Fatalf("RunTx() = %v, want %v", err, errX)
	}
	if r.calls != 1 || r.ctx != ctx || r.fn == nil || len(r.opts) != 1 {
		t.Fatalf("runner got calls=%d ctx=%v fn=%v opts=%d; want one call with ctx, fn, 1 option",
			r.calls, r.ctx, r.fn != nil, len(r.opts))
	}
	var o sql.TxOptions
	r.opts[0](&o)
	if !o.ReadOnly {
		t.Errorf("forwarded option did not set ReadOnly")
	}
}

func TestRunTxUnsupported(t *testing.T) {
	err := atla.RunTx(context.Background(), plainExecutor{}, func(atla.Executor) error { return nil })
	if !errors.Is(err, atla.ErrTxUnsupported) {
		t.Fatalf("RunTx() = %v, want %v", err, atla.ErrTxUnsupported)
	}
}

type ctxKey struct{}

// plainExecutor is an Executor that cannot begin transactions.
type plainExecutor struct{}

func (plainExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("plainExecutor: not implemented")
}

func (plainExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("plainExecutor: not implemented")
}

// failingBeginner is a TxBeginner whose BeginTx fails with errX.
type failingBeginner struct{ plainExecutor }

func (failingBeginner) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errX
}

// fakeRunner is a TxRunner that records its call.
type fakeRunner struct {
	plainExecutor
	err   error
	calls int
	ctx   context.Context
	fn    func(atla.Executor) error
	opts  []atla.TxOption
}

func (r *fakeRunner) RunTx(ctx context.Context, fn func(atla.Executor) error, opts ...atla.TxOption) error {
	r.calls++
	r.ctx, r.fn, r.opts = ctx, fn, opts
	return r.err
}
