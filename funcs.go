package atla

import (
	"database/sql"
	"fmt"
	"slices"
)

// funcCall renders `name(args…)`, with `DISTINCT ` before the arguments
// when distinct is set.
type funcCall[T any] struct {
	name     string
	distinct bool
	args     []node
}

func (f funcCall[T]) appendSQL(c *compiler) {
	c.buf = append(c.buf, f.name...)
	c.buf = append(c.buf, '(')
	if f.distinct {
		c.buf = append(c.buf, "DISTINCT "...)
	}
	for i, a := range f.args {
		if i > 0 {
			c.buf = append(c.buf, ", "...)
		}
		a.appendSQL(c)
	}
	c.buf = append(c.buf, ')')
}

func (funcCall[T]) exprOf(T) {}

// star renders `*`, as in COUNT(*).
type star struct{}

func (star) appendSQL(c *compiler) { c.buf = append(c.buf, '*') }

// Count returns COUNT(*).
func Count() Expr[int64] { return funcCall[int64]{name: "COUNT", args: []node{star{}}} }

// CountOf returns COUNT(e), the number of non-NULL values of e.
func CountOf[T any](e Expr[T]) Expr[int64] {
	return funcCall[int64]{name: "COUNT", args: []node{e}}
}

// CountDistinct returns COUNT(DISTINCT e).
func CountDistinct[T any](e Expr[T]) Expr[int64] {
	return funcCall[int64]{name: "COUNT", distinct: true, args: []node{e}}
}

// Sum returns SUM(e), which is NULL when there are no rows.
func Sum[T Number](e Expr[T]) Expr[sql.Null[T]] {
	return funcCall[sql.Null[T]]{name: "SUM", args: []node{e}}
}

// Avg returns AVG(e), which is NULL when there are no rows.
func Avg[T Number](e Expr[T]) Expr[sql.Null[float64]] {
	return funcCall[sql.Null[float64]]{name: "AVG", args: []node{e}}
}

// Min returns MIN(e), which is NULL when there are no rows.
func Min[T any](e Expr[T]) Expr[sql.Null[T]] {
	return funcCall[sql.Null[T]]{name: "MIN", args: []node{e}}
}

// Max returns MAX(e), which is NULL when there are no rows.
func Max[T any](e Expr[T]) Expr[sql.Null[T]] {
	return funcCall[sql.Null[T]]{name: "MAX", args: []node{e}}
}

// Coalesce returns COALESCE(e, fallback), which is never NULL.
func Coalesce[T any](e Expr[sql.Null[T]], fallback T) Expr[T] {
	return funcCall[T]{name: "COALESCE", args: []node{e, param[T]{fallback}}}
}

// raw is user SQL whose `?` markers become bind parameters.
type raw[T any] struct {
	sql  string
	args []any
}

// Raw returns the SQL fragment sql typed as T. Each `?` becomes the next
// bind parameter, taking the next value from args, and `??` produces a
// literal `?` (for example for Postgres jsonb operators). If the number of
// `?` markers differs from len(args), compilation fails with an error.
// Every arg is always bound as a parameter: an atla expression passed as an
// arg is not rendered inline as SQL.
//
// The fragment is inserted verbatim. Inside a multi-operand [And] / [Or] and
// inside arithmetic it is wrapped in parentheses, but as an operand of a
// comparison, IN, BETWEEN or IS NULL it is not: a fragment containing an
// operator of lower precedence than the one it is combined with (for
// example `a OR b` or `a + b = c`) must be parenthesized by the caller.
func Raw[T any](sql string, args ...any) Expr[T] {
	return raw[T]{sql: sql, args: slices.Clone(args)}
}

func (r raw[T]) appendSQL(c *compiler) {
	n, start := 0, 0
	for i := 0; i < len(r.sql); i++ {
		if r.sql[i] != '?' {
			continue
		}
		c.buf = append(c.buf, r.sql[start:i]...)
		if i+1 < len(r.sql) && r.sql[i+1] == '?' {
			c.buf = append(c.buf, '?')
			i++
		} else {
			if n < len(r.args) {
				c.param(r.args[n])
			}
			n++
		}
		start = i + 1
	}
	c.buf = append(c.buf, r.sql[start:]...)
	if n != len(r.args) {
		c.fail(fmt.Errorf("atla: Raw: %d placeholders, %d args", n, len(r.args)))
	}
}

func (raw[T]) exprOf(T)     {}
func (raw[T]) rawFragment() {}
