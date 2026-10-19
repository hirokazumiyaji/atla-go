package atla

import (
	"context"
	"database/sql"
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
