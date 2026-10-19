package atla

import (
	"errors"
	"io"
	"testing"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

type fakePgError struct{ code string }

func (e *fakePgError) Error() string    { return "pg error " + e.code }
func (e *fakePgError) SQLState() string { return e.code }

type fakeSQLiteError struct{ code int }

func (e *fakeSQLiteError) Error() string { return "sqlite error" }
func (e *fakeSQLiteError) Code() int     { return e.code }

func TestDBErrorIsAndAs(t *testing.T) {
	pgErr := &fakePgError{code: "23505"}
	err := wrapDBError(dialect.Postgres, "insert", "users", pgErr)
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("errors.Is(%v, ErrUniqueViolation) = false", err)
	}
	var target *fakePgError
	if !errors.As(err, &target) || target != pgErr {
		t.Fatalf("errors.As did not find the driver error: %v", err)
	}
	if got, want := err.Error(), "atla: insert users: "+pgErr.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	var dbErr *DBError
	if !errors.As(err, &dbErr) {
		t.Fatalf("errors.As(*DBError) = false")
	}
	if dbErr.Op != "insert" || dbErr.Table != "users" || !errors.Is(dbErr.Kind, ErrUniqueViolation) || !errors.Is(dbErr.Err, pgErr) {
		t.Fatalf("DBError fields = %+v", dbErr)
	}
}

func TestWrapDBErrorKinds(t *testing.T) {
	tests := []struct {
		err  error
		want error
	}{
		{&fakePgError{"23505"}, ErrUniqueViolation},
		{&fakePgError{"23503"}, ErrForeignKeyViolation},
		{&fakePgError{"23502"}, ErrNotNullViolation},
		{&fakePgError{"23514"}, ErrCheckViolation},
		{&fakePgError{"40001"}, ErrSerialization},
	}
	for _, tt := range tests {
		err := wrapDBError(dialect.Postgres, "update", "t", tt.err)
		if !errors.Is(err, tt.want) {
			t.Errorf("wrapDBError(%v) = %v, want Is %v", tt.err, err, tt.want)
		}
	}
	err := wrapDBError(dialect.SQLite, "delete", "t", &fakeSQLiteError{787})
	if !errors.Is(err, ErrForeignKeyViolation) {
		t.Errorf("sqlite 787 = %v, want Is ErrForeignKeyViolation", err)
	}
}

func TestUnknownErrorUnwrapped(t *testing.T) {
	if err := wrapDBError(dialect.Postgres, "select", "users", io.EOF); err != io.EOF { //nolint:errorlint // identity is the point
		t.Fatalf("wrapDBError(io.EOF) = %#v, want io.EOF unchanged", err)
	}
	if err := wrapDBError(dialect.Postgres, "select", "users", nil); err != nil {
		t.Fatalf("wrapDBError(nil) = %v, want nil", err)
	}
}

var errMySQL = errors.New("Error 1062: Duplicate entry")

func TestRegisterClassifier(t *testing.T) {
	resetClassifiers()
	t.Cleanup(resetClassifiers)

	if err := wrapDBError(dialect.SQLite, "insert", "users", errMySQL); err != errMySQL { //nolint:errorlint // identity is the point
		t.Fatalf("before registration: %v, want unchanged", err)
	}
	RegisterClassifier(func(error) dialect.ErrorKind { return dialect.KindUnknown })
	RegisterClassifier(func(err error) dialect.ErrorKind {
		if errors.Is(err, errMySQL) {
			return dialect.KindUniqueViolation
		}
		return dialect.KindUnknown
	})
	err := wrapDBError(dialect.SQLite, "insert", "users", errMySQL)
	if !errors.Is(err, ErrUniqueViolation) || !errors.Is(err, errMySQL) {
		t.Fatalf("after registration: %v, want Is ErrUniqueViolation and errMySQL", err)
	}
}

func TestSentinelMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrNotFound, "atla: not found"},
		{ErrMultipleResults, "atla: multiple results"},
		{ErrNotLoaded, "atla: edge not loaded"},
		{ErrUnsafeMutation, "atla: UPDATE/DELETE without WHERE; call AllowFullTable()"},
		{ErrEmptyUpdate, "atla: update has no assignments"},
		{ErrTxUnsupported, "atla: executor cannot begin a transaction"},
		{ErrReturningUnsupported, "atla: RETURNING not supported for this table/dialect"},
		{ErrNoPrimaryKey, "atla: table has no primary key"},
		{ErrIterEdgesSingleConn, "atla: Iter with edges needs a pooled executor; use All inside transactions"},
		{ErrTxOptionsNested, "atla: transaction options are not allowed on a nested transaction"},
		{ErrUniqueViolation, "atla: unique violation"},
		{ErrForeignKeyViolation, "atla: foreign key violation"},
		{ErrNotNullViolation, "atla: not null violation"},
		{ErrCheckViolation, "atla: check violation"},
		{ErrSerialization, "atla: serialization failure or deadlock"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("message = %q, want %q", got, tt.want)
		}
	}
}
