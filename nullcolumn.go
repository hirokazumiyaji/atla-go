package atla

import "database/sql"

// NullColumn is a nullable table column. As an expression it has the type
// sql.Null[T], while its comparison methods take plain T. It is a small
// comparable value.
type NullColumn[T any] struct {
	col Column[T] // the same column viewed as non-nullable
}

// NewNullColumn returns the nullable column name of table; both are already
// quoted. For use by generated code.
func NewNullColumn[T any](table, name string) NullColumn[T] {
	return NullColumn[T]{col: NewColumn[T](table, name)}
}

func (c NullColumn[T]) appendSQL(cc *compiler) { c.col.appendSQL(cc) }
func (NullColumn[T]) exprOf(sql.Null[T])       {}

// NotNull returns c typed as non-nullable, rendering the same SQL. Use it
// where the value is known not to be NULL, for example after IsNotNull, or
// to compare c with another column via EQExpr.
func (c NullColumn[T]) NotNull() Expr[T] { return c.col }

// EQ reports c = v.
func (c NullColumn[T]) EQ(v T) Pred { return c.col.EQ(v) }

// NE reports c <> v.
func (c NullColumn[T]) NE(v T) Pred { return c.col.NE(v) }

// LT reports c < v.
func (c NullColumn[T]) LT(v T) Pred { return c.col.LT(v) }

// LTE reports c <= v.
func (c NullColumn[T]) LTE(v T) Pred { return c.col.LTE(v) }

// GT reports c > v.
func (c NullColumn[T]) GT(v T) Pred { return c.col.GT(v) }

// GTE reports c >= v.
func (c NullColumn[T]) GTE(v T) Pred { return c.col.GTE(v) }

// EQExpr reports c = e.
func (c NullColumn[T]) EQExpr(e Expr[T]) Pred { return c.col.EQExpr(e) }

// NEExpr reports c <> e.
func (c NullColumn[T]) NEExpr(e Expr[T]) Pred { return c.col.NEExpr(e) }

// LTExpr reports c < e.
func (c NullColumn[T]) LTExpr(e Expr[T]) Pred { return c.col.LTExpr(e) }

// LTEExpr reports c <= e.
func (c NullColumn[T]) LTEExpr(e Expr[T]) Pred { return c.col.LTEExpr(e) }

// GTExpr reports c > e.
func (c NullColumn[T]) GTExpr(e Expr[T]) Pred { return c.col.GTExpr(e) }

// GTEExpr reports c >= e.
func (c NullColumn[T]) GTEExpr(e Expr[T]) Pred { return c.col.GTEExpr(e) }

// In reports whether c is one of vs. With no values it compiles to `1 = 0`.
func (c NullColumn[T]) In(vs ...T) Pred { return c.col.In(vs...) }

// NotIn reports whether c is none of vs. With no values it compiles to `1 = 1`.
func (c NullColumn[T]) NotIn(vs ...T) Pred { return c.col.NotIn(vs...) }

// Between reports lo <= c <= hi.
func (c NullColumn[T]) Between(lo, hi T) Pred { return c.col.Between(lo, hi) }

// IsNull reports whether c is NULL.
func (c NullColumn[T]) IsNull() Pred { return c.col.IsNull() }

// IsNotNull reports whether c is not NULL.
func (c NullColumn[T]) IsNotNull() Pred { return c.col.IsNotNull() }

// Asc orders by c ascending.
func (c NullColumn[T]) Asc() Order { return c.col.Asc() }

// Desc orders by c descending.
func (c NullColumn[T]) Desc() Order { return c.col.Desc() }

// NullNumberColumn is a nullable numeric column. Arithmetic on it is
// nullable too.
type NullNumberColumn[T Number] struct{ NullColumn[T] }

// NewNullNumberColumn returns the nullable numeric column name of table;
// both are already quoted. For use by generated code.
func NewNullNumberColumn[T Number](table, name string) NullNumberColumn[T] {
	return NullNumberColumn[T]{NewNullColumn[T](table, name)}
}

// Add returns c + v.
func (c NullNumberColumn[T]) Add(v T) Expr[sql.Null[T]] { return nullOf[T]{x: Add(c.col, v)} }

// Sub returns c - v.
func (c NullNumberColumn[T]) Sub(v T) Expr[sql.Null[T]] { return nullOf[T]{x: Sub(c.col, v)} }

// Mul returns c * v.
func (c NullNumberColumn[T]) Mul(v T) Expr[sql.Null[T]] { return nullOf[T]{x: Mul(c.col, v)} }

// Div returns c / v.
func (c NullNumberColumn[T]) Div(v T) Expr[sql.Null[T]] { return nullOf[T]{x: Div(c.col, v)} }

// NullStringColumn is a nullable string column; T is string or a generated
// enum type.
type NullStringColumn[T ~string] struct{ NullColumn[T] }

// NewNullStringColumn returns the nullable string column name of table;
// both are already quoted. For use by generated code.
func NewNullStringColumn[T ~string](table, name string) NullStringColumn[T] {
	return NullStringColumn[T]{NewNullColumn[T](table, name)}
}

func (c NullStringColumn[T]) str() StringColumn[T] { return StringColumn[T]{c.col} }

// Like reports c LIKE pattern. The pattern is used as is.
func (c NullStringColumn[T]) Like(pattern string) Pred { return c.str().Like(pattern) }

// ILike reports a case-insensitive LIKE; see [StringColumn.ILike].
func (c NullStringColumn[T]) ILike(pattern string) Pred { return c.str().ILike(pattern) }

// Contains reports whether c contains s literally.
func (c NullStringColumn[T]) Contains(s string) Pred { return c.str().Contains(s) }

// HasPrefix reports whether c starts with s literally.
func (c NullStringColumn[T]) HasPrefix(s string) Pred { return c.str().HasPrefix(s) }

// HasSuffix reports whether c ends with s literally.
func (c NullStringColumn[T]) HasSuffix(s string) Pred { return c.str().HasSuffix(s) }
