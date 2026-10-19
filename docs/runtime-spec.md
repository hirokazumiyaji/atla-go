# `atla` Runtime Core Specification

This document specifies the zero-dependency runtime package `atla`
(`github.com/hirokazumiyaji/atla-go`) and its `dialect` subpackage. It covers
expressions, the SQL compiler, dialects, the `Executor` abstraction, transactions,
query and mutation builders, joins, projections, eager loading, and errors.

Identifiers prefixed with `dbgen.` / `user.` / `post.` refer to **generated** code
(see `generator-spec.md`). Everything else is the runtime.

---

## 1. Design Principles

1. **Standard library only.** The root `go.mod` has no `require` directives.
2. **Go 1.24+.** Uses generics, `iter.Seq2`, `errors.Join`/multi-`Unwrap`, and
   `go tool` directives.
3. **`ctx` first, `error` last.** Every operation that touches the database takes
   `ctx context.Context` as its first parameter and returns an `error` as its last
   result. The runtime never panics on database errors.
4. **Immutable value builders.** Builder methods use value receivers and return a
   modified copy. Slices are cloned before appending
   (`append(slices.Clip(s), x...)`), so branching a stored base query never
   mutates it.
5. **No reflection on the hot path.** Row scanning goes through generated functions,
   SQL dispatch goes through an unexported interface method on each node, and
   argument values are passed to `database/sql` unchanged.
6. **Sealed expression types.** Users cannot implement `Expr[T]` themselves.
   `atla.Raw[T]` is the escape hatch.

---

## 2. Expressions (`expr.go`, `column.go`, `nullcolumn.go`, `ops.go`, `funcs.go`, `subquery.go`)

### 2.1 `Expr[T]`

```go
package atla

// node is implemented by every SQL AST node.
type node interface {
    appendSQL(c *compiler)
}

// Expr is a SQL expression that evaluates to Go type T.
// The unexported exprOf method makes Expr[int64] and Expr[string] distinct,
// mutually non-assignable types, and seals the interface.
type Expr[T any] interface {
    node
    exprOf(T)
}

// Pred is a boolean SQL expression, accepted by Where / Having / join conditions.
type Pred = Expr[bool]
```

> [!IMPORTANT]
> **Why a phantom method?** A generic interface whose method set does not mention `T`
> makes all instantiations assignable to each other, so `Expr[int64]` would be
> accepted where `Expr[string]` is expected. The `exprOf(T)` method prevents
> this. It was verified with Go 1.24 that each of the following fails to compile:
> `user.Age.EQ("20")`, `post.UserID.EQExpr(user.Name)`, and assigning an
> `Expr[int64]` to an `Expr[string]`.

### 2.2 Column Types

Columns are small comparable value structs. Each carries the table reference
(name or alias) and the quoted column name, which the generator precomputes.

```go
type Column[T any] struct {
    table  string // quoted table name or alias, e.g. `"users"` / `"manager"`
    name   string // quoted column name, e.g. `"age"`
}

// Constructors used by generated code only. table/name are already quoted.
func NewColumn[T any](table, name string) Column[T]
func NewNumberColumn[T Number](table, name string) NumberColumn[T]
func NewStringColumn[T ~string](table, name string) StringColumn[T]
func NewNullColumn[T any](table, name string) NullColumn[T]
func NewNullNumberColumn[T Number](table, name string) NullNumberColumn[T]
func NewNullStringColumn[T ~string](table, name string) NullStringColumn[T]

// Comparison with a Go value (bound as a parameter)
func (c Column[T]) EQ(v T) Pred
func (c Column[T]) NE(v T) Pred
func (c Column[T]) LT(v T) Pred
func (c Column[T]) LTE(v T) Pred
func (c Column[T]) GT(v T) Pred
func (c Column[T]) GTE(v T) Pred

// Comparison with another expression of the same type
func (c Column[T]) EQExpr(e Expr[T]) Pred
func (c Column[T]) NEExpr(e Expr[T]) Pred
func (c Column[T]) LTExpr(e Expr[T]) Pred   // … LTE/GT/GTE Expr variants likewise

// Sets, ranges, NULL
func (c Column[T]) In(vs ...T) Pred         // empty -> compiles to `1 = 0`
func (c Column[T]) NotIn(vs ...T) Pred      // empty -> compiles to `1 = 1`
func (c Column[T]) InQuery(sub Subquery[T]) Pred
func (c Column[T]) Between(lo, hi T) Pred
func (c Column[T]) IsNull() Pred
func (c Column[T]) IsNotNull() Pred

// Ordering
func (c Column[T]) Asc() Order
func (c Column[T]) Desc() Order
func (o Order) NullsFirst() Order
func (o Order) NullsLast() Order
```

Specialised column kinds embed `Column[T]`, so all methods above are promoted and
the specialised column still satisfies `Expr[T]`:

```go
type Number interface {
    ~int8 | ~int16 | ~int32 | ~int64 | ~int | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

type NumberColumn[T Number] struct{ Column[T] }
func (c NumberColumn[T]) Add(v T) Expr[T]
func (c NumberColumn[T]) Sub(v T) Expr[T]
func (c NumberColumn[T]) Mul(v T) Expr[T]
func (c NumberColumn[T]) Div(v T) Expr[T]
func (c NumberColumn[T]) AddExpr(e Expr[T]) Expr[T] // … Sub/Mul/Div Expr variants

type StringColumn[T ~string] struct{ Column[T] }  // T is string or a generated enum type
func (c StringColumn[T]) Like(pattern string) Pred
func (c StringColumn[T]) ILike(pattern string) Pred      // dialect-expanded, see §3
func (c StringColumn[T]) Contains(s string) Pred         // LIKE $1 ESCAPE '\', $1 = "%" + escaped(s) + "%"
func (c StringColumn[T]) HasPrefix(s string) Pred
func (c StringColumn[T]) HasSuffix(s string) Pred
```

`Contains` / `HasPrefix` / `HasSuffix` escape `%`, `_`, and the escape character,
and emit `ESCAPE '\'`. They match the Go `strings` naming.

Other columns (`bool`, `time.Time`, `[]byte`, `json.RawMessage`, override types)
use plain `Column[T]`.

**Nullable columns** use a parallel set of kinds whose *expression type* is
`sql.Null[T]`, while their comparison methods still take plain `T`:

