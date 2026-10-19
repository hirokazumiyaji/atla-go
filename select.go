package atla

import (
	"context"
	"database/sql"
	"iter"
	"slices"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

// Select is the immutable SELECT builder that generated XxxQuery types wrap.
// Every method returns a modified copy, so a stored query can be branched
// freely.
type Select[R any] struct {
	ex     Executor
	t      Table[R]
	where  []Pred
	order  []Order
	limit  int // -1: unset
	offset int // -1: unset
	lock   *dialect.Lock
}

// NewSelect returns a query selecting every column of t, run on ex. ex may be
// nil when the query is only rendered with ToSQL. For use by generated code.
func NewSelect[R any](ex Executor, t Table[R]) Select[R] {
	return Select[R]{ex: ex, t: t, limit: -1, offset: -1}
}

// Where adds predicates. All predicates, across repeated calls, are combined
// with AND.
func (q Select[R]) Where(ps ...Pred) Select[R] {
	q.where = append(slices.Clip(q.where), ps...)
	return q
}

// OrderBy adds ORDER BY items after any added earlier.
func (q Select[R]) OrderBy(os ...Order) Select[R] {
	q.order = append(slices.Clip(q.order), os...)
	return q
}

// Limit sets the maximum number of rows. It is rendered as a literal.
func (q Select[R]) Limit(n int) Select[R] {
	q.limit = n
	return q
}

// Offset sets the number of rows to skip. It is rendered as a literal.
func (q Select[R]) Offset(n int) Select[R] {
	q.offset = n
	return q
}

// ForUpdate locks the selected rows with FOR UPDATE, replacing any earlier
// lock. Dialects without row locks, such as SQLite, render nothing.
func (q Select[R]) ForUpdate(opts ...LockOption) Select[R] {
	return q.withLock(dialect.LockUpdate, opts)
}

// ForShare locks the selected rows with FOR SHARE, replacing any earlier
// lock. Dialects without row locks, such as SQLite, render nothing.
func (q Select[R]) ForShare(opts ...LockOption) Select[R] {
	return q.withLock(dialect.LockShare, opts)
}

func (q Select[R]) withLock(s dialect.LockStrength, opts []LockOption) Select[R] {
	l := &dialect.Lock{Strength: s}
	for _, o := range opts {
		o(l)
	}
	q.lock = l
	return q
}

// LockOption configures a FOR UPDATE / FOR SHARE clause.
type LockOption func(*dialect.Lock)

// SkipLocked skips rows that are locked by other transactions (SKIP LOCKED).
func SkipLocked() LockOption {
	return func(l *dialect.Lock) { l.Wait = dialect.WaitSkipLocked }
}

// NoWait fails instead of waiting for rows locked by other transactions
// (NOWAIT).
func NoWait() LockOption {
	return func(l *dialect.Lock) { l.Wait = dialect.WaitNoWait }
}

// Of limits the lock to the given tables (OF …). Each table is an
// already-quoted table name or alias, such as `"users"`.
func Of(tables ...string) LockOption {
	tables = slices.Clone(tables)
	return func(l *dialect.Lock) { l.Of = append(slices.Clip(l.Of), tables...) }
}

// ToSQL returns the SQL text and bind arguments of the query.
func (q Select[R]) ToSQL() (string, []any, error) {
	return compile(q.t.Dialect, "", selectNode[R]{q: q})
}

// Subquery returns the query for use as a subquery, for example in [Exists].
func (q Select[R]) Subquery() AnySubquery {
	return AnySubquery{sub: subquery{sel: selectNode[R]{q: q}}}
}

// selectNode renders a Select as a statement. Rendered inside another
// statement's compiler, its placeholders continue the outer numbering.
type selectNode[R any] struct {
	q    Select[R]
	list string // replaces q.t.SelectList when not empty, e.g. "COUNT(*)"
}

func (n selectNode[R]) appendSQL(c *compiler) {
	q := n.q
	list := n.list
	if list == "" {
		list = q.t.SelectList
	}
	c.buf = append(c.buf, "SELECT "...)
	c.buf = append(c.buf, list...)
	c.buf = append(c.buf, " FROM "...)
	c.buf = q.t.appendFrom(c.buf)
	if len(q.where) > 0 {
		c.buf = append(c.buf, " WHERE "...)
		And(q.where...).appendSQL(c)
	}
	for i, o := range q.order {
		if i == 0 {
			c.buf = append(c.buf, " ORDER BY "...)
		} else {
			c.buf = append(c.buf, ", "...)
		}
		o.appendSQL(c)
	}
	c.buf = c.d.AppendLimitOffset(c.buf, q.limit, q.offset)
	if q.lock != nil {
		c.buf = c.d.AppendLock(c.buf, *q.lock)
	}
}

// All runs the query and returns every row.
func (q Select[R]) All(ctx context.Context) ([]*R, error) {
	return q.fetch(ctx, -1)
}

// First runs the query with LIMIT 1, replacing any limit, and returns the
// row. It returns [ErrNotFound] when no row matches.
func (q Select[R]) First(ctx context.Context) (*R, error) {
	q.limit = 1
	rs, err := q.fetch(ctx, 1)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNotFound
	}
	return rs[0], nil
}

