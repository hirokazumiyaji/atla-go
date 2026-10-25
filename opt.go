package atla

// Opt states.
const (
	optUnset uint8 = iota
	optValue
	optExpr
)

// Opt is a write value for INSERT and UPDATE structs. The zero value means
// "not set": the column is omitted, so INSERT uses the database default and
// UPDATE leaves the column unchanged. A set Opt holds either a Go value
// ([Some], [SomePtr], [Null]) or an SQL expression ([SetExpr], [SetExprPtr]).
type Opt[T any] struct {
	state uint8 // optUnset, optValue or optExpr
	v     T
	e     Expr[T]
}

// Some returns an Opt set to the Go value v.
func Some[T any](v T) Opt[T] { return Opt[T]{state: optValue, v: v} }

// SomePtr returns an Opt for a nullable column set to the non-NULL value v.
// It holds a pointer to a copy of v.
func SomePtr[T any](v T) Opt[*T] { return Opt[*T]{state: optValue, v: &v} }

// Null returns an Opt for a nullable column set to an explicit NULL.
func Null[T any]() Opt[*T] { return Opt[*T]{state: optValue} }

// SetExpr returns an Opt set to the SQL expression e, for example
// user.Age.Add(1). The expression is rendered inline in the statement.
func SetExpr[T any](e Expr[T]) Opt[T] { return Opt[T]{state: optExpr, e: e} }

// SetExprPtr returns an Opt for a nullable column set to the SQL expression
// e. The SQL of e is rendered unchanged.
func SetExprPtr[T any](e Expr[T]) Opt[*T] {
	if e == nil {
		return Opt[*T]{state: optExpr}
	}
	return Opt[*T]{state: optExpr, e: ptrExpr[T]{e: e}}
}

// ptrExpr presents an Expr[T] as an Expr[*T] without changing its SQL.
type ptrExpr[T any] struct{ e Expr[T] }

func (p ptrExpr[T]) appendSQL(c *compiler) { p.e.appendSQL(c) }
func (ptrExpr[T]) exprOf(*T)               {}

// IsSet reports whether o was set, to a value or to an expression.
func (o Opt[T]) IsSet() bool { return o.state != optUnset }

// Get returns the Go value of o and true when o was set to a value with
// [Some], [SomePtr] or [Null] (Null yields a nil pointer and true). For an
// unset Opt and for one set to an expression with [SetExpr] or [SetExprPtr]
// it returns the zero value and false.
func (o Opt[T]) Get() (T, bool) {
	if o.state != optValue {
		var zero T
		return zero, false
	}
	return o.v, true
}

// AppendTo appends col and o's value to cols and vals when o is set, and
// returns them unchanged otherwise. For an Opt set with [SetExpr] or
// [SetExprPtr] it appends the expression itself, which the statement then
// renders as SQL instead of a bind parameter. Like the built-in append, it
// may write into the spare capacity of cols and vals, so callers keep only
// the returned slices. For use by generated code.
func (o Opt[T]) AppendTo(cols []string, vals []any, col string) ([]string, []any) {
	switch o.state {
	case optValue:
		return append(cols, col), append(vals, o.v)
	case optExpr:
		return append(cols, col), append(vals, o.e)
	default:
		return cols, vals
	}
}