```go
type NullColumn[T any] struct{ /* table, name */ }        // implements Expr[sql.Null[T]]
func (c NullColumn[T]) EQ(v T) Pred                         // … NE/LT/LTE/GT/GTE, In, NotIn, Between
func (c NullColumn[T]) EQExpr(e Expr[T]) Pred               // … other Expr variants
func (c NullColumn[T]) IsNull() Pred
func (c NullColumn[T]) IsNotNull() Pred
func (c NullColumn[T]) NotNull() Expr[T]                    // asserts non-NULL, e.g. after IsNotNull() or for EQExpr on the other side
func (c NullColumn[T]) Asc() Order                          // … Desc

type NullNumberColumn[T Number] struct{ NullColumn[T] }     // Add/Sub/Mul/Div -> Expr[sql.Null[T]]
type NullStringColumn[T ~string] struct{ NullColumn[T] }    // Like/ILike/Contains/HasPrefix/HasSuffix
```

So a nullable `deleted_at timestamptz` is `atla.NullColumn[time.Time]`:

- `user.DeletedAt.LT(t)` takes a `time.Time`.
- `atla.Coalesce(user.Bio, "")` type-checks.
- `atla.Select2(q, user.ID, user.Bio)` yields `Tuple2[int64, sql.Null[string]]`, so a
  projection can never fail at runtime on NULL.

Non-nullable `Column[T]` also has `IsNull()` / `IsNotNull()`, which are needed for
the right side of a `LEFT JOIN`. It also has `Null() Expr[sql.Null[T]]`, which
projects a NOT NULL column of an outer-joined table safely:
`atla.Select2(lj, user.ID, post.Title.Null())`. If you project a NOT NULL column
from the nullable side without `.Null()` and it is NULL, the projection returns a
scan error. It does not panic.

In model fields, nullability shows as `*time.Time` or `sql.Null[time.Time]`. In the
write structs it shows as `atla.Opt[*time.Time]` (see §4).

### 2.3 Expression Nodes (unexported)

| Node | Implements | SQL |
| :--- | :--- | :--- |
| `param[T]` | `Expr[T]` | bind parameter (`$1` / `?` / `%s` placeholder style is chosen by the dialect) |
| `Column[T]` | `Expr[T]` | `"users"."age"` |
| `binary[T]` | `Pred` | comparison `l op r` (`=`, `<>`, `<`, `<=`, `>`, `>=`) |
| `arith[T]` | `Expr[T]` | arithmetic `(l op r)` (`+`, `-`, `*`, `/`). It parenthesizes itself, so nested arithmetic is unambiguous: `atla.Add(user.Age.Add(1), 2)` → `(("users"."age" + $1) + $2)`. |
| `unary[T]` | `Pred` | `NOT (x)`, `x IS NULL`, `x IS NOT NULL` |
| `logical` | `Pred` | `(a AND b AND c)`. Nested nodes with the same operator are flattened, and a single operand is emitted without parentheses. With no operands, `And()` compiles to `1 = 1` and `Or()` to `1 = 0`. |
| `inList` | `Pred` | `x IN ($1, $2)`. An empty list compiles to `1 = 0` (`NOT IN` → `1 = 1`). Non-generic: the typed `In` / `NotIn` functions convert the values to `[]any`. |
| `between` | `Pred` | `x BETWEEN $1 AND $2`. Non-generic, like `inList`. |
| `like` | `Pred` | `x LIKE $1`, with ` ESCAPE '\'` for `Contains` / `HasPrefix` / `HasSuffix`. `ILike` renders `x ILIKE $1` when `Dialect.NativeILike()` is true and `LOWER(x) LIKE LOWER($1)` otherwise. |
| `funcCall[T]` | `Expr[T]` | `COUNT(*)`, `COUNT(DISTINCT x)`, `MAX(x)`, `COALESCE(a, b)` |
| `raw[T]` | `Expr[T]` | user SQL with `?` markers rewritten to dialect placeholders (see `Raw`, §2.4) |
| `nullOf[T]` | `Expr[sql.Null[T]]` | `x` unchanged; presents a non-null expression as nullable (`Column.Null()`, `NullNumberColumn` arithmetic) |
| `subquery` | wrapped by `Subquery[T]` / `AnySubquery` | `(SELECT …)`. Placeholder numbering continues from the enclosing statement. Compiling a zero `Subquery` / `AnySubquery` fails with `atla: empty subquery`. |
| `inQuery` | `Pred` | `x IN (SELECT …)` |
| `exists` | `Pred` | `EXISTS (SELECT …)` |

**Operand parenthesization.** The compiler adds parentheses only where SQL
precedence would otherwise change the meaning:

- *Predicate-shaped* nodes are comparisons (`binary`), `logical`, `unary`
  (`NOT`, `IS [NOT] NULL`), `inList`, `between`, `like`, `inQuery` and `exists`.
  As the operand of a comparison, `IN` / `NOT IN`, `IN (SELECT …)`, `BETWEEN`
  (subject or bounds) or `IS [NOT] NULL`, a predicate-shaped node is wrapped in
  parentheses: `"users"."active" = ("users"."age" = $1)`,
  `("t"."a" = $1) IN ($2)`, `(NOT ("t"."a" = $1)) IS NULL`. A `logical` node with
  two or more operands already carries its own parentheses and is not wrapped again.
- Other nodes (columns, parameters, function calls, `arith`, `nullOf`, `raw`) are
  rendered bare as operands.
- A `raw` node is wrapped in parentheses when it is an operand of a `logical` node
  with two or more operands (`((a = 1 OR b = 2) AND "users"."id" = $1)`) or of an
  `arith` node (`((a + b) * $1)`). Everywhere else, including as the operand of a
  comparison, it is inserted bare (see `Raw`, §2.4).

### 2.4 Helper Functions

```go
func And(ps ...Pred) Pred
func Or(ps ...Pred) Pred
func Not(p Pred) Pred

func Count() Expr[int64]
func CountOf[T any](e Expr[T]) Expr[int64]
func CountDistinct[T any](e Expr[T]) Expr[int64]
func Sum[T Number](e Expr[T]) Expr[sql.Null[T]]
func Avg[T Number](e Expr[T]) Expr[sql.Null[float64]]
func Min[T any](e Expr[T]) Expr[sql.Null[T]]
func Max[T any](e Expr[T]) Expr[sql.Null[T]]
func Coalesce[T any](e Expr[sql.Null[T]], fallback T) Expr[T]

// Raw SQL escape hatch. `?` markers are rewritten to the dialect's placeholders.
func Raw[T any](sql string, args ...any) Expr[T]
func Exists(sub AnySubquery) Pred
```

