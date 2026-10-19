package fakedb_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/internal/fakedb"
)

// recorder captures Error calls made by Script.Done.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}

func (r *recorder) Error(args ...any) { r.errs = append(r.errs, fmt.Sprint(args...)) }

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func assertLog(t *testing.T, s *fakedb.Script, want ...string) {
	t.Helper()
	if got := s.Log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Log() = %q, want %q", got, want)
	}
}

func TestFakeDB(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectQuery("SELECT 1", 7).Rows([]string{"x"}, []driver.Value{int64(1)})

	rows, err := db.QueryContext(ctx, "SELECT 1", 7)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.OpenRows(); n != 1 {
		t.Fatalf("OpenRows() = %d while open, want 1", n)
	}
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"x"}) {
		t.Fatalf("Columns() = %q, %v", cols, err)
	}
	var got []int64
	for rows.Next() {
		var x int64
		if err := rows.Scan(&x); err != nil {
			t.Fatal(err)
		}
		got = append(got, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("scanned %v, want [1]", got)
	}
	if n := s.OpenRows(); n != 0 {
		t.Fatalf("OpenRows() = %d after Close, want 0", n)
	}
	assertLog(t, s, "QUERY SELECT 1", "ROWS CLOSED")
	s.Done(t)
}

func TestFakeDBUnexpected(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)

	_, err := db.ExecContext(ctx, "DELETE FROM users")
	if err == nil || !strings.Contains(err.Error(), "DELETE FROM users") {
		t.Fatalf("unexpected exec err = %v, want error containing the SQL", err)
	}

	s.ExpectQuery("SELECT 2", int64(1))
	_, err = db.QueryContext(ctx, "SELECT 3") //nolint:rowserrcheck,sqlclosecheck // err is non-nil
	if err == nil || !strings.Contains(err.Error(), "SELECT 3") {
		t.Fatalf("unexpected query err = %v, want error containing the SQL", err)
	}
	_, err = db.QueryContext(ctx, "SELECT 2", int64(2)) //nolint:rowserrcheck,sqlclosecheck // err is non-nil
	if err == nil || !strings.Contains(err.Error(), "SELECT 2") {
		t.Fatalf("wrong-args query err = %v, want error containing the SQL", err)
	}

	r := &recorder{TB: t}
	s.Done(r)
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], "SELECT 2") {
		t.Fatalf("Done reported %q, want one unmet expectation for SELECT 2", r.errs)
	}
}

func TestFakeDBExecResultAndErr(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	boom := errors.New("boom")
	name := "bob"
	var nilPtr *string
	s.ExpectExec("UPDATE t SET a = ?, b = ?, c = ?", &name, nilPtr, nil).Result(3)
	s.ExpectExec("DELETE FROM t").Err(boom)

	res, err := db.ExecContext(ctx, "UPDATE t SET a = ?, b = ?, c = ?", &name, nilPtr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 3 {
		t.Fatalf("RowsAffected() = %d, %v; want 3", n, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM t"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	assertLog(t, s, "EXEC UPDATE t SET a = ?, b = ?, c = ?", "EXEC DELETE FROM t")
	s.Done(t)
}

func TestFakeDBQueryErr(t *testing.T) {
	db, s := fakedb.Open(t)
	boom := errors.New("boom")
	s.ExpectQuery("SELECT 1").Err(boom)
	_, err := db.QueryContext(context.Background(), "SELECT 1") //nolint:rowserrcheck,sqlclosecheck // err is non-nil
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := s.OpenRows(); n != 0 {
		t.Fatalf("OpenRows() = %d, want 0", n)
	}
	s.Done(t)
}

func TestFakeDBTx(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectExec("INSERT INTO t").Result(1)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT sp1"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO t"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT sp1"); err != nil {
		t.Fatal(err)
	}
	res, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT sp1")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 0 {
		t.Fatalf("savepoint RowsAffected() = %d, %v; want 0", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertLog(t, s,
		"BEGIN",
		"EXEC SAVEPOINT sp1",
		"EXEC INSERT INTO t",
		"EXEC ROLLBACK TO SAVEPOINT sp1",
		"EXEC RELEASE SAVEPOINT sp1",
		"COMMIT",
		"BEGIN iso=6 ro=true",
		"ROLLBACK",
	)
	s.Done(t)
}
