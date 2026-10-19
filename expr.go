package atla

import (
	"database/sql"
	"errors"
)

// node is implemented by every SQL AST node.
type node interface {
	appendSQL(c *compiler)
}

// Expr is a SQL expression that evaluates to the Go type T.
//
// The unexported exprOf method makes Expr[int64] and Expr[string] distinct,
// mutually non-assignable types, and seals the interface.
type Expr[T any] interface {
	node
	exprOf(T)
}

// Pred is a boolean SQL expression, accepted by Where, Having and join
// conditions.
type Pred = Expr[bool]

// param is a bind parameter.
type param[T any] struct{ v T }

func (p param[T]) appendSQL(c *compiler) { c.param(p.v) }
func (param[T]) exprOf(T)                {}

// predicate is implemented by predicate-shaped nodes (comparisons, AND/OR,
// NOT, IS NULL, IN, BETWEEN). As an operand of binary they are wrapped in
// parentheses, because SQL comparison operators do not nest without them.
// Atoms such as columns and parameters stay bare.
type predicate interface {
	node
	predicate()
}

// errNilPredicate is reported when a nil predicate or expression is compiled
// as an operand.
var errNilPredicate = errors.New("atla: nil predicate")

// errZeroOrder is reported when an Order without an expression is compiled.
var errZeroOrder = errors.New("atla: zero Order")

// appendOperand renders n as an operand of a binary operator.
func appendOperand(c *compiler, n node) {
	if n == nil {
		c.fail(errNilPredicate)
		return
	}
	if _, ok := n.(predicate); !ok {
		n.appendSQL(c)
		return
	}
	if l, ok := n.(logical); ok && len(l.ps) > 1 {
		l.appendSQL(c) // already parenthesized
		return
	}
	c.buf = append(c.buf, '(')
	n.appendSQL(c)
	c.buf = append(c.buf, ')')
}

// rawFragment is implemented by Raw nodes. Their SQL is inserted verbatim,
// so inside a multi-operand AND/OR and inside arithmetic they are wrapped in
// parentheses to keep a fragment such as `a OR b` or `a + b` grouped.
type rawFragment interface {
	node
	rawFragment()
}

// appendGrouped renders n, parenthesizing it when it is a Raw fragment.
func appendGrouped(c *compiler, n node) {
	if _, ok := n.(rawFragment); !ok {
		n.appendSQL(c)
		return
	}
	c.buf = append(c.buf, '(')
	n.appendSQL(c)
	c.buf = append(c.buf, ')')
}

// binary renders `l op r`, parenthesizing predicate operands.
type binary[T any] struct {
	l  node
	op string
	r  node
}

func (b binary[T]) appendSQL(c *compiler) {
	appendOperand(c, b.l)
	c.buf = append(c.buf, ' ')
	c.buf = append(c.buf, b.op...)
	c.buf = append(c.buf, ' ')
	appendOperand(c, b.r)
}

func (binary[T]) exprOf(T)   {}
func (binary[T]) predicate() {}

// unary renders `prefix x suffix`, e.g. `NOT (x)` or `x IS NULL`. Without a
// prefix, x is rendered as an operand (see appendOperand); with one, the
// prefix is expected to open its own parenthesis.
type unary[T any] struct {
	prefix string
	x      node
	suffix string
}

func (u unary[T]) appendSQL(c *compiler) {
	switch {
	case u.prefix == "":
		appendOperand(c, u.x)
	case u.x == nil:
		c.fail(errNilPredicate)
		return
	default:
		c.buf = append(c.buf, u.prefix...)
		u.x.appendSQL(c)
	}
	c.buf = append(c.buf, u.suffix...)
}

func (unary[T]) exprOf(T)   {}
func (unary[T]) predicate() {}

// logical joins predicates with AND or OR.
type logical struct {
	op string // " AND " or " OR "
	ps []Pred
}

const (
	opAnd = " AND "
	opOr  = " OR "
)

// newLogical builds a logical node. A one-operand logical operand (of
// either operator) is first replaced by its operand, recursively, so Or(p)
// combines exactly like p; then operands that are logical nodes with the
// same operator are flattened. The result never aliases ps.
func newLogical(op string, ps []Pred) logical {
	out := make([]Pred, 0, len(ps))
	for _, p := range ps {
		for {
			l, ok := p.(logical)
			if !ok || len(l.ps) != 1 {
				break
			}
			p = l.ps[0]
		}
		if l, ok := p.(logical); ok && l.op == op {
			out = append(out, l.ps...)
			continue
		}
		out = append(out, p)
	}
	return logical{op: op, ps: out}
}

