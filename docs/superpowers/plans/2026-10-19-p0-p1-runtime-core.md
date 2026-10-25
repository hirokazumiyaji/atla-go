# atla-go P0 + P1 (Repository Foundation & Runtime Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the atla-go repository (modules, workspace, CI) and implement the
zero-dependency `atla` runtime core: expressions, dialects, compiler, executor and
transactions, the `Select` builder, and the `Insert` / `Update` / `Delete` cores. The
generated code of P3 will be built on this core.

**Architecture:** The root module `github.com/hirokazumiyaji/atla-go` (package `atla`
plus subpackage `dialect`) uses only the standard library, its tests included.
Builders are immutable value types that compile to SQL in one pass over a `[]byte`
buffer and execute through `database/sql`-shaped interfaces. Tests run against an
in-repo fake `database/sql` driver (`internal/fakedb`), and against hand-written
"generated-like" table fixtures that use only the exported API, exactly as
`atlagen` output will.

**Tech Stack:** Go 1.24 (`go 1.24.0`), standard library only (`database/sql`,
`iter`, `context`, `errors`, `slices`, `strconv`, `sync`), GitHub Actions,
golangci-lint v2.

**Spec:** `docs/architecture.md`, `docs/runtime-spec.md`, `docs/roadmap.md`. P0 and P1
are defined in `docs/roadmap.md`. Every API in this plan comes from
`docs/runtime-spec.md` unless it is listed under "Plan decisions" below.

## Global Constraints

- Module path `github.com/hirokazumiyaji/atla-go`, package name `atla`; subpackage `github.com/hirokazumiyaji/atla-go/dialect`.
- Root `go.mod`: directive `go 1.24.0` and **no `require` lines**. This applies to test code too, so no test may import a third-party package.
- Every database-touching method takes `ctx context.Context` first and returns `error` last. Never panic on database errors.
- Builders use value receivers and return modified copies. Before appending to a slice field, clone it: `append(slices.Clip(s), x...)`.
- No reflection on the hot path. Nodes dispatch through the unexported method `appendSQL(*compiler)`.
- `Expr[T]` is sealed by the unexported methods `appendSQL` and `exprOf(T)`.
- APIs that exist for generated code carry the godoc sentence "For use by generated code."
- Sentinel error messages use the prefix `atla: `, with the exact text from `runtime-spec.md` §10.
- `gofmt` clean; `go vet` and `golangci-lint run` pass in every module.

## Plan Decisions (spec gaps settled here; Task 12 writes them back into `runtime-spec.md`)

1. **`dialect.Dialect`, final shape.** `AppendILike(b, left, right func)` is replaced by `NativeILike() bool`; the compiler does the expansion. New methods: `AppendLock(b []byte, l Lock) []byte` (SQLite emits nothing, because it has no row locks) and `AppendLimitOffset(b []byte, limit, offset int) []byte` (`-1` = unset; SQLite emits `LIMIT -1` when only an offset is set).
2. **`atla.Table[R]`, final fields.** `Dialect dialect.Dialect`, `Ident string` (quoted base name), `RawName string` (unquoted, used by `QueryInfo` and `DBError`), `AliasName string` (quoted alias or `""`), `SelectList`, `ReturningList` (unqualified column list for `RETURNING`), `NumCols`, `Scan`, `NewRowScanner`, `NewNullableRowScanner`, `Alias func(string) Table[R]`. The FROM item is `Ident`, or `Ident + " AS " + AliasName`.
3. **Core constructors for generated code.** `NewSelect[R](ex Executor, t Table[R]) Select[R]`, `NewInsert[R](ex, t, cols []string, vals []any) Insert[R]`, `NewUpdate[R](ex, t, cols []string, vals []any) Update[R]`, `NewDelete[R](ex, t) Delete[R]`. `cols` are quoted names. A `vals` entry that is an atla expression is rendered as SQL; any other entry becomes a bind parameter.
4. **`Opt[T].AppendTo(cols []string, vals []any, col string) ([]string, []any)`** (for generated code). It appends only when the value is set, and it appends the expression itself when the value was created by `SetExpr`.
5. **LIMIT / OFFSET** are rendered as integer literals, not bind parameters.
6. **`Raw`**: each `?` becomes the next placeholder, and `??` produces a literal `?` (for Postgres jsonb operators). If the number of `?` markers differs from the number of args, compilation fails with `atla: Raw: N placeholders, M args`.
7. **The unsafe-mutation guard also applies to `ToSQL`.** `Update` / `Delete` `ToSQL` return `ErrUnsafeMutation`, keeping the guard in one place.
8. **`And()` with no operands** compiles to `1 = 1`, and **`Or()`** to `1 = 0`.
9. **`Count` with `Limit` / `Offset`** wraps the query: `SELECT COUNT(*) FROM (SELECT 1 FROM … LIMIT n) AS "atla_count"`. `Count` always drops `ORDER BY`.
10. **New sentinel `ErrTxOptionsNested`** (`atla: transaction options are not allowed on a nested transaction`).
11. **Deferred from P0**: the Postgres CI service container moves to P5, its first user.

## Review Focus