Aggregates that can return SQL `NULL` are typed as `sql.Null[T]`. As a result,
scanning their projections can never fail on NULL.

**`Raw`.** Each `?` in `sql` becomes the next bind placeholder and takes the next
value from `args`. `??` produces a literal `?`, for Postgres jsonb operators such as
`data ?? 'key'`. If the number of `?` markers differs from `len(args)`, compilation
fails and `ToSQL` / the terminal method returns
`atla: Raw: N placeholders, M args` (N markers, M args). `args` are copied when
`Raw` is called.

The fragment is inserted verbatim. It is parenthesized only inside a multi-operand
`And` / `Or` and inside arithmetic (§2.3). As the operand of a comparison, `IN`,
`BETWEEN` or `IS NULL` it stays bare, so
`atla.EQ(atla.Raw[int64]("length(?) + ?", s, 1), 3)` renders
`length($1) + $2 = $3`. A fragment containing an operator of
lower precedence than the one it is combined with (for example `a OR b` compared
with `=`) must be parenthesized by the caller.

**Operators on arbitrary expressions.** `Expr[T]` is a sealed interface without
exported methods, so the column methods are sugar. The general forms are generic
free functions that accept any `Expr[T]`:

```go
func EQ[T any](e Expr[T], v T) Pred          // … NE, LT, LTE, GT, GTE
func EQExpr[T any](a, b Expr[T]) Pred        // … other Expr variants
func IsNull[T any](e Expr[T]) Pred
func In[T any](e Expr[T], vs ...T) Pred
func Add[T Number](e Expr[T], v T) Expr[T]   // … Sub, Mul, Div, AddExpr …
func Asc[T any](e Expr[T]) Order             // … Desc
```

Examples: `Having(atla.GT(atla.Count(), 5))`,
`OrderBy(atla.Desc(atla.Max(user.Age)))`, `atla.Add(user.Age.Add(1), 2)`.

**Subqueries** come from single-column projections:
`atla.Select1(q, post.AuthorID).Subquery()` returns `Subquery[int64]`, which is used
as `user.ID.InQuery(sub)`. `Exists` takes any builder's `.Subquery()` result
(`AnySubquery`).

---

## 3. Dialects & Compiler (`dialect/`, `compiler.go`)

### 3.1 `dialect.Dialect`

```go
package dialect

type Dialect interface {
    Name() string                                  // "postgres", "sqlite", "mysql", "tidb"
    AppendIdent(b []byte, ident string) []byte     // quoting + escaping
    AppendPlaceholder(b []byte, n int) []byte      // n is 1-based
    Returning() bool                               // RETURNING supported
    NativeILike() bool                             // ILIKE operator available
    AppendLock(b []byte, l Lock) []byte            // row lock clause, leading space
    AppendLimitOffset(b []byte, limit, offset int) []byte // LIMIT/OFFSET literals, leading space
    MaxParams() int                                // bind-parameter limit used for chunking
    Classify(err error) ErrorKind                  // driver error -> kind (KindUnknown if not recognized)
}

// Lock describes a row lock clause such as FOR UPDATE.
type Lock struct {
    Strength LockStrength
    Wait     LockWait
    Of       []string // already-quoted table references for the OF clause
}

type LockStrength uint8

const (
    LockUpdate LockStrength = iota // FOR UPDATE
    LockShare                      // FOR SHARE
)

type LockWait uint8

const (
    WaitDefault    LockWait = iota // wait for locked rows
    WaitNoWait                     // NOWAIT
    WaitSkipLocked                 // SKIP LOCKED
)

// ErrorKind lives in package dialect so that dialect does not import atla
// (atla imports dialect). atla maps each kind to its sentinel error (§10).
type ErrorKind uint8

const (
    KindUnknown ErrorKind = iota
    KindUniqueViolation
    KindForeignKeyViolation
    KindNotNullViolation
    KindCheckViolation
    KindSerialization
)

var (
    Postgres Dialect // "ident", $n, RETURNING, native ILIKE, 65535 params
    SQLite   Dialect // "ident", ?, RETURNING (3.35+), LOWER(a) LIKE LOWER(b), 32766 params
    // later: MySQL, TiDB — `ident`, ?, no RETURNING, LOWER() LIKE LOWER(), ON DUPLICATE KEY
)
```

Each dialect value is a stateless struct. Generated code references one dialect
chosen at generation time, because the precomputed SQL constants already embed
its quoting and placeholders.

- **`NativeILike`.** The compiler expands `ILike` itself: `x ILIKE $1` when it
  returns true, `LOWER(x) LIKE LOWER($1)` otherwise.
- **`AppendLock`** appends the lock clause with a leading space. Postgres renders
  ` FOR UPDATE` / ` FOR SHARE`, then ` OF a, b` when `Of` is non-empty, then
  ` NOWAIT` / ` SKIP LOCKED`. The `Of` entries are emitted verbatim, so they must
  already be quoted (`"users"`). SQLite has no row locks and appends nothing.
- **`AppendLimitOffset`** appends ` LIMIT n` and/or ` OFFSET m` as integer literals,
  never as bind parameters. A negative value means unset, and when both are unset
  nothing is appended. SQLite does not accept `OFFSET` without `LIMIT`, so with only
  an offset it renders ` LIMIT -1 OFFSET m`.

| Dialect | Quote | Placeholder | RETURNING | ILIKE | Upsert |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Postgres | `"x"` | `$1` | yes | native | `ON CONFLICT (…) DO UPDATE` |
| SQLite | `"x"` | `?` | yes (3.35+) | `LOWER(a) LIKE LOWER(b)` | `ON CONFLICT (…) DO UPDATE` |
| MySQL *(later)* | `` `x` `` | `?` | no (`LastInsertId`) | `LOWER(a) LIKE LOWER(b)` | `ON DUPLICATE KEY UPDATE` |
| TiDB *(later)* | `` `x` `` | `?` | no | same as MySQL | same as MySQL |

### 3.2 Single-Pass Compiler

