package dialect_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

func assertEq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

type pgErr struct{ state string }

func (e pgErr) Error() string    { return "pg error " + e.state }
func (e pgErr) SQLState() string { return e.state }

type sqliteErr struct{ code int }

func (e sqliteErr) Error() string { return fmt.Sprintf("sqlite error %d", e.code) }
func (e sqliteErr) Code() int     { return e.code }

func TestAppendIdentEscapes(t *testing.T) {
	assertEq(t, string(dialect.Postgres.AppendIdent(nil, `users`)), `"users"`)
	assertEq(t, string(dialect.Postgres.AppendIdent(nil, `we"ird`)), `"we""ird"`)
	assertEq(t, string(dialect.SQLite.AppendIdent(nil, `users`)), `"users"`)
	assertEq(t, string(dialect.SQLite.AppendIdent(nil, `we"ird`)), `"we""ird"`)
	assertEq(t, string(dialect.Postgres.AppendIdent([]byte("x "), `a`)), `x "a"`)
}

func TestPlaceholders(t *testing.T) {
	assertEq(t, string(dialect.Postgres.AppendPlaceholder(nil, 12)), `$12`)
	assertEq(t, string(dialect.Postgres.AppendPlaceholder(nil, 1)), `$1`)
	assertEq(t, string(dialect.SQLite.AppendPlaceholder(nil, 12)), `?`)
}

func TestCapabilities(t *testing.T) {
	assertEq(t, dialect.Postgres.Name(), "postgres")
	assertEq(t, dialect.Postgres.Returning(), true)
	assertEq(t, dialect.Postgres.NativeILike(), true)
	assertEq(t, dialect.Postgres.MaxParams(), 65535)

	assertEq(t, dialect.SQLite.Name(), "sqlite")
	assertEq(t, dialect.SQLite.Returning(), true)
	assertEq(t, dialect.SQLite.NativeILike(), false)
	assertEq(t, dialect.SQLite.MaxParams(), 32766)
}

func TestAppendLock(t *testing.T) {
	tests := []struct {
		name string
		lock dialect.Lock
		want string
	}{
		{"update", dialect.Lock{Strength: dialect.LockUpdate, Wait: dialect.WaitDefault}, " FOR UPDATE"},
		{"share nowait", dialect.Lock{Strength: dialect.LockShare, Wait: dialect.WaitNoWait}, " FOR SHARE NOWAIT"},
		{"update of skip locked", dialect.Lock{Strength: dialect.LockUpdate, Wait: dialect.WaitSkipLocked, Of: []string{`"users"`}}, ` FOR UPDATE OF "users" SKIP LOCKED`},
		{"share of multiple", dialect.Lock{Strength: dialect.LockShare, Of: []string{`"a"`, `"b"`}}, ` FOR SHARE OF "a", "b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEq(t, string(dialect.Postgres.AppendLock(nil, tt.lock)), tt.want)
			assertEq(t, string(dialect.Postgres.AppendLock([]byte("SELECT 1"), tt.lock)), "SELECT 1"+tt.want)
			assertEq(t, string(dialect.SQLite.AppendLock(nil, tt.lock)), "")
			assertEq(t, string(dialect.SQLite.AppendLock([]byte("SELECT 1"), tt.lock)), "SELECT 1")
		})
	}
}

func TestAppendLimitOffset(t *testing.T) {
	tests := []struct {
		limit, offset int
		pg, sqlite    string
	}{
		{10, -1, " LIMIT 10", " LIMIT 10"},
		{10, 5, " LIMIT 10 OFFSET 5", " LIMIT 10 OFFSET 5"},
		{-1, 5, " OFFSET 5", " LIMIT -1 OFFSET 5"},
		{-1, -1, "", ""},
		{0, 0, " LIMIT 0 OFFSET 0", " LIMIT 0 OFFSET 0"},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("limit=%d,offset=%d", tt.limit, tt.offset)
		t.Run(name, func(t *testing.T) {
			assertEq(t, string(dialect.Postgres.AppendLimitOffset(nil, tt.limit, tt.offset)), tt.pg)
			assertEq(t, string(dialect.SQLite.AppendLimitOffset(nil, tt.limit, tt.offset)), tt.sqlite)
		})
	}
}

func TestClassifyPostgres(t *testing.T) {
	tests := []struct {
		state string
		want  dialect.ErrorKind
	}{
		{"23505", dialect.KindUniqueViolation},
		{"23503", dialect.KindForeignKeyViolation},
		{"23502", dialect.KindNotNullViolation},
		{"23514", dialect.KindCheckViolation},
		{"40001", dialect.KindSerialization},
		{"40P01", dialect.KindSerialization},
		{"42601", dialect.KindUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			err := fmt.Errorf("x: %w", pgErr{tt.state})
			assertEq(t, dialect.Postgres.Classify(err), tt.want)
		})
	}
	assertEq(t, dialect.Postgres.Classify(errors.New("plain")), dialect.KindUnknown)
	assertEq(t, dialect.Postgres.Classify(nil), dialect.KindUnknown)
}

func TestClassifySQLite(t *testing.T) {
	tests := []struct {
		code int
		want dialect.ErrorKind
	}{
		{2067, dialect.KindUniqueViolation},
		{1555, dialect.KindUniqueViolation},
		{787, dialect.KindForeignKeyViolation},
		{1299, dialect.KindNotNullViolation},
		{275, dialect.KindCheckViolation},
		{5, dialect.KindSerialization},
		{6, dialect.KindSerialization},
		{261, dialect.KindSerialization},
		{517, dialect.KindSerialization},
		{1, dialect.KindUnknown},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.code), func(t *testing.T) {
			err := fmt.Errorf("x: %w", sqliteErr{tt.code})
			assertEq(t, dialect.SQLite.Classify(err), tt.want)
		})
	}
	assertEq(t, dialect.SQLite.Classify(errors.New("plain")), dialect.KindUnknown)
	assertEq(t, dialect.SQLite.Classify(nil), dialect.KindUnknown)
}