1. **Branching a stored base query.** Two `Where` calls on the same base value must not see each other's predicates, even when the base slice has spare capacity. Owned by Task 7 (`TestSelectBranchingDoesNotAlias`).
2. **Postgres placeholder numbering across nested parts.** `$n` must keep counting through `Raw` args, `In` lists, and subqueries. Owned by Task 7 (`TestSelectSubqueryPlaceholderNumbering`).
3. **Identifiers containing `"`.** A user-supplied alias must be escaped by doubling the quote character. Owned by Task 2 (`TestAppendIdentEscapes`).
4. **Leaving an `Iter` loop early.** It must close `*sql.Rows`, or the pooled connection leaks. Owned by Task 8 (`TestIterBreakClosesRows`).
5. **`%`, `_`, `\` in input to `Contains`, `HasPrefix`, `HasSuffix`.** They must match literally. Owned by Task 4 (`TestContainsEscapesWildcards`).

---

## File Structure

```text
atla-go/
├── go.mod                        # T1  module root, go 1.24.0, no require
├── go.work                       # T1  use . ./atlagen ./internal/e2e
├── .gitignore                    # T1  stop ignoring go.work / go.work.sum
├── .golangci.yml                 # T1  golangci-lint v2 config
├── .github/workflows/ci.yml      # T1
├── scripts/check-no-deps.sh      # T1  fails if root go.mod has a require
├── doc.go                        # T1  package doc; T6 adds GenVersion1
├── atlagen/go.mod, atlagen/doc.go        # T1 placeholder module
├── internal/e2e/go.mod, internal/e2e/doc.go  # T1 placeholder module
├── dialect/dialect.go            # T2  Dialect, ErrorKind, Lock types
├── dialect/postgres.go           # T2
├── dialect/sqlite.go             # T2
├── expr.go                       # T3  node, Expr, Pred, param, binary, unary, logical, And/Or/Not
├── compiler.go                   # T3  compiler, compile()
├── column.go                     # T3  Column[T], Order; T4 NumberColumn, StringColumn
├── nullcolumn.go                 # T4  NullColumn / NullNumberColumn / NullStringColumn
├── ops.go                        # T4  generic operator functions (EQ, GT, Add, Desc, …)
├── funcs.go                      # T4  aggregates, Coalesce, Raw
├── compilefail_test.go           # T5
├── testdata/compilefail/<case>/main.go   # T5
├── errors.go                     # T6  sentinels, DBError, classification
├── executor.go                   # T6  Executor, TxBeginner, QueryInfo
├── internal/fakedb/fakedb.go     # T6  scripted database/sql driver for tests
├── table.go                      # T7  Table[R], RowScanner[R]
├── select.go                     # T7  Select[R] building + ToSQL; T8 terminal ops
├── subquery.go                   # T7  Subquery[T], AnySubquery, Exists, InQuery
├── fixture_test.go               # T7  hand-written "generated" user table (package atla_test)
├── tx.go                         # T9  RunTx, TxExecutor, TxRunner, TxOption
├── opt.go                        # T10 Opt[T]
└── mutation.go                   # T10 Insert; T11 Update, Delete
```

---

### Task 1: Repository, modules, workspace, CI (P0)

**Files:**
- Create: `go.mod`, `doc.go`, `go.work`, `atlagen/go.mod`, `atlagen/doc.go`, `internal/e2e/go.mod`, `internal/e2e/doc.go`, `.golangci.yml`, `.github/workflows/ci.yml`, `scripts/check-no-deps.sh`
- Modify: `.gitignore` (delete the lines `go.work` and `go.work.sum`)

**Interfaces:**
- Produces: module `github.com/hirokazumiyaji/atla-go` (package `atla`), module `github.com/hirokazumiyaji/atla-go/atlagen` (package `atlagen`, doc only), and module `github.com/hirokazumiyaji/atla-go/internal/e2e` (package `e2e`, doc only).

- [ ] **Step 1: Create the three modules.** Each `go.mod` has `go 1.24.0`. Each `doc.go` holds only a package comment. The root comment summarizes `docs/architecture.md` §0 in 2–3 sentences. `go.work` is `go 1.24.0` plus `use ( . ./atlagen ./internal/e2e )`.
- [ ] **Step 2: Write `scripts/check-no-deps.sh`.** It runs `go list -m all` in the root with `GOWORK=off` and fails unless the output is exactly `github.com/hirokazumiyaji/atla-go`. It also fails if `grep -E '^\s*require' go.mod` matches.
- [ ] **Step 3: Verify locally.**
  Run: `bash scripts/check-no-deps.sh && go vet ./... && (cd atlagen && go vet ./...) && (cd internal/e2e && go vet ./...)`
  Expected: exit 0.
- [ ] **Step 4: Write `.golangci.yml`** (`version: "2"`). Linters: default set plus `errorlint`, `gocritic`, `revive`, `misspell`, `unconvert`. Formatters: `gofmt`, `goimports` with `local-prefixes: github.com/hirokazumiyaji/atla-go`.
- [ ] **Step 5: Write `.github/workflows/ci.yml`.** Triggers: `push` to `main` and `pull_request`. Job `test`, with a matrix over Go `1.24.x` and `stable`, and for each module in `.`, `atlagen`, `internal/e2e`:
  - `gofmt -l .` must print nothing
  - `go vet ./...`
  - `go test -race ./...`

  Job `lint`: `golangci/golangci-lint-action@v8` with `version: v2.1` (or the latest v2), `working-directory` once per module. Job `isolation`: `GOWORK=off go build ./...` in each module, then `bash scripts/check-no-deps.sh`.
- [ ] **Step 6: Commit.**
  ```bash
  git add go.mod doc.go go.work .gitignore .golangci.yml .github scripts atlagen internal/e2e
  git commit -m "chore: set up modules, workspace, and CI"
  ```

### Task 2: `dialect` package (Postgres, SQLite)

**Files:**
- Create: `dialect/dialect.go`, `dialect/postgres.go`, `dialect/sqlite.go`
- Test: `dialect/dialect_test.go`

**Interfaces:**
- Produces:
  ```go
  type Dialect interface {
      Name() string
      AppendIdent(b []byte, ident string) []byte
      AppendPlaceholder(b []byte, n int) []byte // n is 1-based
      Returning() bool
      NativeILike() bool
      AppendLock(b []byte, l Lock) []byte       // appends with a leading space, or nothing
      AppendLimitOffset(b []byte, limit, offset int) []byte // -1 = unset; leading space
      MaxParams() int
      Classify(err error) ErrorKind
  }
  type ErrorKind uint8 // KindUnknown, KindUniqueViolation, KindForeignKeyViolation, KindNotNullViolation, KindCheckViolation, KindSerialization
  type LockStrength uint8 // LockUpdate, LockShare
  type LockWait uint8     // WaitDefault, WaitNoWait, WaitSkipLocked
  type Lock struct { Strength LockStrength; Wait LockWait; Of []string } // Of: quoted table refs
  var Postgres Dialect
  var SQLite Dialect
  ```

- [ ] **Step 1: Write the failing tests.** Use table-driven tests over both dialects.
  ```go
  func TestAppendIdentEscapes(t *testing.T) {
      // both dialects
      assertEq(t, string(dialect.Postgres.AppendIdent(nil, `users`)), `"users"`)
      assertEq(t, string(dialect.Postgres.AppendIdent(nil, `we"ird`)), `"we""ird"`)
      assertEq(t, string(dialect.SQLite.AppendIdent(nil, `we"ird`)), `"we""ird"`)
  }
  func TestPlaceholders(t *testing.T) {
      assertEq(t, string(dialect.Postgres.AppendPlaceholder(nil, 12)), `$12`)
      assertEq(t, string(dialect.SQLite.AppendPlaceholder(nil, 12)), `?`)
  }
  func TestCapabilities(t *testing.T) {
      // Postgres: Name "postgres", Returning true, NativeILike true, MaxParams 65535
      // SQLite:   Name "sqlite",   Returning true, NativeILike false, MaxParams 32766
  }
  func TestAppendLock(t *testing.T) {
      // Postgres:
      //   {LockUpdate, WaitDefault, nil}                   -> " FOR UPDATE"
      //   {LockShare, WaitNoWait, nil}                     -> " FOR SHARE NOWAIT"
      //   {LockUpdate, WaitSkipLocked, []string{`"users"`}} -> ` FOR UPDATE OF "users" SKIP LOCKED`
      // SQLite: every input -> ""
  }
  func TestAppendLimitOffset(t *testing.T) {
      // Postgres: (10,-1) " LIMIT 10"; (10,5) " LIMIT 10 OFFSET 5"; (-1,5) " OFFSET 5"; (-1,-1) ""
      // SQLite:   (-1,5) " LIMIT -1 OFFSET 5"; others same as Postgres
  }
  func TestClassifyPostgres(t *testing.T) {
      // fake error type with SQLState() string, wrapped with fmt.Errorf("x: %w", e)
      // "23505"->KindUniqueViolation, "23503"->KindForeignKeyViolation, "23502"->KindNotNullViolation,
      // "23514"->KindCheckViolation, "40001"/"40P01"->KindSerialization, "42601"->KindUnknown,
      // errors.New("plain")->KindUnknown
  }
  func TestClassifySQLite(t *testing.T) {
      // fake error type with Code() int
      // 2067, 1555 -> Unique; 787 -> FK; 1299 -> NotNull; 275 -> Check;
      // 5, 6, 261, 517 (primary code = code & 0xff is 5 or 6) -> Serialization; 1 -> Unknown
  }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.**
  Run: `go test ./dialect/`
  Expected: FAIL (package does not compile: undefined names).