```go
type compiler struct {
    d    dialect.Dialect
    buf  []byte
    args []any
    err  error // first compilation error, e.g. a Raw placeholder mismatch
}

func (c *compiler) param(v any) {                  // append placeholder + arg
    c.args = append(c.args, v)
    c.buf = c.d.AppendPlaceholder(c.buf, len(c.args))
}
```

- The compiler starts from an optional static prefix and then renders the statement
  node, whose fixed parts (`Ident`, `SelectList`, `ReturningList`) are precomputed
  strings on `Table`. `buf` is pre-sized to `len(prefix) + 128` and is not pooled
  in v1.
- Placeholder numbering continues across every node appended to the same compiler,
  so subqueries share the enclosing statement's argument list.
- The first compilation error is kept, and `ToSQL` / the terminal method returns it
  instead of SQL.
- Nodes dispatch through `appendSQL(*compiler)`, with no type switch.
- The result is converted to `string` once at the end.
- `ToSQL() (string, []any, error)` is exposed on every builder for debugging and
  testing. It returns an error rather than panicking, for example for an invalid
  `Raw` placeholder count.

---

## 4. Write Values: `Opt[T]` (`opt.go`)

```go
// Opt is a write value for INSERT / UPDATE structs. The zero value means
// "not set": the column is omitted (INSERT uses the DB default; UPDATE leaves it unchanged).
type Opt[T any] struct {
    state uint8   // unset | value | expr
    v     T
    e     Expr[T]
}

func Some[T any](v T) Opt[T]           // set to a Go value
func SomePtr[T any](v T) Opt[*T]       // nullable column, non-NULL value (takes the address of a copy)
func Null[T any]() Opt[*T]             // nullable column, explicit NULL
func SetExpr[T any](e Expr[T]) Opt[T]  // set to an SQL expression, e.g. user.Age.Add(1)
func SetExprPtr[T any](e Expr[T]) Opt[*T] // expression into a nullable column (wraps e in an internal
                                           // adapter node that implements Expr[*T]; SQL is unchanged)

func (o Opt[T]) IsSet() bool    // set to a value or to an expression
func (o Opt[T]) Get() (T, bool) // the Go value; see below

// AppendTo appends col and o's value to cols / vals when o is set and returns
// them unchanged otherwise. For use by generated code.
func (o Opt[T]) AppendTo(cols []string, vals []any, col string) ([]string, []any)
```

- **`Get`** reports only the value state. It returns the value and `true` for an
  `Opt` created by `Some`, `SomePtr` or `Null` (`Null` yields a nil pointer and
  `true`). For an unset `Opt`, and for one created by `SetExpr` / `SetExprPtr`, it
  returns the zero value and `false`; use `IsSet` to tell "set to an expression"
  from "unset".
- **`AppendTo`** is how generated `Insert` / `Update` methods build their column
  and value lists for `NewInsert` / `NewUpdate` (§6.1). For a value it appends the
  Go value, which the statement binds as a parameter. For an `Opt` created by
  `SetExpr` / `SetExprPtr` it appends the expression itself, which the statement
  renders as SQL. Like the built-in `append`, it may write into the spare capacity
  of `cols` and `vals`, so callers keep only the returned slices.
- **`SetExprPtr`** wraps `e` in an unexported adapter node that implements
  `Expr[*T]` and renders the SQL of `e` unchanged.

> [!NOTE]
> **Untyped constants.** Go infers `atla.Some(20)` as `Opt[int]`, which does not fit
> an `Opt[int64]` field. Write `atla.Some[int64](20)` or `atla.Some(int64(20))`.
> Typed variables (`atla.Some(age)`) are unaffected. This is a known ergonomic cost of
> keeping sized integer types (§ mapping in `generator-spec.md`).

---

## 5. Execution Layer (`executor.go`, `tx.go`)

### 5.1 `Executor`

```go
// Executor is satisfied by *sql.DB, *sql.Tx and *sql.Conn.
type Executor interface {
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// TxBeginner is satisfied by *sql.DB and *sql.Conn.
type TxBeginner interface {
    BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}
```

`QueryRowContext` is intentionally not required. Single-row paths use
`QueryContext` and read at most one row, which keeps wrapper implementations small.

**Query metadata travels in `ctx`.** Before every call, the runtime attaches a
`QueryInfo` to the context:

```go
type QueryInfo struct {
    Op       string // "select", "insert", "update", "delete", "exec"
    Table    string // unquoted primary table
    ReadOnly bool   // plain SELECT without locks
}

func QueryInfoFrom(ctx context.Context) (QueryInfo, bool)
```

Wrappers (hooks, replica routing; see §11) read the metadata from `ctx`, so the
`Executor` interface stays the `database/sql` shape, and `*sql.DB` keeps working
unwrapped.

### 5.2 Transactions

The generated client exposes:

```go
func (c *Client) Tx(ctx context.Context, fn func(tx *Client) error, opts ...atla.TxOption) error
```

It is implemented by the runtime helper:

```go
func RunTx(ctx context.Context, ex Executor, fn func(Executor) error, opts ...TxOption) error

func WithIsolation(l sql.IsolationLevel) TxOption
func ReadOnly() TxOption
```

```go
// TxExecutor is the Executor handed to fn inside a transaction.
// It wraps *sql.Tx and owns the savepoint counter.
type TxExecutor struct { /* tx *sql.Tx; depth int */ }
func (t *TxExecutor) QueryContext(...) (*sql.Rows, error)
func (t *TxExecutor) ExecContext(...) (sql.Result, error)
func (t *TxExecutor) Tx() *sql.Tx // escape hatch for raw database/sql use

// TxRunner is an optional capability for Executor wrappers (hooks, routers).
// RunTx delegates to it, so the wrapper can begin on the right underlying
// executor and re-wrap the transaction executor it hands to fn.
type TxRunner interface {
    RunTx(ctx context.Context, fn func(Executor) error, opts ...TxOption) error
}
```

`RunTx` chooses the first case that matches `ex`:

1. **`TxRunner`**: delegate to it.
2. **`*TxExecutor`** (a nested call): run `fn` inside `SAVEPOINT atla_sp_<n>`,
   where `n` is the parent's depth + 1, and pass `fn` a child `*TxExecutor` with
   depth `n`. Passing any `TxOption` to a nested call returns
   `atla.ErrTxOptionsNested` before any statement is sent.
