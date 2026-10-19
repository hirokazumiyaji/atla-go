package atla

import "database/sql"

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

// binary renders `l op r`.
type binary[T any] struct {
	l  node
	op string
	r  node
}

func (b binary[T]) appendSQL(c *compiler) {
	b.l.appendSQL(c)
	c.buf = append(c.buf, ' ')
	c.buf = append(c.buf, b.op...)
	c.buf = append(c.buf, ' ')
	b.r.appendSQL(c)
}

func (binary[T]) exprOf(T) {}

// unary renders `prefix x suffix`, e.g. `NOT (x)` or `x IS NULL`.
type unary[T any] struct {
	prefix string
	x      node
	suffix string
}

func (u unary[T]) appendSQL(c *compiler) {
	c.buf = append(c.buf, u.prefix...)
	u.x.appendSQL(c)
	c.buf = append(c.buf, u.suffix...)
}

func (unary[T]) exprOf(T) {}

// logical joins predicates with AND or OR.
type logical struct {
	op string // " AND " or " OR "
	ps []Pred
}

const (
	opAnd = " AND "
	opOr  = " OR "
)

// newLogical builds a logical node, flattening operands that are logical
// nodes with the same operator. The result never aliases ps.
func newLogical(op string, ps []Pred) logical {
	out := make([]Pred, 0, len(ps))
	for _, p := range ps {
		if l, ok := p.(logical); ok && l.op == op {
			out = append(out, l.ps...)
			continue
		}
		out = append(out, p)
	}
	return logical{op: op, ps: out}
}

func (l logical) appendSQL(c *compiler) {
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
		p.appendSQL(c)
	}
	c.buf = append(c.buf, ')')
}

func (logical) exprOf(bool) {}

// inList renders `x IN (…)` or `x NOT IN (…)`, with each value bound as a
// parameter.
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
	in.x.appendSQL(c)
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

// between renders `x BETWEEN lo AND hi`.
type between struct {
	x, lo, hi node
}

func (b between) appendSQL(c *compiler) {
	b.x.appendSQL(c)
	c.buf = append(c.buf, " BETWEEN "...)
	b.lo.appendSQL(c)
	c.buf = append(c.buf, " AND "...)
	b.hi.appendSQL(c)
}

func (between) exprOf(bool) {}

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
