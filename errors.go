package atla

import (
	"errors"
	"sync"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

// Sentinel errors returned by the runtime.
var (
	ErrNotFound             = errors.New("atla: not found")
	ErrMultipleResults      = errors.New("atla: multiple results")
	ErrNotLoaded            = errors.New("atla: edge not loaded")
	ErrUnsafeMutation       = errors.New("atla: UPDATE/DELETE without WHERE; call AllowFullTable()")
	ErrEmptyUpdate          = errors.New("atla: update has no assignments")
	ErrTxUnsupported        = errors.New("atla: executor cannot begin a transaction")
	ErrReturningUnsupported = errors.New("atla: RETURNING not supported for this table/dialect")
	ErrNoPrimaryKey         = errors.New("atla: table has no primary key")
	ErrIterEdgesSingleConn  = errors.New("atla: Iter with edges needs a pooled executor; use All inside transactions")
	ErrTxOptionsNested      = errors.New("atla: transaction options are not allowed on a nested transaction")
)

// Database error kinds, classified from driver errors and carried as
// [DBError.Kind].
var (
	ErrUniqueViolation     = errors.New("atla: unique violation")
	ErrForeignKeyViolation = errors.New("atla: foreign key violation")
	ErrNotNullViolation    = errors.New("atla: not null violation")
	ErrCheckViolation      = errors.New("atla: check violation")
	ErrSerialization       = errors.New("atla: serialization failure or deadlock")
)

// DBError wraps a classified driver error. Both errors.Is(err, Kind) and
// errors.As(err, &driverErr) work through it.
type DBError struct {
	Kind  error  // one of the database error kinds, e.g. ErrUniqueViolation
	Op    string // "select", "insert", "update", "delete", "exec"
	Table string // unquoted table name when known
	Err   error  // the original driver error
}

// Error returns "atla: <op> <table>: <driver error>".
func (e *DBError) Error() string {
	return "atla: " + e.Op + " " + e.Table + ": " + e.Err.Error()
}

// Unwrap returns the kind sentinel and the original driver error.
func (e *DBError) Unwrap() []error { return []error{e.Kind, e.Err} }

var (
	classifiersMu sync.RWMutex
	classifiers   []func(error) dialect.ErrorKind
)

// RegisterClassifier adds a classifier for driver errors that the dialect
// cannot recognise structurally (for example go-sql-driver/mysql or
// mattn/go-sqlite3). Call it once at startup. Classifiers are consulted in
// registration order after the dialect; the first non-unknown kind wins.
func RegisterClassifier(f func(error) dialect.ErrorKind) {
	classifiersMu.Lock()
	defer classifiersMu.Unlock()
	classifiers = append(classifiers, f)
}

// classify returns the kind of err according to d and the registered
// classifiers.
func classify(d dialect.Dialect, err error) dialect.ErrorKind {
	if d != nil {
		if k := d.Classify(err); k != dialect.KindUnknown {
			return k
		}
	}
	classifiersMu.RLock()
	defer classifiersMu.RUnlock()
	for _, f := range classifiers {
		if k := f(err); k != dialect.KindUnknown {
			return k
		}
	}
	return dialect.KindUnknown
}

// kindSentinel maps an ErrorKind to its sentinel, or nil for KindUnknown.
func kindSentinel(k dialect.ErrorKind) error {
	switch k {
	case dialect.KindUniqueViolation:
		return ErrUniqueViolation
	case dialect.KindForeignKeyViolation:
		return ErrForeignKeyViolation
	case dialect.KindNotNullViolation:
		return ErrNotNullViolation
	case dialect.KindCheckViolation:
		return ErrCheckViolation
	case dialect.KindSerialization:
		return ErrSerialization
	default:
		return nil
	}
}

// wrapDBError wraps a classified driver error in a *DBError. A nil error
// stays nil, and an unclassified error or one that already contains a
// *DBError is returned unchanged.
func wrapDBError(d dialect.Dialect, op, table string, err error) error {
	if err == nil {
		return nil
	}
	var dbErr *DBError
	if errors.As(err, &dbErr) {
		return err
	}
	kind := kindSentinel(classify(d, err))
	if kind == nil {
		return err
	}
	return &DBError{Kind: kind, Op: op, Table: table, Err: err}
}