3. **`*sql.Tx`** (the user passed a raw transaction): wrap it in a `*TxExecutor` with
   depth 0, then proceed as in case 2 (so `fn` runs inside `atla_sp_1`). atla never
   commits or rolls back a transaction it did not begin.
4. **`TxBeginner`**: call `BeginTx` with the options and run `fn` with a new
   `*TxExecutor`. Commit when `fn` returns `nil`. When `fn` returns an error, roll
   back and return that error joined with any rollback error (the error is returned
   unchanged when the rollback succeeds). When `fn` panics, roll back and re-panic
   with the original value.
5. **Anything else**: return `atla.ErrTxUnsupported`.

**Ending a savepoint** (cases 2 and 3):

- When `fn` returns `nil`, the savepoint is released (`RELEASE SAVEPOINT atla_sp_<n>`).
- When `fn` returns an error or panics, the runtime sends
  `ROLLBACK TO SAVEPOINT atla_sp_<n>` and then `RELEASE SAVEPOINT atla_sp_<n>`.
  `ROLLBACK TO` leaves the savepoint open, so releasing it keeps failed sibling
  calls in a loop from accumulating open savepoints. The error is `fn`'s error
  joined with any error from these statements; a panic continues after them.
- When `RELEASE SAVEPOINT` fails (for example on Postgres after `fn` swallowed a
  statement error), the runtime rolls back to the savepoint and releases it as
  above, so the outer transaction stays usable, and returns the release error
  joined with any error from that rollback.
- The `RELEASE` / `ROLLBACK TO` statements run with `context.WithoutCancel(ctx)`.
  If `fn` cancelled `ctx`, the savepoint is still rolled back, so `fn`'s writes
  cannot reach the outer commit. These statements can therefore run after the
  caller's cancellation, bounded by the lifetime of the transaction.

There is no automatic retry. `errors.Is(err, atla.ErrSerialization)` lets callers
build their own retry loop.

---

## 6. Select Builder (`table.go`, `select.go`)

### 6.1 Runtime Core

```go
// Table describes a generated table to the runtime. Every field is precomputed
// by the generator. For use by generated code.
type Table[R any] struct {
    Dialect       dialect.Dialect // the dialect the table's SQL is written for
    Ident         string          // quoted base table name, possibly schema-qualified
    RawName       string          // unquoted name, reported in QueryInfo.Table and DBError.Table
    AliasName     string          // quoted alias, or "" when not aliased
    SelectList    string          // qualified column list `"users"."id", "users"."email", …`
    ReturningList string          // unqualified column list for RETURNING `"id", "email", …`
    NumCols       int             // number of columns in SelectList

    // Scan is the single-table fast path: one rows.Scan call into the struct.
    Scan func(*sql.Rows) (*R, error)

    // NewRowScanner supports composition. rows.Scan must receive every column of a
    // row in one call, so a JOIN collects the targets of each table into one []any,
    // calls rows.Scan once, and then lets each table finish its row (widen / nullptr
    // conversions, see generator-spec §4.4).
    NewRowScanner func() RowScanner[R]

    // NewNullableRowScanner scans every column into sql.Null temporaries and reports
    // absence (all PK columns NULL) for the nullable side of a LEFT JOIN.
    // It is nil for tables without a primary key.
    NewNullableRowScanner func() RowScanner[R]

    // Alias returns the table aliased as alias (AliasName set, SelectList
    // qualified by the alias).
    Alias func(alias string) Table[R]
}

type RowScanner[R any] interface {
    Targets(dst []any) []any // appends this table's scan destinations
    Finish() (*R, error)     // returns nil, nil when absent (nullable scanner only)
}

// Select is the immutable core embedded by generated XxxQuery types.
type Select[R any] struct { /* executor, table, where, order, limit, offset, lock */ }

// Core constructors, for use by generated code. ex may be nil when the
// statement is only rendered with ToSQL. cols are quoted column names and vals
// their values, in the same order; both slices are copied.
func NewSelect[R any](ex Executor, t Table[R]) Select[R]
func NewInsert[R any](ex Executor, t Table[R], cols []string, vals []any) Insert[R]
func NewUpdate[R any](ex Executor, t Table[R], cols []string, vals []any) Update[R]
func NewDelete[R any](ex Executor, t Table[R]) Delete[R]

func (q Select[R]) Where(ps ...Pred) Select[R]
func (q Select[R]) OrderBy(os ...Order) Select[R]
func (q Select[R]) Limit(n int) Select[R]
func (q Select[R]) Offset(n int) Select[R]
func (q Select[R]) ForUpdate(opts ...LockOption) Select[R]   // SkipLocked(), NoWait(), Of(...)
func (q Select[R]) ForShare(opts ...LockOption) Select[R]

type LockOption func(*dialect.Lock)
func SkipLocked() LockOption
func NoWait() LockOption
func Of(tables ...string) LockOption // already-quoted table names or aliases, e.g. `"users"`

func (q Select[R]) All(ctx context.Context) ([]*R, error)
func (q Select[R]) First(ctx context.Context) (*R, error)   // LIMIT 1; ErrNotFound if none
func (q Select[R]) One(ctx context.Context) (*R, error)     // LIMIT 2; ErrNotFound / ErrMultipleResults
func (q Select[R]) Count(ctx context.Context) (int64, error)
func (q Select[R]) Exist(ctx context.Context) (bool, error) // SELECT EXISTS (SELECT 1 …)
func (q Select[R]) Iter(ctx context.Context) iter.Seq2[*R, error] // streams; rows closed when the loop ends
func (q Select[R]) Subquery() AnySubquery
func (q Select[R]) ToSQL() (string, []any, error)
```

- **FROM item.** The table renders as `Ident`, or `Ident AS AliasName` when
  `AliasName` is set. `NewInsert` / `NewUpdate` / `NewDelete` render only `Ident`.
- **Values in `NewInsert` / `NewUpdate`.** A `vals` entry that is an atla expression
  (for example from `Opt.AppendTo` on a `SetExpr` value, §4) is rendered as SQL. Any
  other entry becomes a bind parameter. If `cols` and `vals` differ in length,
  compilation fails with `atla: <op> <table>: N columns, M values`. `NewInsert` with
  no columns renders `INSERT INTO "t" DEFAULT VALUES`.