- [ ] **Step 3: Implement.** `dialect.go` holds the interface and types. `postgres.go` and `sqlite.go` each define an unexported empty struct type. Classify uses `errors.As` with interface-typed targets (`interface{ SQLState() string }` and `interface{ Code() int }`). Placeholders use `strconv.AppendInt`.
- [ ] **Step 4: Run the tests and confirm they pass.**
  Run: `go test ./dialect/`
  Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat(dialect): add Postgres and SQLite dialects"`

### Task 3: Expression core, compiler, `Column[T]`, `Order`, logical operators

**Files:**
- Create: `expr.go`, `compiler.go`, `column.go`
- Test: `expr_test.go` (package `atla`, internal)

**Interfaces:**
- Consumes: `dialect.Dialect` (Task 2).
- Produces:
  ```go
  type node interface{ appendSQL(c *compiler) }
  type Expr[T any] interface{ node; exprOf(T) }
  type Pred = Expr[bool]
  type compiler struct { d dialect.Dialect; buf []byte; args []any; err error }
  func (c *compiler) param(v any)          // appends placeholder + arg
  func (c *compiler) value(v any)          // node -> appendSQL, else param(v)
  func (c *compiler) fail(err error)       // records the first error
  func compile(d dialect.Dialect, prefix string, n node) (string, []any, error) // tests and builders; n may be nil
  func NewColumn[T any](table, name string) Column[T]
  // Column[T]: EQ NE LT LTE GT GTE (v T) Pred; EQExpr NEExpr LTExpr LTEExpr GTExpr GTEExpr (Expr[T]) Pred;
  //            In(vs ...T) Pred; NotIn(vs ...T) Pred; Between(lo, hi T) Pred; IsNull() Pred; IsNotNull() Pred;
  //            Asc() Order; Desc() Order; Null() Expr[sql.Null[T]]
  type Order struct{ /* expr node; desc bool; nulls int8 */ }
  func (o Order) NullsFirst() Order; func (o Order) NullsLast() Order
  func (o Order) appendSQL(c *compiler)    // used by Select (Task 7)
  func And(ps ...Pred) Pred; func Or(ps ...Pred) Pred; func Not(p Pred) Pred
  ```

- [ ] **Step 1: Write the failing golden tests.** Helper: `sqlOf(t, d, n) (string, []any)` calls `compile(d, "", n)` and fails on error. Columns: `age := NewColumn[int64](`"users"`, `"age"`)`, `name := NewColumn[string](`"users"`, `"name"`)`.
  ```go
  func TestComparisonsPostgres(t *testing.T) {
      // age.EQ(20)  -> `"users"."age" = $1`, args [20]
      // age.NE(1) "<>", LT "<", LTE "<=", GT ">", GTE ">="
      // age.EQExpr(NewColumn[int64](`"posts"`, `"user_id"`)) -> `"users"."age" = "posts"."user_id"`, no args
  }
  func TestComparisonsSQLite(t *testing.T) { /* age.EQ(20) -> `"users"."age" = ?` */ }
  func TestInBetweenNull(t *testing.T) {
      // age.In(1,2,3) -> `"users"."age" IN ($1, $2, $3)`
      // age.In() -> `1 = 0`; age.NotIn() -> `1 = 1`; age.NotIn(4) -> `"users"."age" NOT IN ($1)`
      // age.Between(1, 9) -> `"users"."age" BETWEEN $1 AND $2`
      // age.IsNull() -> `"users"."age" IS NULL`; IsNotNull -> `... IS NOT NULL`
  }
  func TestLogical(t *testing.T) {
      // And(a, b)           -> `("users"."age" = $1 AND "users"."name" = $2)`
      // And(a)              -> `"users"."age" = $1`  (no parens)
      // And(And(a, b), c)   -> `(… AND … AND …)`    (flattened)
      // Or(a, And(b, c))    -> `(a OR (b AND c))`
      // And() -> `1 = 1`; Or() -> `1 = 0`
      // Not(a) -> `NOT ("users"."age" = $1)`
  }
  func TestOrder(t *testing.T) {
      // age.Asc() -> `"users"."age" ASC`; age.Desc().NullsLast() -> `"users"."age" DESC NULLS LAST`
      // age.Asc().NullsFirst() -> `"users"."age" ASC NULLS FIRST`
  }
  func TestNullAdapterRendersSameSQL(t *testing.T) { /* age.Null() renders `"users"."age"` */ }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test -run 'Comparisons|InBetween|Logical|Order|NullAdapter' .` Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** One unexported node type per row of `runtime-spec.md` §2.3, as far as this task needs (`param`, `binary`, `unary`, `logical`, `inList`, `between`). `exprOf` is an empty method. `Column.Null()` returns an adapter node that delegates `appendSQL` to the column. The compiler pre-sizes `buf` to `len(prefix)+128` and converts to `string` once.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test .` Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add expression core, compiler, and Column"`