// One runs the query with LIMIT 2, replacing any limit, and returns the only
// row. It returns [ErrNotFound] when no row matches and [ErrMultipleResults]
// when more than one does.
func (q Select[R]) One(ctx context.Context) (*R, error) {
	q.limit = 2
	rs, err := q.fetch(ctx, 2)
	if err != nil {
		return nil, err
	}
	switch len(rs) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return rs[0], nil
	default:
		return nil, ErrMultipleResults
	}
}

// Count returns the number of rows the query matches. ORDER BY is dropped.
// With a limit, an offset or a lock, the query is counted as a subquery:
// `SELECT COUNT(*) FROM (SELECT 1 FROM … LIMIT n) AS "atla_count"`.
func (q Select[R]) Count(ctx context.Context) (int64, error) {
	q.order = nil
	var (
		sqlText string
		args    []any
		err     error
	)
	if q.limit < 0 && q.offset < 0 && q.lock == nil {
		sqlText, args, err = compile(q.t.Dialect, "", selectNode[R]{q: q, list: "COUNT(*)"})
	} else {
		sub := subquery{sel: selectNode[R]{q: q, list: "1"}}
		sqlText, args, err = compile(q.t.Dialect, "SELECT COUNT(*) FROM ", countSubquery{sub})
	}
	if err != nil {
		return 0, err
	}
	return queryValue[int64](ctx, q, sqlText, args)
}

// countSubquery renders `(SELECT …) AS "atla_count"`.
type countSubquery struct{ sub subquery }

func (s countSubquery) appendSQL(c *compiler) {
	s.sub.appendSQL(c)
	c.buf = append(c.buf, " AS "...)
	c.buf = c.d.AppendIdent(c.buf, "atla_count")
}

// Exist reports whether the query matches any row:
// `SELECT EXISTS (SELECT 1 FROM … WHERE …)`. ORDER BY is dropped.
func (q Select[R]) Exist(ctx context.Context) (bool, error) {
	q.order = nil
	sub := subquery{sel: selectNode[R]{q: q, list: "1"}}
	sqlText, args, err := compile(q.t.Dialect, "SELECT ", exists{sub})
	if err != nil {
		return false, err
	}
	return queryValue[bool](ctx, q, sqlText, args)
}

// Iter runs the query and streams its rows. On an error it yields
// (nil, err) once and stops. The rows are closed when the loop ends,
// including when it exits early.
func (q Select[R]) Iter(ctx context.Context) iter.Seq2[*R, error] {
	return func(yield func(*R, error) bool) {
		sqlText, args, err := q.ToSQL()
		if err != nil {
			yield(nil, err)
			return
		}
		rows, err := q.query(ctx, sqlText, args)
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = rows.Close() }() // a no-op after the Close below
		for rows.Next() {
			r, err := q.t.Scan(rows)
			if err != nil {
				yield(nil, q.wrapErr(err))
				return
			}
			if !yield(r, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(nil, q.wrapErr(err))
			return
		}
		if err := rows.Close(); err != nil {
			yield(nil, q.wrapErr(err))
		}
	}
}

// fetch runs the query and scans at most n rows, or every row when n < 0.
func (q Select[R]) fetch(ctx context.Context, n int) (out []*R, err error) {
	sqlText, args, err := q.ToSQL()
	if err != nil {
		return nil, err
	}
	rows, err := q.query(ctx, sqlText, args)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			out, err = nil, q.wrapErr(cerr)
		}
	}()
	for (n < 0 || len(out) < n) && rows.Next() {
		r, serr := q.t.Scan(rows)
		if serr != nil {
			return nil, q.wrapErr(serr)
		}
		out = append(out, r)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, q.wrapErr(rerr)
	}
	return out, nil
}

// queryValue runs a query that returns a single value, such as a COUNT, on
// q's executor and scans the value into a T.
func queryValue[T, R any](ctx context.Context, q Select[R], sqlText string, args []any) (v T, err error) {
	var zero T
	rows, err := q.query(ctx, sqlText, args)
	if err != nil {
		return zero, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			v, err = zero, q.wrapErr(cerr)
		}
	}()
	if !rows.Next() {
		if rerr := rows.Err(); rerr != nil {
			return zero, q.wrapErr(rerr)
		}
		return zero, ErrNotFound
	}
	if serr := rows.Scan(&v); serr != nil {
		return zero, q.wrapErr(serr)
	}
	if rerr := rows.Err(); rerr != nil {
		return zero, q.wrapErr(rerr)
	}
	return v, nil
}

// query sends sqlText to the executor with the query's QueryInfo attached.
func (q Select[R]) query(ctx context.Context, sqlText string, args []any) (*sql.Rows, error) {
	ctx = withQueryInfo(ctx, QueryInfo{Op: "select", Table: q.t.RawName, ReadOnly: q.lock == nil})
	rows, err := q.ex.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, q.wrapErr(err)
	}
	return rows, nil
}

// wrapErr classifies a database error of the query.
func (q Select[R]) wrapErr(err error) error {
	return wrapDBError(q.t.Dialect, "select", q.t.RawName, err)
}
