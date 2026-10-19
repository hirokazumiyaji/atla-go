package atla

import (
	"context"
	"fmt"
	"slices"
)

// Insert is the INSERT builder that generated XxxInsertQuery types wrap. It
// inserts one row.
type Insert[R any] struct {
	ex   Executor
	t    Table[R]
	cols []string
	vals []any
}

// NewInsert returns an insert into t of one row, run on ex. cols are quoted
// column names and vals their values, in the same order: a value that is an
// atla expression is rendered as SQL, any other value becomes a bind
// parameter. With no columns the row is inserted with DEFAULT VALUES. cols
// and vals are copied. ex may be nil when the statement is only rendered
// with ToSQL. For use by generated code.
func NewInsert[R any](ex Executor, t Table[R], cols []string, vals []any) Insert[R] {
	return Insert[R]{ex: ex, t: t, cols: slices.Clone(cols), vals: slices.Clone(vals)}
}

// ToSQL returns the SQL text and bind arguments of the statement, without
// RETURNING.
func (q Insert[R]) ToSQL() (string, []any, error) {
	return compile(q.t.Dialect, "", insertNode[R]{q: q})
}

// Exec runs the statement and returns the number of rows affected.
func (q Insert[R]) Exec(ctx context.Context) (int64, error) {
	sqlText, args, err := q.ToSQL()
	if err != nil {
		return 0, err
	}
	return q.runner().exec(ctx, sqlText, args)
}

// Save runs the statement with RETURNING and returns the inserted row. It
// returns [ErrReturningUnsupported], without sending anything, when the
// dialect has no RETURNING, and [ErrNotFound] when no row is returned.
func (q Insert[R]) Save(ctx context.Context) (*R, error) {
	if !q.t.Dialect.Returning() {
		return nil, ErrReturningUnsupported
	}
	sqlText, args, err := compile(q.t.Dialect, "", insertNode[R]{q: q, returning: true})
	if err != nil {
		return nil, err
	}
	rs, err := queryRows(ctx, q.runner(), q.t.Scan, sqlText, args, 1)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNotFound
	}
	return rs[0], nil
}

// runner returns the statement plumbing of the insert.
func (q Insert[R]) runner() runner {
	return runner{ex: q.ex, d: q.t.Dialect, op: "insert", table: q.t.RawName}
}

// insertNode renders an Insert as a statement.
type insertNode[R any] struct {
	q         Insert[R]
	returning bool // append RETURNING with the table's ReturningList
}

func (n insertNode[R]) appendSQL(c *compiler) {
	q := n.q
	if len(q.cols) != len(q.vals) {
		c.fail(fmt.Errorf("atla: insert %s: %d columns, %d values", q.t.RawName, len(q.cols), len(q.vals)))
		return
	}
	c.buf = append(c.buf, "INSERT INTO "...)
	c.buf = append(c.buf, q.t.Ident...)
	if len(q.cols) == 0 {
		c.buf = append(c.buf, " DEFAULT VALUES"...)
	} else {
		c.buf = append(c.buf, " ("...)
		for i, col := range q.cols {
			if i > 0 {
				c.buf = append(c.buf, ", "...)
			}
			c.buf = append(c.buf, col...)
		}
		c.buf = append(c.buf, ") VALUES ("...)
		for i, v := range q.vals {
			if i > 0 {
				c.buf = append(c.buf, ", "...)
			}
			c.value(v)
		}
		c.buf = append(c.buf, ')')
	}
	if n.returning {
		appendReturning(c, q.t.ReturningList)
	}
}

// appendReturning appends ` RETURNING <list>`.
func appendReturning(c *compiler, list string) {
	c.buf = append(c.buf, " RETURNING "...)
	c.buf = append(c.buf, list...)
}
