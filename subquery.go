package atla

import "errors"

// errEmptySubquery is reported when a zero AnySubquery or Subquery is
// compiled.
var errEmptySubquery = errors.New("atla: empty subquery")

// subquery renders `(SELECT …)` into the enclosing statement's compiler, so
// placeholder numbering continues across the nesting.
type subquery struct{ sel node }

func (s subquery) appendSQL(c *compiler) {
	if s.sel == nil {
		c.fail(errEmptySubquery)
		return
	}
	c.buf = append(c.buf, '(')
	s.sel.appendSQL(c)
	c.buf = append(c.buf, ')')
}

// AnySubquery is a query of any shape used as a subquery, as returned by
// [Select.Subquery]. Use it with [Exists].
type AnySubquery struct{ sub subquery }

// Subquery is a single-column query whose column has the Go type T. Use it
// with InQuery, as in user.ID.InQuery(sub).
type Subquery[T any] struct{ sub subquery }

// Exists reports whether sub returns any row: `EXISTS (SELECT …)`.
func Exists(sub AnySubquery) Pred { return exists(sub) }

// exists renders `EXISTS (SELECT …)`.
type exists struct{ sub subquery }

func (e exists) appendSQL(c *compiler) {
	c.buf = append(c.buf, "EXISTS "...)
	e.sub.appendSQL(c)
}

func (exists) exprOf(bool) {}
func (exists) predicate()  {}

// inQuery renders `x IN (SELECT …)`. A predicate-shaped x is parenthesized
// (see appendOperand).
type inQuery struct {
	x   node
	sub subquery
}

func (in inQuery) appendSQL(c *compiler) {
	appendOperand(c, in.x)
	c.buf = append(c.buf, " IN "...)
	in.sub.appendSQL(c)
}

func (inQuery) exprOf(bool) {}
func (inQuery) predicate()  {}

// inQueryOf reports whether e is one of the values sub returns. The column
// kinds share this helper.
func inQueryOf[T any](e Expr[T], sub Subquery[T]) Pred { return inQuery{x: e, sub: sub.sub} }