### Task 4: Column kinds, nullable columns, generic operators, functions, `Raw`

**Files:**
- Create: `nullcolumn.go`, `ops.go`, `funcs.go`
- Modify: `column.go` (add `Number`, `NumberColumn`, `StringColumn`)
- Test: `column_kinds_test.go` (internal)

**Interfaces:**
- Consumes: Task 3 nodes and `compile`.
- Produces (exact names from `runtime-spec.md` §2.2 and §2.4):
  ```go
  type Number interface{ ~int8 | ~int16 | ~int32 | ~int64 | ~int | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64 }
  type NumberColumn[T Number] struct{ Column[T] }   // Add Sub Mul Div (v T) Expr[T]; AddExpr SubExpr MulExpr DivExpr (Expr[T]) Expr[T]
  type StringColumn[T ~string] struct{ Column[T] }  // Like ILike Contains HasPrefix HasSuffix (string) Pred
  type NullColumn[T any] struct{ /* table, name */ } // Expr[sql.Null[T]]; EQ…GTE(v T); *Expr(Expr[T]); In NotIn Between IsNull IsNotNull Asc Desc; NotNull() Expr[T]
  type NullNumberColumn[T Number] struct{ NullColumn[T] }   // Add…Div (v T) Expr[sql.Null[T]]
  type NullStringColumn[T ~string] struct{ NullColumn[T] }  // Like ILike Contains HasPrefix HasSuffix
  func NewNumberColumn[T Number](table, name string) NumberColumn[T]
  func NewStringColumn[T ~string](table, name string) StringColumn[T]
  func NewNullColumn[T any](table, name string) NullColumn[T]
  func NewNullNumberColumn[T Number](table, name string) NullNumberColumn[T]
  func NewNullStringColumn[T ~string](table, name string) NullStringColumn[T]
  // ops.go
  func EQ[T any](e Expr[T], v T) Pred  // NE LT LTE GT GTE likewise
  func EQExpr[T any](a, b Expr[T]) Pred // NEExpr LTExpr LTEExpr GTExpr GTEExpr likewise
  func IsNull[T any](e Expr[T]) Pred; func IsNotNull[T any](e Expr[T]) Pred
  func In[T any](e Expr[T], vs ...T) Pred; func NotIn[T any](e Expr[T], vs ...T) Pred
  func Add[T Number](e Expr[T], v T) Expr[T] // Sub Mul Div; AddExpr SubExpr MulExpr DivExpr (a, b Expr[T]) Expr[T]
  func Asc[T any](e Expr[T]) Order; func Desc[T any](e Expr[T]) Order
  // funcs.go
  func Count() Expr[int64]; func CountOf[T any](e Expr[T]) Expr[int64]; func CountDistinct[T any](e Expr[T]) Expr[int64]
  func Sum[T Number](e Expr[T]) Expr[sql.Null[T]]; func Avg[T Number](e Expr[T]) Expr[sql.Null[float64]]
  func Min[T any](e Expr[T]) Expr[sql.Null[T]];   func Max[T any](e Expr[T]) Expr[sql.Null[T]]
  func Coalesce[T any](e Expr[sql.Null[T]], fallback T) Expr[T]
  func Raw[T any](sql string, args ...any) Expr[T]
  ```
  Column methods delegate to the generic functions in `ops.go`, so each operator is implemented once.

- [ ] **Step 1: Write the failing tests.**
  ```go
  func TestArithmetic(t *testing.T) {
      // age := NewNumberColumn[int32](`"users"`, `"age"`)
      // age.Add(1) -> `("users"."age" + $1)`; Add(age.Add(1), 2) -> `(("users"."age" + $1) + $2)`
      // age.MulExpr(age) -> `("users"."age" * "users"."age")`
  }
  func TestStringPredicates(t *testing.T) {
      // name := NewStringColumn[string](`"users"`, `"name"`)
      // Postgres: name.Like("a%") -> `"users"."name" LIKE $1` args ["a%"]
      //           name.ILike("a%") -> `"users"."name" ILIKE $1`
      // SQLite:   name.ILike("a%") -> `LOWER("users"."name") LIKE LOWER(?)`
  }
  func TestContainsEscapesWildcards(t *testing.T) {
      // name.Contains(`50%_off\`) -> `"users"."name" LIKE $1 ESCAPE '\'`, args [`%50\%\_off\\%`]
      // name.HasPrefix("a_") -> args [`a\_%`];  name.HasSuffix("%") -> args [`%\%`]
  }
  func TestNullColumns(t *testing.T) {
      // bio := NewNullStringColumn[string](`"users"`, `"bio"`)
      // bio.EQ("x") -> `"users"."bio" = $1`; bio.IsNull() -> `"users"."bio" IS NULL`
      // bio.NotNull() renders `"users"."bio"`; Coalesce(bio, "") -> `COALESCE("users"."bio", $1)` args [""]
      // NewNullNumberColumn[int64](…, `"score"`).Add(1) -> `("users"."score" + $1)`
  }
  func TestAggregates(t *testing.T) {
      // Count() `COUNT(*)`; CountOf(age) `COUNT("users"."age")`; CountDistinct(age) `COUNT(DISTINCT "users"."age")`
      // Sum/Avg/Min/Max -> `SUM(…)` `AVG(…)` `MIN(…)` `MAX(…)`
      // GT(Count(), 5) -> `COUNT(*) > $1`; Desc(Max(age)) -> `MAX("users"."age") DESC`
  }
  func TestRaw(t *testing.T) {
      // Postgres: EQ(Raw[int64]("length(?) + ?", "ab", 1), 3) -> `length($1) + $2 = $3`, args ["ab",1,3]
      // Raw[bool](`"data" ?? 'k'`) -> `"data" ? 'k'` (no args)
      // Raw[bool]("? = ?", 1) -> compile error containing "atla: Raw: 2 placeholders, 1 args"
  }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test .` Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** Arithmetic is parenthesized (`(l op r)`) so that nesting is unambiguous; comparisons are not. The LIKE escaping replaces `\` first, then `%` and `_`. `Raw` scans its SQL once and calls `c.param` for each `?`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test .` Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add column kinds, nullable columns, operators, and Raw"`

### Task 5: Compile-fail suite (type-safety claims)

