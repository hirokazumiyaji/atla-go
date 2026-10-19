package dialect

import (
	"errors"
	"strconv"
)

// SQLite is the SQLite dialect.
var SQLite Dialect = sqlite{}

type sqlite struct{}

func (sqlite) Name() string { return "sqlite" }

func (sqlite) AppendIdent(b []byte, ident string) []byte { return appendQuotedIdent(b, ident) }

func (sqlite) AppendPlaceholder(b []byte, _ int) []byte { return append(b, '?') }

func (sqlite) Returning() bool   { return true }
func (sqlite) NativeILike() bool { return false }
func (sqlite) MaxParams() int    { return 32766 }

// AppendLock appends nothing: SQLite has no row locks.
func (sqlite) AppendLock(b []byte, _ Lock) []byte { return b }

func (sqlite) AppendLimitOffset(b []byte, limit, offset int) []byte {
	if limit < 0 && offset >= 0 {
		// SQLite requires LIMIT whenever OFFSET is present.
		b = append(b, " LIMIT -1"...)
	} else if limit >= 0 {
		b = append(b, " LIMIT "...)
		b = strconv.AppendInt(b, int64(limit), 10)
	}
	if offset >= 0 {
		b = append(b, " OFFSET "...)
		b = strconv.AppendInt(b, int64(offset), 10)
	}
	return b
}

func (sqlite) Classify(err error) ErrorKind {
	var ce interface{ Code() int }
	if !errors.As(err, &ce) {
		return KindUnknown
	}
	code := ce.Code()
	switch code {
	case 2067, 1555: // SQLITE_CONSTRAINT_UNIQUE, SQLITE_CONSTRAINT_PRIMARYKEY
		return KindUniqueViolation
	case 787: // SQLITE_CONSTRAINT_FOREIGNKEY
		return KindForeignKeyViolation
	case 1299: // SQLITE_CONSTRAINT_NOTNULL
		return KindNotNullViolation
	case 275: // SQLITE_CONSTRAINT_CHECK
		return KindCheckViolation
	}
	// SQLITE_BUSY (5) and SQLITE_LOCKED (6), including extended codes.
	if p := code & 0xff; p == 5 || p == 6 {
		return KindSerialization
	}
	return KindUnknown
}
