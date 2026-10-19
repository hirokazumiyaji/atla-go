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
	if !checkColsVals(c, "insert", q.t.RawName, q.cols, q.vals) {
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

// checkColsVals fails c and reports false when cols and vals differ in
// length. op and table name the statement in the error.
func checkColsVals(c *compiler, op, table string, cols []string, vals []any) bool {
	if len(cols) != len(vals) {
		c.fail(fmt.Errorf("atla: %s %s: %d columns, %d values", op, table, len(cols), len(vals)))
		return false
	}
	return true
}

// checkFullTable returns [ErrUnsafeMutation] when an UPDATE or DELETE has no
// predicate and full-table changes were not allowed.
func checkFullTable(where []Pred, allowFull bool) error {
	if len(where) == 0 && !allowFull {
		return ErrUnsafeMutation
	}
	return nil
}

// Update is the immutable UPDATE builder that generated XxxUpdateQuery types
// wrap. Every method returns a modified copy.
type Update[R any] struct {
	ex        Executor
	t         Table[R]
	cols      []string
	vals      []any
	where     []Pred
	allowFull bool
}

// NewUpdate returns an update of t, run on ex. cols are quoted column names
// and vals the values assigned to them, in the same order: a value that is
// an atla expression is rendered as SQL, any other value becomes a bind
// parameter. cols and vals are copied. ex may be nil when the statement is
// only rendered with ToSQL. For use by generated code.
func NewUpdate[R any](ex Executor, t Table[R], cols []string, vals []any) Update[R] {
	return Update[R]{ex: ex, t: t, cols: slices.Clone(cols), vals: slices.Clone(vals)}
}

// Where adds predicates. All predicates, across repeated calls, are combined
// with AND.
func (q Update[R]) Where(ps ...Pred) Update[R] {
	q.where = append(slices.Clip(q.where), ps...)
	return q
}

// AllowFullTable permits the statement to run without any predicate, which
// updates every row of the table. Without it such a statement fails with
// [ErrUnsafeMutation].
func (q Update[R]) AllowFullTable() Update[R] {
	q.allowFull = true
	return q
}

// ToSQL returns the SQL text and bind arguments of the statement, without
// RETURNING. It returns [ErrEmptyUpdate] when no column is assigned and
// [ErrUnsafeMutation] when there is no predicate and AllowFullTable was not
// called.
func (q Update[R]) ToSQL() (string, []any, error) {
	return q.compile(false)
}

// Exec runs the statement and returns the number of rows affected. It fails
// like ToSQL, without sending anything.
func (q Update[R]) Exec(ctx context.Context) (int64, error) {
	sqlText, args, err := q.ToSQL()
	if err != nil {
		return 0, err
	}
	return q.runner().exec(ctx, sqlText, args)
}

// Save runs the statement with RETURNING and returns every updated row. It
// fails like ToSQL, and with [ErrReturningUnsupported] when the dialect has
// no RETURNING, without sending anything.
func (q Update[R]) Save(ctx context.Context) ([]*R, error) {
	if err := q.check(); err != nil {
		return nil, err
	}
	if !q.t.Dialect.Returning() {
		return nil, ErrReturningUnsupported
	}
	sqlText, args, err := q.compile(true)
	if err != nil {
		return nil, err
	}
	return queryRows(ctx, q.runner(), q.t.Scan, sqlText, args, -1)
}

// check runs the guards that precede compilation: [ErrEmptyUpdate], then
// [ErrUnsafeMutation].
func (q Update[R]) check() error {
	if len(q.cols) == 0 {
		return ErrEmptyUpdate
	}
	return checkFullTable(q.where, q.allowFull)
}

// compile runs the guards and renders the statement.
func (q Update[R]) compile(returning bool) (string, []any, error) {
	if err := q.check(); err != nil {
		return "", nil, err
	}
	return compile(q.t.Dialect, "", updateNode[R]{q: q, returning: returning})
}

// runner returns the statement plumbing of the update.
func (q Update[R]) runner() runner {
	return runner{ex: q.ex, d: q.t.Dialect, op: "update", table: q.t.RawName}
}

// updateNode renders an Update as a statement.
type updateNode[R any] struct {
	q         Update[R]
	returning bool // append RETURNING with the table's ReturningList
}

func (n updateNode[R]) appendSQL(c *compiler) {
	q := n.q
	if !checkColsVals(c, "update", q.t.RawName, q.cols, q.vals) {
		return
	}
	c.buf = append(c.buf, "UPDATE "...)
	c.buf = append(c.buf, q.t.Ident...)
	c.buf = append(c.buf, " SET "...)
	for i, col := range q.cols {
		if i > 0 {
			c.buf = append(c.buf, ", "...)
		}
		c.buf = append(c.buf, col...)
		c.buf = append(c.buf, " = "...)
		c.value(q.vals[i])
	}
	appendWhere(c, q.where)
	if n.returning {
		appendReturning(c, q.t.ReturningList)
	}
}

// Delete is the immutable DELETE builder that generated XxxDeleteQuery types
// wrap. Every method returns a modified copy.
type Delete[R any] struct {
	ex        Executor
	t         Table[R]
	where     []Pred
	allowFull bool
}

// NewDelete returns a delete from t, run on ex. ex may be nil when the
// statement is only rendered with ToSQL. For use by generated code.
func NewDelete[R any](ex Executor, t Table[R]) Delete[R] {
	return Delete[R]{ex: ex, t: t}
}

// Where adds predicates. All predicates, across repeated calls, are combined
// with AND.
func (q Delete[R]) Where(ps ...Pred) Delete[R] {
	q.where = append(slices.Clip(q.where), ps...)
	return q
}

// AllowFullTable permits the statement to run without any predicate, which
// deletes every row of the table. Without it such a statement fails with
// [ErrUnsafeMutation].
func (q Delete[R]) AllowFullTable() Delete[R] {
	q.allowFull = true
	return q
}

// ToSQL returns the SQL text and bind arguments of the statement. It returns
// [ErrUnsafeMutation] when there is no predicate and AllowFullTable was not
// called.
func (q Delete[R]) ToSQL() (string, []any, error) {
	if err := checkFullTable(q.where, q.allowFull); err != nil {
		return "", nil, err
	}
	return compile(q.t.Dialect, "", deleteNode[R]{q: q})
}

// Exec runs the statement and returns the number of rows affected. It fails
// like ToSQL, without sending anything.
func (q Delete[R]) Exec(ctx context.Context) (int64, error) {
	sqlText, args, err := q.ToSQL()
	if err != nil {
		return 0, err
	}
	return q.runner().exec(ctx, sqlText, args)
}

// runner returns the statement plumbing of the delete.
func (q Delete[R]) runner() runner {
	return runner{ex: q.ex, d: q.t.Dialect, op: "delete", table: q.t.RawName}
}

// deleteNode renders a Delete as a statement.
type deleteNode[R any] struct{ q Delete[R] }

func (n deleteNode[R]) appendSQL(c *compiler) {
	c.buf = append(c.buf, "DELETE FROM "...)
	c.buf = append(c.buf, n.q.t.Ident...)
	appendWhere(c, n.q.where)
}