**Files:**
- Create: `compilefail_test.go` (package `atla_test`), `testdata/compilefail/{ok,eq_wrong_type,expr_type_mismatch,opt_wrong_type,expr_as_pred,null_vs_nonnull,sealed}/main.go`

**Interfaces:**
- Consumes: the exported API of Tasks 3–4, plus `atla.Some` / `atla.Opt` (stub them here in `opt.go` as `type Opt[T any] struct{ ... }` / `func Some[T any](v T) Opt[T]`; Task 10 completes them).

- [ ] **Step 1: Write the harness.** `TestCompileFail` skips under `testing.Short()`. For each case directory it creates a temp module containing:
  - `go.mod`: `module cf`, `go 1.24.0`, `require github.com/hirokazumiyaji/atla-go v0.0.0`, `replace github.com/hirokazumiyaji/atla-go => <abs repo root>`
  - the case's `main.go`

  It then runs `go build ./...` with `GOWORK=off` and `GOFLAGS=-mod=mod`. Case `ok` must succeed. Every other case must fail, and its combined output must contain the substring from the table below.
- [ ] **Step 2: Write the cases.** Each `main.go` builds its columns with `atla.NewNumberColumn[int64]` and the other constructors.

  | Case | Offending line | Expected substring |
  | :--- | :--- | :--- |
  | `ok` | uses every column kind correctly, plus `var _ = atla.GenVersion1` (add this after Task 6) | builds |
  | `eq_wrong_type` | `_ = age.EQ("20")` | `cannot use "20"` |
  | `expr_type_mismatch` | `_ = userID.EQExpr(name)` | `does not implement atla.Expr[int64]` |
  | `opt_wrong_type` | `var _ atla.Opt[int64] = atla.Some("x")` | `cannot use atla.Some("x")` |
  | `expr_as_pred` | `var _ atla.Pred = age` | `does not implement atla.Expr[bool]` |
  | `null_vs_nonnull` | `_ = userID.EQExpr(editorID)` (`editorID` is a `NullNumberColumn[int64]`) | `does not implement atla.Expr[int64]` |
  | `sealed` | `type my struct{}; var _ atla.Pred = my{}` | `missing method` |

- [ ] **Step 3: Run.** Run: `go test -run TestCompileFail -v .` Expected: PASS, with every subtest listed.
- [ ] **Step 4: Commit.** `git commit -m "test: add compile-fail suite for type-safety guarantees"`

### Task 6: Errors, classification, `Executor`, `QueryInfo`, fake driver, `GenVersion1`

**Files:**
- Create: `errors.go`, `executor.go`, `internal/fakedb/fakedb.go`
- Modify: `doc.go` (add `const GenVersion1 = true` with the godoc "For use by generated code.")
- Test: `errors_test.go`, `executor_test.go`, `internal/fakedb/fakedb_test.go`

**Interfaces:**
- Consumes: `dialect.ErrorKind` and `Dialect.Classify` (Task 2).
- Produces:
  ```go
  // errors.go — sentinels exactly as runtime-spec §10, plus ErrTxOptionsNested
  type DBError struct{ Kind error; Op, Table string; Err error }
  func (e *DBError) Error() string        // "atla: <op> <table>: <Err>"
  func (e *DBError) Unwrap() []error      // {Kind, Err}
  func RegisterClassifier(f func(error) dialect.ErrorKind)
  func wrapDBError(d dialect.Dialect, op, table string, err error) error // nil->nil; unknown -> err unchanged
  // executor.go
  type Executor interface {
      QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
      ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
  }
  type TxBeginner interface{ BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) }
  type QueryInfo struct{ Op, Table string; ReadOnly bool }
  func QueryInfoFrom(ctx context.Context) (QueryInfo, bool)
  func withQueryInfo(ctx context.Context, qi QueryInfo) context.Context
  // internal/fakedb
  func Open(t testing.TB) (*sql.DB, *Script)        // sql.OpenDB over an in-memory connector
  func (s *Script) ExpectQuery(query string, args ...any) *Response
  func (s *Script) ExpectExec(query string, args ...any) *Response
  func (r *Response) Rows(cols []string, rows ...[]driver.Value)
  func (r *Response) Result(rowsAffected int64)
  func (r *Response) Err(err error)
  func (s *Script) Log() []string   // "QUERY <sql>", "EXEC <sql>", "BEGIN", "BEGIN iso=<n> ro=<bool>", "COMMIT", "ROLLBACK", "ROWS CLOSED"
  func (s *Script) OpenRows() int
  func (s *Script) Done(t testing.TB) // t.Error for unmet expectations
  ```
  Behaviour of `fakedb`:
  - Expectations are matched in FIFO order by exact SQL. Args are compared with `reflect.DeepEqual` after `database/sql` default conversion; reflection is fine in test-only code.
  - Statements starting with `SAVEPOINT`, `RELEASE SAVEPOINT`, or `ROLLBACK TO SAVEPOINT` are auto-accepted, logged as `EXEC <sql>`, and report 0 rows affected.
  - Anything unexpected returns an error whose text includes the SQL.
  - The connection does **not** implement `driver.NamedValueChecker`, so the standard conversion (pointer dereference, nil → NULL) applies.

