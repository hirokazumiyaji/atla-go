package atla

import (
	"database/sql"
	"strings"
)

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

// EQ reports c = v.
func (c Column[T]) EQ(v T) Pred { return EQ[T](c, v) }

// NE reports c <> v.
func (c Column[T]) NE(v T) Pred { return NE[T](c, v) }

// LT reports c < v.
func (c Column[T]) LT(v T) Pred { return LT[T](c, v) }

// LTE reports c <= v.
func (c Column[T]) LTE(v T) Pred { return LTE[T](c, v) }

// GT reports c > v.
func (c Column[T]) GT(v T) Pred { return GT[T](c, v) }

// GTE reports c >= v.
func (c Column[T]) GTE(v T) Pred { return GTE[T](c, v) }

// EQExpr reports c = e.
func (c Column[T]) EQExpr(e Expr[T]) Pred { return EQExpr[T](c, e) }

// NEExpr reports c <> e.
func (c Column[T]) NEExpr(e Expr[T]) Pred { return NEExpr[T](c, e) }

// LTExpr reports c < e.
func (c Column[T]) LTExpr(e Expr[T]) Pred { return LTExpr[T](c, e) }

// LTEExpr reports c <= e.
func (c Column[T]) LTEExpr(e Expr[T]) Pred { return LTEExpr[T](c, e) }

// GTExpr reports c > e.
func (c Column[T]) GTExpr(e Expr[T]) Pred { return GTExpr[T](c, e) }

// GTEExpr reports c >= e.
func (c Column[T]) GTEExpr(e Expr[T]) Pred { return GTEExpr[T](c, e) }

// In reports whether c is one of vs. With no values it compiles to `1 = 0`.
func (c Column[T]) In(vs ...T) Pred { return In[T](c, vs...) }

// NotIn reports whether c is none of vs. With no values it compiles to `1 = 1`.
func (c Column[T]) NotIn(vs ...T) Pred { return NotIn[T](c, vs...) }

// InQuery reports whether c is one of the values sub returns.
func (c Column[T]) InQuery(sub Subquery[T]) Pred { return inQueryOf[T](c, sub) }

// Between reports lo <= c <= hi.
func (c Column[T]) Between(lo, hi T) Pred { return betweenOf[T](c, lo, hi) }

// IsNull reports whether c is NULL.
func (c Column[T]) IsNull() Pred { return IsNull[T](c) }

// IsNotNull reports whether c is not NULL.
func (c Column[T]) IsNotNull() Pred { return IsNotNull[T](c) }

// Asc orders by c ascending.
func (c Column[T]) Asc() Order { return Asc[T](c) }

// Desc orders by c descending.
func (c Column[T]) Desc() Order { return Desc[T](c) }

// Null returns c typed as nullable. Use it to project a NOT NULL column from
// the nullable side of an outer join.
func (c Column[T]) Null() Expr[sql.Null[T]] { return nullOf[T]{x: c} }

// Number is the set of Go types that support SQL arithmetic.
type Number interface {
	~int8 | ~int16 | ~int32 | ~int64 | ~int | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// NumberColumn is a numeric column. It adds arithmetic to [Column].
type NumberColumn[T Number] struct{ Column[T] }

// NewNumberColumn returns the numeric column name of table; both are
// already quoted. For use by generated code.
func NewNumberColumn[T Number](table, name string) NumberColumn[T] {
	return NumberColumn[T]{NewColumn[T](table, name)}
}

// Add returns c + v.
func (c NumberColumn[T]) Add(v T) Expr[T] { return Add[T](c.Column, v) }

// Sub returns c - v.
func (c NumberColumn[T]) Sub(v T) Expr[T] { return Sub[T](c.Column, v) }

// Mul returns c * v.
func (c NumberColumn[T]) Mul(v T) Expr[T] { return Mul[T](c.Column, v) }

// Div returns c / v.
func (c NumberColumn[T]) Div(v T) Expr[T] { return Div[T](c.Column, v) }

// AddExpr returns c + e.
func (c NumberColumn[T]) AddExpr(e Expr[T]) Expr[T] { return AddExpr[T](c.Column, e) }

// SubExpr returns c - e.
func (c NumberColumn[T]) SubExpr(e Expr[T]) Expr[T] { return SubExpr[T](c.Column, e) }

// MulExpr returns c * e.
func (c NumberColumn[T]) MulExpr(e Expr[T]) Expr[T] { return MulExpr[T](c.Column, e) }

// DivExpr returns c / e.
func (c NumberColumn[T]) DivExpr(e Expr[T]) Expr[T] { return DivExpr[T](c.Column, e) }

// StringColumn is a string column; T is string or a generated enum type.
// It adds pattern matching to [Column].
type StringColumn[T ~string] struct{ Column[T] }

// NewStringColumn returns the string column name of table; both are already
// quoted. For use by generated code.
func NewStringColumn[T ~string](table, name string) StringColumn[T] {
	return StringColumn[T]{NewColumn[T](table, name)}
}

// Like reports c LIKE pattern. The pattern is used as is.
func (c StringColumn[T]) Like(pattern string) Pred { return like{x: c.Column, pattern: pattern} }

// ILike reports a case-insensitive LIKE: native ILIKE where the dialect has
// it, otherwise LOWER(c) LIKE LOWER(pattern).
func (c StringColumn[T]) ILike(pattern string) Pred {
	return like{x: c.Column, pattern: pattern, fold: true}
}

// Contains reports whether c contains s literally.
func (c StringColumn[T]) Contains(s string) Pred {
	return like{x: c.Column, pattern: "%" + likeEscaper.Replace(s) + "%", escape: true}
}

// HasPrefix reports whether c starts with s literally.
func (c StringColumn[T]) HasPrefix(s string) Pred {
	return like{x: c.Column, pattern: likeEscaper.Replace(s) + "%", escape: true}
}

// HasSuffix reports whether c ends with s literally.
func (c StringColumn[T]) HasSuffix(s string) Pred {
	return like{x: c.Column, pattern: "%" + likeEscaper.Replace(s), escape: true}
}

// like renders `x LIKE $1`, optionally with `ESCAPE '\'`. With fold it is a
// case-insensitive match, expanded by the compiler for dialects without
// ILIKE.
type like struct {
	x       node
	pattern string
	fold    bool
	escape  bool
}

func (l like) appendSQL(c *compiler) {
	switch {
	case l.fold && !c.d.NativeILike():
		c.buf = append(c.buf, "LOWER("...)
		l.x.appendSQL(c)
		c.buf = append(c.buf, ") LIKE LOWER("...)
		c.param(l.pattern)
		c.buf = append(c.buf, ')')
	case l.fold:
		l.x.appendSQL(c)
		c.buf = append(c.buf, " ILIKE "...)
		c.param(l.pattern)
	default:
		l.x.appendSQL(c)
		c.buf = append(c.buf, " LIKE "...)
		c.param(l.pattern)
	}
	if l.escape {
		c.buf = append(c.buf, ` ESCAPE '\'`...)
	}
}

func (like) exprOf(bool) {}
func (like) predicate()  {}

// likeEscaper escapes the escape character `\` and the wildcards `%` and `_`.
// strings.Replacer works in a single pass, so escaping `\` cannot re-escape
// the backslashes it inserts.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
