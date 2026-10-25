package dialect

import (
	"errors"
	"strconv"
)

// Postgres is the PostgreSQL dialect.
var Postgres Dialect = postgres{}

type postgres struct{}

func (postgres) Name() string { return "postgres" }

func (postgres) AppendIdent(b []byte, ident string) []byte { return appendQuotedIdent(b, ident) }

func (postgres) AppendPlaceholder(b []byte, n int) []byte {
	b = append(b, '$')
	return strconv.AppendInt(b, int64(n), 10)
}

func (postgres) Returning() bool   { return true }
func (postgres) NativeILike() bool { return true }
func (postgres) MaxParams() int    { return 65535 }

func (postgres) AppendLock(b []byte, l Lock) []byte {
	if l.Strength == LockShare {
		b = append(b, " FOR SHARE"...)
	} else {
		b = append(b, " FOR UPDATE"...)
	}
	for i, of := range l.Of {
		if i == 0 {
			b = append(b, " OF "...)
		} else {
			b = append(b, ", "...)
		}
		b = append(b, of...)
	}
	switch l.Wait {
	case WaitNoWait:
		b = append(b, " NOWAIT"...)
	case WaitSkipLocked:
		b = append(b, " SKIP LOCKED"...)
	case WaitDefault:
	}
	return b
}

func (postgres) AppendLimitOffset(b []byte, limit, offset int) []byte {
	if limit >= 0 {
		b = append(b, " LIMIT "...)
		b = strconv.AppendInt(b, int64(limit), 10)
	}
	if offset >= 0 {
		b = append(b, " OFFSET "...)
		b = strconv.AppendInt(b, int64(offset), 10)
	}
	return b
}

func (postgres) Classify(err error) ErrorKind {
	var se interface{ SQLState() string }
	if !errors.As(err, &se) {
		return KindUnknown
	}
	switch se.SQLState() {
	case "23505":
		return KindUniqueViolation
	case "23503":
		return KindForeignKeyViolation
	case "23502":
		return KindNotNullViolation
	case "23514":
		return KindCheckViolation
	case "40001", "40P01":
		return KindSerialization
	}
	return KindUnknown
}