- [ ] **Step 1: Write the failing tests.**
  ```go
  func TestDBErrorIsAndAs(t *testing.T) {
      // pgErr has SQLState() "23505"; err := wrapDBError(dialect.Postgres, "insert", "users", pgErr)
      // errors.Is(err, ErrUniqueViolation) == true; errors.As(err, &target pgErrType) == true
      // err.Error() == `atla: insert users: <pgErr.Error()>`
  }
  func TestUnknownErrorUnwrapped(t *testing.T) { /* wrapDBError(…, io.EOF) == io.EOF (identity) */ }
  func TestRegisterClassifier(t *testing.T) {
      // after resetClassifiers() (export_test.go), register f: errMySQL -> KindUniqueViolation
      // wrapDBError(dialect.SQLite, "insert", "users", errMySQL) Is ErrUniqueViolation
  }
  func TestQueryInfoRoundTrip(t *testing.T) { /* QueryInfoFrom(withQueryInfo(ctx, qi)) == qi, true; on empty ctx ok=false */ }
  func TestFakeDB(t *testing.T) { // in internal/fakedb
      // ExpectQuery("SELECT 1", 7).Rows([]string{"x"}, []driver.Value{int64(1)}): QueryContext with arg 7 scans 1
      // unexpected SQL returns an error containing the SQL; Done reports an unmet expectation
      // rows.Close() increments the "ROWS CLOSED" log entry and OpenRows() returns to 0
  }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test ./...` Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** Classifiers live in a mutex-guarded slice. `wrapDBError` tries the dialect first, then the registered classifiers in order. It maps each `ErrorKind` to its sentinel.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test ./...` Expected: PASS.
- [ ] **Step 5: Add `var _ = atla.GenVersion1` to `testdata/compilefail/ok/main.go`.** Run `go test -run TestCompileFail .` and expect PASS.
- [ ] **Step 6: Commit.** `git commit -m "feat: add errors, Executor, QueryInfo, and fake driver for tests"`

### Task 7: `Table[R]`, `Select[R]` building, subqueries, fixture

**Files:**
- Create: `table.go`, `select.go`, `subquery.go`, `fixture_test.go` (package `atla_test`)
- Test: `select_sql_test.go` (package `atla_test`)

**Interfaces:**
- Consumes: Tasks 2–4 and 6.
- Produces:
  ```go
  type Table[R any] struct { // fields exactly as in Plan Decision 2
      Dialect dialect.Dialect; Ident, RawName, AliasName, SelectList, ReturningList string; NumCols int
      Scan func(*sql.Rows) (*R, error); NewRowScanner, NewNullableRowScanner func() RowScanner[R]
      Alias func(alias string) Table[R]
  }
  type RowScanner[R any] interface{ Targets(dst []any) []any; Finish() (*R, error) }
  type Select[R any] struct{ /* ex Executor; t Table[R]; where []Pred; order []Order; limit, offset int (-1 unset); lock *dialect.Lock */ }
  func NewSelect[R any](ex Executor, t Table[R]) Select[R]
  func (q Select[R]) Where(ps ...Pred) Select[R]
  func (q Select[R]) OrderBy(os ...Order) Select[R]
  func (q Select[R]) Limit(n int) Select[R]
  func (q Select[R]) Offset(n int) Select[R]
  func (q Select[R]) ForUpdate(opts ...LockOption) Select[R]
  func (q Select[R]) ForShare(opts ...LockOption) Select[R]
  type LockOption func(*dialect.Lock)
  func SkipLocked() LockOption; func NoWait() LockOption; func Of(tables ...string) LockOption
  func (q Select[R]) ToSQL() (string, []any, error)
  func (q Select[R]) Subquery() AnySubquery
  type AnySubquery struct{ /* node */ }
  type Subquery[T any] struct{ /* node */ }   // produced by Select1 in P4; until then tests use NewSubqueryForTest from export_test.go
  func Exists(sub AnySubquery) Pred
  // Column[T].InQuery(sub Subquery[T]) Pred and NullColumn[T].InQuery are added here
  ```
  `fixture_test.go` defines `type User struct{ ID int64; Name string; Age int32; Bio *string }` and `var userTable atla.Table[User]` for Postgres. Its values are:
  - `Ident` `"users"`, `RawName` `users`
  - `SelectList` `"users"."id", "users"."name", "users"."age", "users"."bio"`
  - `ReturningList` `"id", "name", "age", "bio"`, `NumCols` 4
  - a `Scan` that widens `age` through an `int64` local and reads `bio` through `sql.Null[string]`, matching generator-spec §4.4

  It also defines the columns `uID`, `uName`, `uAge`, `uBio` (Number, String, Number, NullString). For posts it defines `type Post struct{ ID, AuthorID int64; Title string }`, a `postTable` (`Ident` `"posts"`), and the columns `pID`, `pAuthorID` (`NumberColumn[int64]`) and `pTitle` (`StringColumn[string]`).

- [ ] **Step 1: Write the failing tests.**
  ```go
  func TestSelectToSQL(t *testing.T) {
      q := atla.NewSelect[User](nil, userTable).Where(uAge.GTE(20)).OrderBy(uID.Desc()).Limit(10).Offset(5)
      // `SELECT "users"."id", "users"."name", "users"."age", "users"."bio" FROM "users" WHERE "users"."age" >= $1 ORDER BY "users"."id" DESC LIMIT 10 OFFSET 5`, args [20]
      // Where called twice ANDs: .Where(a).Where(b) -> `WHERE ("users"."age" >= $1 AND "users"."name" = $2)`
      // ForUpdate(atla.SkipLocked()) -> suffix ` FOR UPDATE SKIP LOCKED`
  }
  func TestSelectBranchingDoesNotAlias(t *testing.T) {
      base := atla.NewSelect[User](nil, userTable).Where(uAge.GTE(1)).Where(uAge.LTE(99)) // force spare capacity
      a := base.Where(uName.EQ("a")); b := base.Where(uName.EQ("b"))
      // base SQL has 2 predicates; a's args end with "a", b's with "b"; neither contains the other's
  }
  func TestSelectSubqueryPlaceholderNumbering(t *testing.T) {
      sub := atla.NewSelect[Post](nil, postTable).Where(pAuthorID.EQExpr(uID), pTitle.EQ("x")).Subquery()
      q := atla.NewSelect[User](nil, userTable).Where(uAge.GT(1), atla.Exists(sub), uName.EQ("n"))
      // WHERE ("users"."age" > $1 AND EXISTS (SELECT … FROM "posts" WHERE ("posts"."author_id" = "users"."id" AND "posts"."title" = $2)) AND "users"."name" = $3)
  }
  func TestSelectAliasFromItem(t *testing.T) { /* Table with AliasName `"m"` -> `FROM "users" AS "m"` */ }
  func TestSelectSQLiteOffsetOnly(t *testing.T) { /* SQLite fixture, Offset(5) -> ` LIMIT -1 OFFSET 5` */ }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test .` Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** Assemble the statement as `"SELECT " + SelectList + " FROM " + fromItem`, then the WHERE clause (`And(where...)`), ORDER BY (comma-joined), `AppendLimitOffset`, and `AppendLock`. A subquery renders the same SELECT inside parentheses, sharing the outer compiler, so placeholder numbering continues.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test .` Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add Table contract and Select builder"`

### Task 8: `Select[R]` terminal operations

**Files:**
- Modify: `select.go`
- Test: `select_exec_test.go` (package `atla_test`, uses `internal/fakedb`)