- **`Where`.** Repeated calls are combined with `AND`, as in atla.
- **`Limit` / `Offset`** are rendered as integer literals, not bind parameters, by
  `Dialect.AppendLimitOffset` (§3.1). A negative value leaves the limit or offset
  unset; this is also the initial state.
- **Locks.** `ForUpdate` / `ForShare` replace any earlier lock. The clause follows
  `LIMIT` / `OFFSET`. On SQLite it renders nothing. A query with a lock reports
  `QueryInfo.ReadOnly == false`.
- **`All`** returns a nil slice and a nil error when no row matches.
- **`First` / `One`** replace any limit with `LIMIT 1` / `LIMIT 2` for that call; the
  receiver is unchanged.
- **`Count`** always drops `ORDER BY`. Without a limit, an offset or a lock it
  renders `SELECT COUNT(*) FROM … WHERE …`. With a limit, an offset or a lock it
  counts the query as a subquery, because the limit must apply before counting and
  Postgres rejects `FOR UPDATE` next to an aggregate:
  `SELECT COUNT(*) FROM (SELECT 1 FROM … LIMIT n) AS "atla_count"`.
- **`Exist`** drops `ORDER BY` and keeps everything else:
  `SELECT EXISTS (SELECT 1 FROM … WHERE … LIMIT n FOR UPDATE)`.
- **`Iter`** yields `(nil, err)` once on an error and stops.

### 6.2 Generated Wrapper Pattern

Go methods cannot add type parameters, and per-table edge methods (`WithPosts`) need
a concrete type. The generator therefore emits one thin value-type wrapper per table:

```go
type UserQuery struct {
    sel   atla.Select[User]
    edges userEdgeLoads // which edges to load, with their sub-query configurators
}

func (q UserQuery) Where(ps ...atla.Pred) UserQuery   { q.sel = q.sel.Where(ps...); return q }
func (q UserQuery) OrderBy(os ...atla.Order) UserQuery { q.sel = q.sel.OrderBy(os...); return q }
// Limit, Offset, ForUpdate, ForShare likewise
func (q UserQuery) WithPosts(fns ...func(PostQuery) PostQuery) UserQuery
func (q UserQuery) All(ctx context.Context) ([]*User, error) // sel.All + edge loading
// First, One, Count, Exist, Iter, ToSQL, Unwrap() atla.Select[User]
```

`Iter` with edges loads edges per batch of 500 parents (configurable through
`atla.IterBatch(n)`), so streaming does not turn into N+1. Loading a batch's edges
runs a second query while the parent `*sql.Rows` is still open. That works on
`*sql.DB`, which uses another pooled connection. On a single connection
(`*TxExecutor`, `*sql.Tx`, `*sql.Conn`) many drivers reject it ("conn busy"), so
`Iter` with edges on those executors yields `atla.ErrIterEdgesSingleConn` before
any query runs. Use `All`, or page with `Limit`/`Offset` or keyset predicates, inside
transactions.

---

## 7. Mutations (`mutation.go`)

### 7.1 Insert

```go
client.User.Insert(dbgen.UserInsert{...})        // -> dbgen.UserInsertQuery
    .Exec(ctx)  (int64, error)                    // rows affected
    .Save(ctx)  (*dbgen.User, error)              // RETURNING <all columns>
```

- Required columns (`NOT NULL`, no default, no identity/serial/auto-increment) are
  plain struct fields. All other columns are `atla.Opt[T]`, or `atla.Opt[*T]` when
  nullable.
- Column list: required columns always appear. An `Opt` column appears only when
  `IsSet()`. Quoted column names and the `INSERT INTO "users" (` prefix are
  precomputed. When every column is set, a precomputed full statement is used.
- `Save` on a dialect without `RETURNING` (MySQL/TiDB, later phase) inserts, reads
  `LastInsertId`, then re-selects by PK. This requires a single auto-increment PK.
  Otherwise `Save` returns `atla.ErrReturningUnsupported`.

### 7.2 Update

```go
client.User.Update(dbgen.UserUpdate{Name: atla.Some("Bob"), Age: atla.SetExpr(user.Age.Add(1))}).
    Where(user.ID.EQ(id)).
    Exec(ctx)                         // (int64, error)
    // or .Save(ctx) ([]*dbgen.User, error) — RETURNING (Postgres/SQLite)
```

- `UserUpdate` contains every non-PK column as `Opt`. PK columns are not updatable
  through `UserUpdate` (my call: avoids accidental key rewrites; use `Raw` if
  needed).
- If no field is set, `ToSQL`, `Exec` and `Save` return `atla.ErrEmptyUpdate`
  without sending anything. This check comes before the unsafe-mutation guard
  (§7.4), so an empty update without `Where` reports `ErrEmptyUpdate`.
- `Save` returns every updated row. When no row matches, it returns a nil slice
  and a nil error.

### 7.3 Delete

```go
client.User.Delete().Where(user.ID.EQ(id)).Exec(ctx) // (int64, error)
```

### 7.4 Unsafe-Mutation Guard

`Update` and `Delete` without any `Where` predicate return `atla.ErrUnsafeMutation`
from `ToSQL`, `Exec` and `Save`, unless `.AllowFullTable()` was called. The check
happens before any SQL is sent; `Exec` and `Save` go through the same check as
`ToSQL`. On `Update`, `ErrEmptyUpdate` is checked first (§7.2), and `Save` checks
both guards before `ErrReturningUnsupported`.

The guard checks the effective `WHERE`, not the number of predicates: it sees
through empty conjunctions. The combined `WHERE` counts as absent when it is
always true by construction: an `AND` whose operands are all always true
(including `atla.And()` with no operands, which renders `1 = 1`), or an `OR`
with an always-true operand. So `Where(atla.And())`, `Where(atla.And(atla.And()))`
and `Where(atla.Or(atla.And()))` fail with `ErrUnsafeMutation`, and a dynamic
filter such as `Where(atla.And(filters...))` with an empty `filters` cannot
silently target every row. Predicates whose effect depends on data are not
inspected: an empty `NotIn()` (`1 = 1`) and `Raw` fragments count as real
predicates, and an empty `atla.Or()` (`1 = 0`, matches nothing) is not a
full-table risk. To intentionally update or delete every row, call
`.AllowFullTable()`.

### 7.5 Generated AOT Shortcuts

For every table with a primary key, the repository also gets precomputed-SQL
methods that bypass the builder and the compiler:

