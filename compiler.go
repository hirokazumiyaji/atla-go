package atla

import "github.com/hirokazumiyaji/atla-go/dialect"

// compiler renders a node tree into SQL text and bind arguments in a single
// pass. Placeholder numbering continues across every node appended to the
// same compiler, so whole statements and nested subqueries share one
// argument list.
type compiler struct {
	d    dialect.Dialect
	buf  []byte
	args []any
	err  error
}

// param appends the placeholder for v and records v as the next argument.
func (c *compiler) param(v any) {
	c.args = append(c.args, v)
	c.buf = c.d.AppendPlaceholder(c.buf, len(c.args))
}

// value renders v as SQL when it is an expression node, and as a bind
// parameter otherwise.
func (c *compiler) value(v any) {
	if n, ok := v.(node); ok {
		n.appendSQL(c)
		return
	}
	c.param(v)
}

// fail records err as the compilation error. Only the first error is kept.
func (c *compiler) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

// compile renders prefix followed by n (which may be nil) for dialect d.
func compile(d dialect.Dialect, prefix string, n node) (string, []any, error) {
	c := compiler{d: d, buf: make([]byte, 0, len(prefix)+128)}
	c.buf = append(c.buf, prefix...)
	if n != nil {
		n.appendSQL(&c)
	}
	if c.err != nil {
		return "", nil, c.err
	}
	return string(c.buf), c.args, nil
}