**Interfaces:**
- Consumes: Task 6 (`fakedb`, `wrapDBError`, `withQueryInfo`) and Task 7.
- Produces:
  ```go
  func (q Select[R]) All(ctx context.Context) ([]*R, error)
  func (q Select[R]) First(ctx context.Context) (*R, error)   // LIMIT 1 replaces any limit; ErrNotFound
  func (q Select[R]) One(ctx context.Context) (*R, error)     // LIMIT 2; ErrNotFound / ErrMultipleResults
  func (q Select[R]) Count(ctx context.Context) (int64, error) // Plan Decision 9
  func (q Select[R]) Exist(ctx context.Context) (bool, error)  // `SELECT EXISTS (SELECT 1 FROM … WHERE …)`
  func (q Select[R]) Iter(ctx context.Context) iter.Seq2[*R, error]
  ```
  Every call attaches `QueryInfo{Op: "select", Table: t.RawName, ReadOnly: lock == nil}`. Errors from `QueryContext`, `rows.Err`, and the scanner go through `wrapDBError(t.Dialect, "select", t.RawName, err)`.

- [ ] **Step 1: Write the failing tests.**
  ```go
  func TestAllScansRows(t *testing.T) {
      // expect the TestSelectToSQL SQL (without order/limit) with arg 20, two rows, one with bio NULL
      // -> 2 users; users[0].Age is int32(30); users[1].Bio == nil; Done(); OpenRows()==0
  }
  func TestFirstAndOne(t *testing.T) {
      // First: SQL ends ` LIMIT 1`; zero rows -> errors.Is(err, atla.ErrNotFound)
      // One:   SQL ends ` LIMIT 2`; 2 rows -> ErrMultipleResults; 0 rows -> ErrNotFound; 1 row -> ok
  }
  func TestCountAndExist(t *testing.T) {
      // Count: `SELECT COUNT(*) FROM "users" WHERE "users"."age" >= $1` (OrderBy dropped) -> 3
      // Count with Limit(10): `SELECT COUNT(*) FROM (SELECT 1 FROM "users" WHERE … LIMIT 10) AS "atla_count"`
      // Exist: `SELECT EXISTS (SELECT 1 FROM "users" WHERE "users"."age" >= $1)` row {true} -> true
  }
  func TestIterBreakClosesRows(t *testing.T) {
      // 3 rows; range over q.Iter(ctx), break after first; s.OpenRows()==0 and log contains "ROWS CLOSED"
  }
  func TestIterYieldsErrorOnce(t *testing.T) { /* query error -> exactly one (nil, err) pair */ }
  func TestQueryInfoAttached(t *testing.T) {
      // a capturing Executor wrapper records QueryInfoFrom(ctx): {Op:"select", Table:"users", ReadOnly:true};
      // with ForUpdate() -> ReadOnly false
  }
  func TestSelectErrorClassified(t *testing.T) { /* fakedb Err(pgUniqueErr) -> errors.Is(err, atla.ErrUniqueViolation) */ }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test .` Expected: FAIL.
- [ ] **Step 3: Implement.** `rows.Close` is deferred on every path. `Iter` closes the rows on early return because the `yield` result is checked.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test -race .` Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add Select terminal operations"`

### Task 9: Transactions (`RunTx`, `TxExecutor`, `TxRunner`)

**Files:**
- Create: `tx.go`
- Test: `tx_test.go` (package `atla_test`)

**Interfaces:**
- Consumes: Task 6 (`Executor`, `TxBeginner`, `fakedb`).
- Produces (`runtime-spec.md` §5.2):
  ```go
  type TxExecutor struct{ /* tx *sql.Tx; depth int */ }
  func (t *TxExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
  func (t *TxExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
  func (t *TxExecutor) Tx() *sql.Tx
  type TxRunner interface{ RunTx(ctx context.Context, fn func(Executor) error, opts ...TxOption) error }
  type TxOption func(*sql.TxOptions)
  func WithIsolation(l sql.IsolationLevel) TxOption
  func ReadOnly() TxOption
  func RunTx(ctx context.Context, ex Executor, fn func(Executor) error, opts ...TxOption) error
  ```

