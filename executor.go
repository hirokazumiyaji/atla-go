package atla

import (
	"context"
	"database/sql"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

// Executor runs queries. It is satisfied by *sql.DB, *sql.Tx and *sql.Conn,
// and by wrappers around them that read [QueryInfo] from the context.
type Executor interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// TxBeginner starts transactions. It is satisfied by *sql.DB and *sql.Conn.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// QueryInfo describes the statement being executed. The runtime attaches it to
// the context of every Executor call; wrappers read it with [QueryInfoFrom].
type QueryInfo struct {
	Op       string // "select", "insert", "update", "delete", "exec"
	Table    string // unquoted primary table
	ReadOnly bool   // plain SELECT without locks
}

type queryInfoKey struct{}

// QueryInfoFrom returns the QueryInfo attached to ctx by the runtime.
func QueryInfoFrom(ctx context.Context) (QueryInfo, bool) {
	qi, ok := ctx.Value(queryInfoKey{}).(QueryInfo)
	return qi, ok
}

// withQueryInfo returns a copy of ctx carrying qi.
func withQueryInfo(ctx context.Context, qi QueryInfo) context.Context {
	return context.WithValue(ctx, queryInfoKey{}, qi)
}

// runner sends the statements of one operation on one table: it attaches
// the operation's QueryInfo to every call and classifies database errors.
// Builders share it so that every statement goes through the same plumbing.
type runner struct {
	ex       Executor
	d        dialect.Dialect
	op       string // QueryInfo.Op and DBError.Op
	table    string // unquoted table name
	readOnly bool
}

// wrapErr classifies a database error of the operation.
func (r runner) wrapErr(err error) error {
	return wrapDBError(r.d, r.op, r.table, err)
}

func (r runner) info(ctx context.Context) context.Context {
	return withQueryInfo(ctx, QueryInfo{Op: r.op, Table: r.table, ReadOnly: r.readOnly})
}

// query sends sqlText with QueryContext.
func (r runner) query(ctx context.Context, sqlText string, args []any) (*sql.Rows, error) {
	rows, err := r.ex.QueryContext(r.info(ctx), sqlText, args...)
	if err != nil {
		return nil, r.wrapErr(err)
	}
	return rows, nil
}

// exec sends sqlText with ExecContext and returns the number of rows
// affected.
func (r runner) exec(ctx context.Context, sqlText string, args []any) (int64, error) {
	res, err := r.ex.ExecContext(r.info(ctx), sqlText, args...)
	if err != nil {
		return 0, r.wrapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, r.wrapErr(err)
	}
	return n, nil
}

// queryRows sends sqlText with QueryContext and scans at most n rows with
// scan, or every row when n < 0. The rows are always closed.
func queryRows[R any](ctx context.Context, r runner, scan func(*sql.Rows) (*R, error), sqlText string, args []any, n int) (out []*R, err error) {
	rows, err := r.query(ctx, sqlText, args)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			out, err = nil, r.wrapErr(cerr)
		}
	}()
	for (n < 0 || len(out) < n) && rows.Next() {
		v, serr := scan(rows)
		if serr != nil {
			return nil, r.wrapErr(serr)
		}
		out = append(out, v)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, r.wrapErr(rerr)
	}
	return out, nil
}

// queryValue sends sqlText with QueryContext and scans the single value of
// its first row, such as a COUNT, into a T. It returns [ErrNotFound] when
// there is no row.
func queryValue[T any](ctx context.Context, r runner, sqlText string, args []any) (v T, err error) {
	var zero T
	rows, err := r.query(ctx, sqlText, args)
	if err != nil {
		return zero, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			v, err = zero, r.wrapErr(cerr)
		}
	}()
	if !rows.Next() {
		if rerr := rows.Err(); rerr != nil {
			return zero, r.wrapErr(rerr)
		}
		return zero, ErrNotFound
	}
	if serr := rows.Scan(&v); serr != nil {
		return zero, r.wrapErr(serr)
	}
	if rerr := rows.Err(); rerr != nil {
		return zero, r.wrapErr(rerr)
	}
	return v, nil
}
