// Package dialect abstracts the SQL differences between supported databases.
//
// It deliberately does not import the root atla package: [Dialect.Classify]
// returns an [ErrorKind] that the root package maps to its sentinel errors.
package dialect

// ErrorKind is a driver-independent classification of a database error.
type ErrorKind uint8

// Error kinds returned by [Dialect.Classify].
const (
	KindUnknown ErrorKind = iota
	KindUniqueViolation
	KindForeignKeyViolation
	KindNotNullViolation
	KindCheckViolation
	KindSerialization
)

// LockStrength is the strength of a row lock clause.
type LockStrength uint8

// Row lock strengths.
const (
	LockUpdate LockStrength = iota
	LockShare
)

// LockWait is the wait policy of a row lock clause.
type LockWait uint8

// Row lock wait policies.
const (
	WaitDefault LockWait = iota
	WaitNoWait
	WaitSkipLocked
)

// Lock describes a row lock clause such as FOR UPDATE.
type Lock struct {
	Strength LockStrength
	Wait     LockWait
	// Of lists already-quoted table references for the OF clause.
	Of []string
}

// Dialect captures the SQL syntax and error differences of one database.
type Dialect interface {
	// Name returns the dialect name, such as "postgres" or "sqlite".
	Name() string
	// AppendIdent appends ident as a quoted identifier.
	AppendIdent(b []byte, ident string) []byte
	// AppendPlaceholder appends the bind placeholder for the n-th (1-based) argument.
	AppendPlaceholder(b []byte, n int) []byte
	// Returning reports whether INSERT/UPDATE/DELETE support RETURNING.
	Returning() bool
	// NativeILike reports whether the database has an ILIKE operator.
	NativeILike() bool
	// AppendLock appends a row lock clause with a leading space, or nothing
	// when the database has no row locks.
	AppendLock(b []byte, l Lock) []byte
	// AppendLimitOffset appends LIMIT/OFFSET with a leading space. A negative
	// value means unset; when both are unset nothing is appended.
	AppendLimitOffset(b []byte, limit, offset int) []byte
	// MaxParams returns the maximum number of bind parameters per statement.
	MaxParams() int
	// Classify maps a driver error to an [ErrorKind].
	Classify(err error) ErrorKind
}

// appendQuotedIdent appends ident wrapped in double quotes, doubling any
// embedded double quote.
func appendQuotedIdent(b []byte, ident string) []byte {
	b = append(b, '"')
	for i := 0; i < len(ident); i++ {
		c := ident[i]
		if c == '"' {
			b = append(b, '"')
		}
		b = append(b, c)
	}
	return append(b, '"')
}