```go
func (r *UserRepo) Get(ctx context.Context, id int64) (*User, error)              // ErrNotFound
func (r *UserRepo) GetMany(ctx context.Context, ids []int64) ([]*User, error)     // chunked IN
func (r *UserRepo) DeleteByPK(ctx context.Context, id int64) error                // ErrNotFound if 0 rows

// composite PK (orgs_users: PRIMARY KEY (org_id, user_id))
func (r *MembershipRepo) Get(ctx context.Context, orgID, userID int64) (*Membership, error)
func (r *MembershipRepo) GetMany(ctx context.Context, keys []MembershipKey) ([]*Membership, error)
```

`GetMany` returns rows in database order. Duplicates collapse and missing keys are
omitted, as in atla. Composite keys use row-value `IN`
(`("org_id","user_id") IN (($1,$2),…)`) on Postgres and SQLite.

**Chunking rule** (shared by `GetMany` and edge loading): each statement carries
`min(500, dialect.MaxParams() / keyWidth)` keys.

---

## 8. Joins & Projections (`join.go`, `project.go`)

Go methods cannot introduce new type parameters, so these are generic functions.

```go
type Pair[A, B any] struct { First A; Second B }
type Triple[A, B, C any] struct { First A; Second B; Third C }

func Join[A, B any](q Selectable[A], t Table[B], on Pred) Joined[*A, *B]
func LeftJoin[A, B any](q Selectable[A], t Table[B], on Pred) Joined[*A, *B] // Second is nil when absent
func Join3[A, B, C any](j Joined[*A, *B], t Table[C], on Pred) Joined3[*A, *B, *C]
func LeftJoin3[A, B, C any](j Joined[*A, *B], t Table[C], on Pred) Joined3[*A, *B, *C]

func (j Joined[A, B]) Where(ps ...Pred) Joined[A, B]   // OrderBy/Limit/Offset/ForUpdate likewise
func (j Joined[A, B]) All(ctx context.Context) ([]Pair[A, B], error)
func (j Joined[A, B]) Iter(ctx context.Context) iter.Seq2[Pair[A, B], error]
```

- `Selectable[R]` is implemented by `atla.Select[R]` and by every generated
  `XxxQuery` (through `Unwrap()`).
- Joined rows are scanned by concatenating each table's `RowScanner.Targets` into
  one destination slice, calling `rows.Scan` once, and then calling each `Finish`.
- `LeftJoin` decides "absent" by checking whether the joined table's primary-key
  columns are all NULL, using `Table.NewNullableRowScanner`. For tables without a
  PK that field is nil, and executing a `LeftJoin` against such a table returns
  `atla.ErrNoPrimaryKey` before any SQL is sent.
- **Self joins**: `m := user.As("manager")` returns a `user.Columns` value whose
  columns render as `"manager"."id"`, and `dbgen.UserTable.Alias("manager")` returns
  the matching `Table[User]`.

Projections:

```go
type Tuple2[A, B any] struct { V1 A; V2 B } // … up to Tuple8

func Select2[R, A, B any](q Selectable[R], a Expr[A], b Expr[B]) Projected[Tuple2[A, B]]
// … Select1 (returns []A directly) through Select8

func (p Projected[T]) GroupBy(es ...AnyExpr) Projected[T]
func (p Projected[T]) Having(ps ...Pred) Projected[T]
func (p Projected[T]) Distinct() Projected[T]
func (p Projected[T]) All(ctx context.Context) ([]T, error)
func (p Projected[T]) One(ctx context.Context) (T, error)
```

Projections are scanned with `rows.Scan(&t.V1, &t.V2, …)`. Because `T` is
statically known, no reflection is needed.

---

## 9. Edge Eager Loading (`edge.go`)

Edges are generated from foreign keys (see `generator-spec.md` §5). The runtime
provides the batching core:

```go
// LoadEdge collects the parents' keys, calls fetch once per key chunk with a
// predicate `(fk cols) IN (keys)`, groups the returned children by key, and calls
// spec.Assign for every parent (with an empty slice when it has no children).
func LoadEdge[P, C any, K comparable](
    ctx context.Context,
    spec EdgeSpec[P, C, K],
    parents []*P,
    fetch func(ctx context.Context, in Pred) ([]*C, error),
) error
```

`EdgeSpec` is emitted by the generator. It holds the parent key extractor, the
child FK extractor, the child FK columns, and the assign function. `K` is the key
type: the column type for single-column keys, or a generated comparable struct
(`postAuthorKey{OrgID, UserID}`) for composite keys.

**Nested loading** happens through `fetch`. The generated `UserQuery.All` passes
`func(ctx, in) { return configured.Where(in).All(ctx) }`, where `configured` is
the `PostQuery` returned by the user's `WithPosts` configurator. That `All` loads
the post's own edges (`WithComments`) before returning, so nesting needs no extra
runtime machinery.

Semantics (mirroring ent):

| Edge kind | Example | Parent field | Not loaded |
| :--- | :--- | :--- | :--- |
| O2M | `User.WithPosts()` | `u.Edges.Posts []*Post` (empty slice, not nil, when loaded with no rows) | `u.Edges.PostsOrErr()` → `atla.ErrNotLoaded` |
| M2O | `Post.WithAuthor()` | `p.Edges.Author *User` (nil if FK is NULL) | `p.Edges.AuthorOrErr()` → `atla.ErrNotLoaded` |
| O2O | FK column(s) covered by a UNIQUE index | `u.Edges.Profile *Profile` | same |

- Nested loading works through the configurator:
  `WithPosts(func(q PostQuery) PostQuery { return q.WithComments() })`.
- `Limit` / `Offset` inside an O2M configurator apply to the whole child query, not
  per parent, and are documented as such. Per-parent limits (window functions) are
  out of scope for v1.
- Parents with all-NULL FK values are skipped without issuing a query.
- With no parents, no child query runs.

---

## 10. Errors (`errors.go`)

