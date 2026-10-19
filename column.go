package atla

import "database/sql"

// Column is a table column whose values have the Go type T. It is a small
// comparable value and implements [Expr].
type Column[T any] struct {
	table string // quoted table name or alias, e.g. `"users"`
	name  string // quoted column name, e.g. `"age"`
}

// NewColumn returns the column name of table; both are already quoted.
// For use by generated code.
func NewColumn[T any](table, name string) Column[T] {
	return Column[T]{table: table, name: name}
}

func (c Column[T]) appendSQL(cc *compiler) {
	cc.buf = append(cc.buf, c.table...)
	cc.buf = append(cc.buf, '.')
	cc.buf = append(cc.buf, c.name...)
}

func (Column[T]) exprOf(T) {}

func (c Column[T]) cmp(op string, r node) Pred {
	return binary[bool]{l: c, op: op, r: r}
}

// EQ reports c = v.
func (c Column[T]) EQ(v T) Pred { return c.cmp("=", param[T]{v}) }

// NE reports c <> v.
func (c Column[T]) NE(v T) Pred { return c.cmp("<>", param[T]{v}) }

// LT reports c < v.
func (c Column[T]) LT(v T) Pred { return c.cmp("<", param[T]{v}) }

// LTE reports c <= v.
func (c Column[T]) LTE(v T) Pred { return c.cmp("<=", param[T]{v}) }

// GT reports c > v.
func (c Column[T]) GT(v T) Pred { return c.cmp(">", param[T]{v}) }

// GTE reports c >= v.
func (c Column[T]) GTE(v T) Pred { return c.cmp(">=", param[T]{v}) }

// EQExpr reports c = e.
func (c Column[T]) EQExpr(e Expr[T]) Pred { return c.cmp("=", e) }

// NEExpr reports c <> e.
func (c Column[T]) NEExpr(e Expr[T]) Pred { return c.cmp("<>", e) }

// LTExpr reports c < e.
func (c Column[T]) LTExpr(e Expr[T]) Pred { return c.cmp("<", e) }

// LTEExpr reports c <= e.
func (c Column[T]) LTEExpr(e Expr[T]) Pred { return c.cmp("<=", e) }

// GTExpr reports c > e.
func (c Column[T]) GTExpr(e Expr[T]) Pred { return c.cmp(">", e) }

// GTEExpr reports c >= e.
func (c Column[T]) GTEExpr(e Expr[T]) Pred { return c.cmp(">=", e) }

func toAny[T any](vs []T) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}

// In reports whether c is one of vs. With no values it compiles to `1 = 0`.
func (c Column[T]) In(vs ...T) Pred { return inList{x: c, vals: toAny(vs)} }

// NotIn reports whether c is none of vs. With no values it compiles to `1 = 1`.
func (c Column[T]) NotIn(vs ...T) Pred { return inList{x: c, vals: toAny(vs), not: true} }

// Between reports lo <= c <= hi.
func (c Column[T]) Between(lo, hi T) Pred {
	return between{x: c, lo: param[T]{lo}, hi: param[T]{hi}}
}

// IsNull reports whether c is NULL.
func (c Column[T]) IsNull() Pred { return unary[bool]{x: c, suffix: " IS NULL"} }

// IsNotNull reports whether c is not NULL.
func (c Column[T]) IsNotNull() Pred { return unary[bool]{x: c, suffix: " IS NOT NULL"} }

// Asc orders by c ascending.
func (c Column[T]) Asc() Order { return Order{expr: c} }

// Desc orders by c descending.
func (c Column[T]) Desc() Order { return Order{expr: c, desc: true} }

// Null returns c typed as nullable. Use it to project a NOT NULL column from
// the nullable side of an outer join.
func (c Column[T]) Null() Expr[sql.Null[T]] { return nullOf[T]{x: c} }
