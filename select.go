package atla

import (
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
	return compile(q.t.Dialect, "", selectNode[R]{q})
}

// Subquery returns the query for use as a subquery, for example in [Exists].
func (q Select[R]) Subquery() AnySubquery {
	return AnySubquery{sub: subquery{sel: selectNode[R]{q}}}
}

// selectNode renders a Select as a statement. Rendered inside another
// statement's compiler, its placeholders continue the outer numbering.
type selectNode[R any] struct{ q Select[R] }

func (n selectNode[R]) appendSQL(c *compiler) {
	q := n.q
	c.buf = append(c.buf, "SELECT "...)
	c.buf = append(c.buf, q.t.SelectList...)
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