```go
var (
    ErrNotFound              = errors.New("atla: not found")
    ErrMultipleResults       = errors.New("atla: multiple results")
    ErrNotLoaded             = errors.New("atla: edge not loaded")
    ErrUnsafeMutation        = errors.New("atla: UPDATE/DELETE without WHERE; call AllowFullTable()")
    ErrEmptyUpdate           = errors.New("atla: update has no assignments")
    ErrTxUnsupported         = errors.New("atla: executor cannot begin a transaction")
    ErrReturningUnsupported  = errors.New("atla: RETURNING not supported for this table/dialect")
    ErrNoPrimaryKey          = errors.New("atla: table has no primary key")
    ErrIterEdgesSingleConn   = errors.New("atla: Iter with edges needs a pooled executor; use All inside transactions")
    ErrTxOptionsNested       = errors.New("atla: transaction options are not allowed on a nested transaction")

    // Database error kinds (classified from driver errors)
    ErrUniqueViolation       = errors.New("atla: unique violation")
    ErrForeignKeyViolation   = errors.New("atla: foreign key violation")
    ErrNotNullViolation      = errors.New("atla: not null violation")
    ErrCheckViolation        = errors.New("atla: check violation")
    ErrSerialization         = errors.New("atla: serialization failure or deadlock")
)

// DBError wraps a classified driver error.
type DBError struct {
    Kind  error  // one of the database error kinds above
    Op    string // "select", "insert", "update", "delete", "exec"
    Table string // unquoted table name when known
    Err   error  // the original driver error
}

func (e *DBError) Error() string   // "atla: " + Op + " " + Table + ": " + Err.Error()
func (e *DBError) Unwrap() []error { return []error{e.Kind, e.Err} }
```

So `errors.Is(err, atla.ErrUniqueViolation)` and `errors.As(err, &pgErr)` both work.

`Error` formats as `atla: <op> <table>: <driver error>`, for example
`atla: insert users: <driver message>`. `Table` is the table's `RawName`; when it
is empty the table part is empty too (`atla: exec : <driver message>`).

`ErrTxOptionsNested` is returned by `RunTx` when options are passed to a nested
transaction (§5.2).

**Classification without driver imports.** Each dialect's `Classify` returns a
`dialect.ErrorKind`, and `atla` maps it to the sentinel above. Detection uses
structural interfaces only:

| Dialect | Detection | Codes |
| :--- | :--- | :--- |
| Postgres | `interface{ SQLState() string }` (implemented by pgx `*pgconn.PgError`) | 23505, 23503, 23502, 23514, 40001, 40P01 |
| SQLite | `interface{ Code() int }` (implemented by `modernc.org/sqlite` `*sqlite.Error`) | extended codes 2067/1555 (unique/PK), 787 (FK), 1299 (not null), 275 (check), 5/6 (busy/locked → serialization) |
| MySQL *(later)* | go-sql-driver's `*mysql.MySQLError` exposes `Number` only as a field, so it goes through the registry below | 1062, 1451/1452, 1048, 3819, 1213 |

Drivers without such a method (for example `mattn/go-sqlite3` and
`go-sql-driver/mysql`) are supported through
`atla.RegisterClassifier(func(error) dialect.ErrorKind)`, which the application calls
once at startup. The runtime cannot import those drivers, so it cannot classify their
errors on its own.

Errors that cannot be classified are returned unchanged and are not wrapped.
An error that already contains a `*DBError` (checked with `errors.As`) is also
returned unchanged, so a classified error is never wrapped twice.

The dialect is consulted first; registered classifiers run afterwards, in
registration order, and the first kind other than `KindUnknown` wins.

---

## 11. Later-Phase Extension Points

These are specified at the interface level so that the v1 design does not block
them. Their detailed specs will be written when they are scheduled (see
`roadmap.md`).

| Feature | Planned shape |
| :--- | :--- |
| Upsert | `Insert(in).OnConflict(user.Email).DoUpdate(dbgen.UserUpdate{…})` / `.DoUpdateExcluded(user.Name, user.Age)` / `.DoNothing()`. MySQL maps to `ON DUPLICATE KEY UPDATE`. |
| Bulk insert | `client.User.InsertMany([]dbgen.UserInsert{…}).Exec(ctx)`. Rows are grouped by their set-column mask, and each group becomes a multi-row `VALUES` statement chunked by `MaxParams`. Each chunk is atomic; wrap the call in `Tx` for all-or-nothing behaviour. |
| COPY (Postgres) | Separate module `github.com/hirokazumiyaji/atla-go/pgxcopy` (depends on pgx). It obtains the `*pgx.Conn` through `sql.Conn.Raw` and calls `CopyFrom`. The runtime defines only `atla.CopyFromer`, an optional interface checked with a type assertion. |
| Prepared queries | `atla.Param[T](name)` slots plus `q.Prepare()`, which compiles once into `atla.Prepared[R]` and binds with `p.All(ctx, atla.Args{"min_age": 20})`. Optionally backed by `*sql.Stmt` per connection. |
| Hooks / observability | `atla.WithHooks(ex, hooks...)` returns an `Executor` (also implementing `TxRunner`, §5.2) that calls `Before(ctx, QueryInfo, sql) context.Context` / `After(ctx, QueryInfo, err)`, reading `QueryInfo` from `ctx` (§5.1). OpenTelemetry users can use otelsql at the driver level instead. |
| Read replicas | `atla.NewRouter(primary, replicas...)` returns an `Executor` + `TxRunner`. Queries whose `QueryInfo.ReadOnly` is true go to a replica, and everything else goes to the primary. Transactions always begin on the primary, so their queries reach a `*TxExecutor`, never the router. No SQL regex is needed, unlike atla's `RoutingAdapter`, and `NewClient` stays option-free. |
| M2M edges | Join tables detected as "PK = two FKs, no other non-default columns" become `User.WithGroups()` (two queries: join table + target). |

---

## 12. Versioning Between Runtime and Generated Code

Generated code uses runtime APIs that application code normally does not touch:
`Table` fields, `RowScanner`, the column constructors, `EdgeSpec` / `LoadEdge`, and
the `Select` / mutation cores. The rules:

1. The two modules are released **in lockstep** with the same version number. Tags
   are `vX.Y.Z` for the root module and `atlagen/vX.Y.Z` for the tooling module.
2. The runtime exports a constant per generated-code contract,
   `const GenVersion3 = true`, where 3 is an example of the number. Every generated
   file contains `const _ = atla.GenVersion3`, so an incompatible runtime fails at
   `go build` with an obvious name instead of with confusing type errors. This is the
   same pattern protobuf uses with `EnforceVersion`.
3. Before v1.0.0, generated-code APIs may change in minor releases, but only together
   with a new `GenVersionN`. After v1.0.0 they follow semver like the public API.
   Their godoc says "for generated code".