func (l logical) appendSQL(c *compiler) {
	for _, p := range l.ps {
		if p == nil {
			c.fail(errNilPredicate)
			return
		}
	}
	switch len(l.ps) {
	case 0:
		if l.op == opAnd {
			c.buf = append(c.buf, "1 = 1"...)
		} else {
			c.buf = append(c.buf, "1 = 0"...)
		}
		return
	case 1:
		l.ps[0].appendSQL(c)
		return
	}
	c.buf = append(c.buf, '(')
	for i, p := range l.ps {
		if i > 0 {
			c.buf = append(c.buf, l.op...)
		}
		appendGrouped(c, p)
	}
	c.buf = append(c.buf, ')')
}

func (logical) exprOf(bool) {}
func (logical) predicate()  {}

// alwaysTrue reports whether p is true for every row by construction: an
// AND whose operands are all always true (including an empty AND, which
// compiles to `1 = 1`), or an OR with an always-true operand. An empty OR
// (`1 = 0`), an empty NOT IN and every other predicate are not.
func alwaysTrue(p Pred) bool {
	l, ok := p.(logical)
	if !ok {
		return false
	}
	if l.op == opAnd {
		for _, q := range l.ps {
			if !alwaysTrue(q) {
				return false
			}
		}
		return true
	}
	for _, q := range l.ps {
		if alwaysTrue(q) {
			return true
		}
	}
	return false
}

// inList renders `x IN (…)` or `x NOT IN (…)`, with each value bound as a
// parameter. A predicate-shaped x is parenthesized (see appendOperand).
type inList struct {
	x    node
	vals []any
	not  bool
}

func (in inList) appendSQL(c *compiler) {
	if len(in.vals) == 0 {
		if in.not {
			c.buf = append(c.buf, "1 = 1"...)
		} else {
			c.buf = append(c.buf, "1 = 0"...)
		}
		return
	}
	appendOperand(c, in.x)
	if in.not {
		c.buf = append(c.buf, " NOT IN ("...)
	} else {
		c.buf = append(c.buf, " IN ("...)
	}
	for i, v := range in.vals {
		if i > 0 {
			c.buf = append(c.buf, ", "...)
		}
		c.param(v)
	}
	c.buf = append(c.buf, ')')
}

func (inList) exprOf(bool) {}
func (inList) predicate()  {}

// between renders `x BETWEEN lo AND hi`, parenthesizing predicate-shaped
// operands (see appendOperand).
type between struct {
	x, lo, hi node
}

func (b between) appendSQL(c *compiler) {
	appendOperand(c, b.x)
	c.buf = append(c.buf, " BETWEEN "...)
	appendOperand(c, b.lo)
	c.buf = append(c.buf, " AND "...)
	appendOperand(c, b.hi)
}

func (between) exprOf(bool) {}
func (between) predicate()  {}

// nullOf presents x as a nullable expression without changing its SQL.
type nullOf[T any] struct{ x node }

func (n nullOf[T]) appendSQL(c *compiler) { n.x.appendSQL(c) }
func (nullOf[T]) exprOf(sql.Null[T])      {}

// Order is an ORDER BY item. It is not an expression.
type Order struct {
	expr  node
	desc  bool
	nulls int8 // 0: unspecified, nullsFirst, nullsLast
}

const (
	nullsFirst int8 = 1
	nullsLast  int8 = 2
)

// NullsFirst returns o with NULLS FIRST.
func (o Order) NullsFirst() Order {
	o.nulls = nullsFirst
	return o
}

// NullsLast returns o with NULLS LAST.
func (o Order) NullsLast() Order {
	o.nulls = nullsLast
	return o
}

func (o Order) appendSQL(c *compiler) {
	if o.expr == nil {
		c.fail(errZeroOrder)
		return
	}
	o.expr.appendSQL(c)
	if o.desc {
		c.buf = append(c.buf, " DESC"...)
	} else {
		c.buf = append(c.buf, " ASC"...)
	}
	switch o.nulls {
	case nullsFirst:
		c.buf = append(c.buf, " NULLS FIRST"...)
	case nullsLast:
		c.buf = append(c.buf, " NULLS LAST"...)
	}
}

// And combines predicates with AND. With no operands it compiles to `1 = 1`.
func And(ps ...Pred) Pred { return newLogical(opAnd, ps) }

// Or combines predicates with OR. With no operands it compiles to `1 = 0`.
func Or(ps ...Pred) Pred { return newLogical(opOr, ps) }

// Not negates p.
func Not(p Pred) Pred { return unary[bool]{prefix: "NOT (", x: p, suffix: ")"} }