- [ ] **Step 1: Write the failing tests** (the five cases in the spec's order).
  ```go
  func TestRunTxCommit(t *testing.T)   { /* *sql.DB; fn returns nil -> log [BEGIN, COMMIT]; fn receives *atla.TxExecutor */ }
  func TestRunTxRollbackOnError(t *testing.T) { /* fn returns errX -> [BEGIN, ROLLBACK]; errors.Is(err, errX) */ }
  func TestRunTxRollbackOnPanic(t *testing.T) { /* fn panics "boom" -> [BEGIN, ROLLBACK]; recover() == "boom" */ }
  func TestRunTxOptions(t *testing.T) { /* WithIsolation(sql.LevelSerializable), ReadOnly() -> "BEGIN iso=6 ro=true" */ }
  func TestRunTxNestedSavepoint(t *testing.T) {
      // outer ok, inner returns errX -> EXEC SAVEPOINT atla_sp_1, EXEC ROLLBACK TO SAVEPOINT atla_sp_1, COMMIT
      // inner ok -> EXEC SAVEPOINT atla_sp_1, EXEC RELEASE SAVEPOINT atla_sp_1
      // doubly nested -> atla_sp_2
      // nested with WithIsolation -> errors.Is(err, atla.ErrTxOptionsNested), no SAVEPOINT executed
  }
  func TestRunTxRawSQLTx(t *testing.T) { /* user passes *sql.Tx -> SAVEPOINT atla_sp_1 used; atla never COMMITs */ }
  func TestRunTxDelegatesToTxRunner(t *testing.T) { /* fake TxRunner records the call; RunTx returns its error */ }
  func TestRunTxUnsupported(t *testing.T) { /* plain Executor stub -> errors.Is(err, atla.ErrTxUnsupported) */ }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test -run RunTx .` Expected: FAIL.
- [ ] **Step 3: Implement.** Check the cases in the spec order: `TxRunner`, `*TxExecutor`, `*sql.Tx`, `TxBeginner`, then everything else. Join a rollback error with `fn`'s error using `errors.Join`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test -race .` Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add transactions with savepoint nesting"`

### Task 10: `Opt[T]` and `Insert[R]`

**Files:**
- Create: `opt.go` (replacing the Task 5 stub), `mutation.go`
- Test: `opt_test.go`, `insert_test.go` (package `atla_test`)

**Interfaces:**
- Consumes: Tasks 6–8.
- Produces:
  ```go
  type Opt[T any] struct{ /* state uint8; v T; e Expr[T] */ }
  func Some[T any](v T) Opt[T]; func SomePtr[T any](v T) Opt[*T]; func Null[T any]() Opt[*T]
  func SetExpr[T any](e Expr[T]) Opt[T]; func SetExprPtr[T any](e Expr[T]) Opt[*T]
  func (o Opt[T]) IsSet() bool; func (o Opt[T]) Get() (T, bool)
  func (o Opt[T]) AppendTo(cols []string, vals []any, col string) ([]string, []any)
  type Insert[R any] struct{ /* ex; t; cols []string; vals []any */ }
  func NewInsert[R any](ex Executor, t Table[R], cols []string, vals []any) Insert[R]
  func (q Insert[R]) ToSQL() (string, []any, error)
  func (q Insert[R]) Exec(ctx context.Context) (int64, error)
  func (q Insert[R]) Save(ctx context.Context) (*R, error)
  ```

- [ ] **Step 1: Write the failing tests.**
  ```go
  func TestOpt(t *testing.T) {
      // zero Opt: IsSet false, AppendTo leaves cols/vals unchanged
      // Some(int64(3)).Get() == (3, true); SomePtr("x") value *string pointing to "x"
      // Null[string]() IsSet true, Get() == ((*string)(nil), true)
      // SetExpr(uAge.Add(1)).AppendTo(nil, nil, `"age"`) -> vals[0] is an atla.Expr[int32]
  }
  func TestInsertSQL(t *testing.T) {
      cols, vals := []string{`"name"`, `"age"`}, []any{"alice", int32(30)}
      cols, vals = atla.SomePtr("hi").AppendTo(cols, vals, `"bio"`)
      // ToSQL -> `INSERT INTO "users" ("name", "age", "bio") VALUES ($1, $2, $3)`, args ["alice", 30, *"hi"]
      // no cols -> `INSERT INTO "users" DEFAULT VALUES`
      // an expression value renders inline: SetExpr(atla.Raw[int32]("1 + ?", 1)) -> `VALUES (1 + $1)`
  }
  func TestInsertExecAndSave(t *testing.T) {
      // Exec: fakedb ExpectExec(sql, "alice", int32→int64 30, "hi").Result(1) -> 1, nil
      // Null[string]() argument arrives at the driver as nil
      // Save: `… RETURNING "id", "name", "age", "bio"` via QueryContext; one row -> *User with ID set
      // QueryInfo Op "insert"; a classified driver error -> ErrUniqueViolation
  }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test .` Expected: FAIL.
- [ ] **Step 3: Implement.** `Save` returns `ErrReturningUnsupported` when `!t.Dialect.Returning()`. It reads exactly one row with `t.Scan`.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test -race .` Expected: PASS. Also run `go test -run TestCompileFail .`, which now uses the real `Opt`.
- [ ] **Step 5: Commit.** `git commit -m "feat: add Opt and Insert"`

### Task 11: `Update[R]`, `Delete[R]`, unsafe-mutation guard

**Files:**
- Modify: `mutation.go`
- Test: `update_delete_test.go` (package `atla_test`)

**Interfaces:**
- Consumes: Task 10.
- Produces:
  ```go
  func NewUpdate[R any](ex Executor, t Table[R], cols []string, vals []any) Update[R]
  func (q Update[R]) Where(ps ...Pred) Update[R]; func (q Update[R]) AllowFullTable() Update[R]
  func (q Update[R]) ToSQL() (string, []any, error)
  func (q Update[R]) Exec(ctx context.Context) (int64, error)
  func (q Update[R]) Save(ctx context.Context) ([]*R, error)  // RETURNING
  func NewDelete[R any](ex Executor, t Table[R]) Delete[R]
  func (q Delete[R]) Where(ps ...Pred) Delete[R]; func (q Delete[R]) AllowFullTable() Delete[R]
  func (q Delete[R]) ToSQL() (string, []any, error)
  func (q Delete[R]) Exec(ctx context.Context) (int64, error)
  ```

- [ ] **Step 1: Write the failing tests.**
  ```go
  func TestUpdateSQL(t *testing.T) {
      cols, vals := atla.Some("bob").AppendTo(nil, nil, `"name"`)
      cols, vals = atla.SetExpr(uAge.Add(1)).AppendTo(cols, vals, `"age"`)
      // .Where(uID.EQ(7)) -> `UPDATE "users" SET "name" = $1, "age" = ("users"."age" + $2) WHERE "users"."id" = $3`, args ["bob", 1, 7]
      // Save -> same + ` RETURNING "id", "name", "age", "bio"`
  }
  func TestUpdateGuards(t *testing.T) {
      // no cols -> ToSQL/Exec errors.Is ErrEmptyUpdate (checked before the WHERE guard)
      // no Where -> ToSQL/Exec/Save errors.Is ErrUnsafeMutation, and fakedb log is empty (nothing sent)
      // Where() with zero preds counts as no WHERE
      // AllowFullTable() -> `UPDATE "users" SET "name" = $1`
  }
  func TestDelete(t *testing.T) {
      // Where(uID.EQ(7)) -> `DELETE FROM "users" WHERE "users"."id" = $1`; Exec returns RowsAffected
      // no Where -> ErrUnsafeMutation; AllowFullTable -> `DELETE FROM "users"`
      // QueryInfo Op "update" / "delete"
  }
  ```
- [ ] **Step 2: Run the tests and confirm they fail.** Run: `go test .` Expected: FAIL.
- [ ] **Step 3: Implement.** `Where` clones its slice (Global Constraints). The guards run before compiling.
- [ ] **Step 4: Run the tests and confirm they pass.** Run: `go test -race ./...` Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "feat: add Update and Delete with unsafe-mutation guard"`

### Task 12: Write plan decisions back into the spec; final verification

**Files:**
- Modify: `docs/runtime-spec.md` (§3.1 Dialect interface, §6.1 Table fields and constructors, §4 `AppendTo`, §2.4 `Raw` `??`, §7.4 guard covers `ToSQL`, §10 `ErrTxOptionsNested`)
- Modify: `docs/roadmap.md` (P0: the Postgres service container moves to P5)

- [ ] **Step 1: Update the spec sections** listed above so they match Plan Decisions 1–10 and the signatures implemented in Tasks 2–11. Wording must state behaviour, not history.
- [ ] **Step 2: Run the full verification.**
  Run: `gofmt -l . && go vet ./... && go test -race ./... && bash scripts/check-no-deps.sh && golangci-lint run`
  Expected: no gofmt output, every command exits 0, and `TestCompileFail` passes.
- [ ] **Step 3: Commit.** `git commit -m "docs: sync runtime spec with P1 implementation decisions"`
