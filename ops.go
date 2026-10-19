package atla

// Generic operators on arbitrary expressions. The column methods are sugar
// over these functions, so each operator is implemented once.

func cmp(l node, op string, r node) Pred { return binary[bool]{l: l, op: op, r: r} }

// EQ reports e = v.
func EQ[T any](e Expr[T], v T) Pred { return cmp(e, "=", param[T]{v}) }

// NE reports e <> v.
func NE[T any](e Expr[T], v T) Pred { return cmp(e, "<>", param[T]{v}) }

// LT reports e < v.
func LT[T any](e Expr[T], v T) Pred { return cmp(e, "<", param[T]{v}) }

// LTE reports e <= v.
func LTE[T any](e Expr[T], v T) Pred { return cmp(e, "<=", param[T]{v}) }

// GT reports e > v.
func GT[T any](e Expr[T], v T) Pred { return cmp(e, ">", param[T]{v}) }

// GTE reports e >= v.
func GTE[T any](e Expr[T], v T) Pred { return cmp(e, ">=", param[T]{v}) }

// EQExpr reports a = b.
func EQExpr[T any](a, b Expr[T]) Pred { return cmp(a, "=", b) }

// NEExpr reports a <> b.
func NEExpr[T any](a, b Expr[T]) Pred { return cmp(a, "<>", b) }

// LTExpr reports a < b.
func LTExpr[T any](a, b Expr[T]) Pred { return cmp(a, "<", b) }

// LTEExpr reports a <= b.
func LTEExpr[T any](a, b Expr[T]) Pred { return cmp(a, "<=", b) }

// GTExpr reports a > b.
func GTExpr[T any](a, b Expr[T]) Pred { return cmp(a, ">", b) }

// GTEExpr reports a >= b.
func GTEExpr[T any](a, b Expr[T]) Pred { return cmp(a, ">=", b) }

// IsNull reports whether e is NULL.
func IsNull[T any](e Expr[T]) Pred { return unary[bool]{x: e, suffix: " IS NULL"} }

// IsNotNull reports whether e is not NULL.
func IsNotNull[T any](e Expr[T]) Pred { return unary[bool]{x: e, suffix: " IS NOT NULL"} }

func toAny[T any](vs []T) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}

// In reports whether e is one of vs. With no values it compiles to `1 = 0`.
func In[T any](e Expr[T], vs ...T) Pred { return inList{x: e, vals: toAny(vs)} }

// NotIn reports whether e is none of vs. With no values it compiles to `1 = 1`.
func NotIn[T any](e Expr[T], vs ...T) Pred { return inList{x: e, vals: toAny(vs), not: true} }

// betweenOf reports lo <= e <= hi. The spec has no exported generic Between,
// so the column kinds share this helper.
func betweenOf[T any](e Expr[T], lo, hi T) Pred {
	return between{x: e, lo: param[T]{lo}, hi: param[T]{hi}}
}

// Asc orders by e ascending.
func Asc[T any](e Expr[T]) Order { return Order{expr: e} }

// Desc orders by e descending.
func Desc[T any](e Expr[T]) Order { return Order{expr: e, desc: true} }

// arith renders `(l op r)`. It parenthesizes itself so that nesting is
// unambiguous, and it is not predicate-shaped, so comparisons render it as
// is. Raw operands are parenthesized (see appendGrouped).
type arith[T any] struct {
	l  node
	op string
	r  node
}

func (a arith[T]) appendSQL(c *compiler) {
	c.buf = append(c.buf, '(')
	appendGrouped(c, a.l)
	c.buf = append(c.buf, ' ')
	c.buf = append(c.buf, a.op...)
	c.buf = append(c.buf, ' ')
	appendGrouped(c, a.r)
	c.buf = append(c.buf, ')')
}

func (arith[T]) exprOf(T) {}

// Add returns e + v.
func Add[T Number](e Expr[T], v T) Expr[T] { return arith[T]{l: e, op: "+", r: param[T]{v}} }

// Sub returns e - v.
func Sub[T Number](e Expr[T], v T) Expr[T] { return arith[T]{l: e, op: "-", r: param[T]{v}} }

// Mul returns e * v.
func Mul[T Number](e Expr[T], v T) Expr[T] { return arith[T]{l: e, op: "*", r: param[T]{v}} }

// Div returns e / v.
func Div[T Number](e Expr[T], v T) Expr[T] { return arith[T]{l: e, op: "/", r: param[T]{v}} }

// AddExpr returns a + b.
func AddExpr[T Number](a, b Expr[T]) Expr[T] { return arith[T]{l: a, op: "+", r: b} }

// SubExpr returns a - b.
func SubExpr[T Number](a, b Expr[T]) Expr[T] { return arith[T]{l: a, op: "-", r: b} }

// MulExpr returns a * b.
func MulExpr[T Number](a, b Expr[T]) Expr[T] { return arith[T]{l: a, op: "*", r: b} }

// DivExpr returns a / b.
func DivExpr[T Number](a, b Expr[T]) Expr[T] { return arith[T]{l: a, op: "/", r: b} }
