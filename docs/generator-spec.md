# `atlagen` Code Generator Specification

This document specifies `atlagen`, the Ahead-of-Time code generator that reads
Atlas schema JSON and emits typed Go models, column descriptors, query builders, and
a client. It also specifies `atlavet`, the static analyzer that reports missing
required `INSERT` columns.

Both live in the tooling module `github.com/hirokazumiyaji/atla-go/atlagen`, which
is separate from the zero-dependency runtime module.

---

## 1. CLI & Configuration (`cmd/atlagen`, `config/`, `inspect/`)

### 1.1 Installation

```bash
# Go 1.24+ tool directive (recommended): pins the generator version in go.mod
go get -tool github.com/hirokazumiyaji/atla-go/atlagen/cmd/atlagen
go get -tool github.com/hirokazumiyaji/atla-go/atlagen/cmd/atlavet
```

```go
// internal/dbgen/generate.go (handwritten, not generated)
package dbgen

//go:generate go tool atlagen generate --config ../../atlagen.yaml
```

### 1.2 Commands

```bash
# 1. stdin (default when --schema is omitted or "-")
atlas schema inspect -u "postgres://localhost:5432/app?sslmode=disable" --format '{{ json . }}' \
  | atlagen generate --dialect postgres --out ./internal/dbgen

# 2. saved Atlas JSON
atlagen generate --schema ./schema.inspect.json --dialect sqlite --out ./internal/dbgen

# 3. atlagen runs `atlas schema inspect` itself
atlagen generate --schema file://schema.hcl --dev-url docker://postgres/16/dev \
  --dialect postgres --out ./internal/dbgen

# 4. CI: exit status 1 and a unified diff when the generated files are stale
atlagen check --config atlagen.yaml

atlagen version
```

### 1.3 Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--config` | `./atlagen.yaml` if present | Config file. Flags override config values. |
| `--schema`, `-s` | `-` | `-` (stdin), path to Atlas JSON, or an Atlas URL (`file://`, `postgres://`, `sqlite://`, …). For a URL, `atlas` must be on `PATH`. |
| `--dev-url` | — | Passed to `atlas schema inspect --dev-url`. |
| `--dialect` | required | `postgres`, `sqlite` (later: `mysql`, `tidb`). |
| `--schema-name` | first schema | Schema to generate when the JSON contains several. |
| `--out`, `-o` | required | Output directory. |
| `--package` | base name of `--out` | Go package name of the root generated package. |
| `--nullable` | `pointer` | Model field style for nullable columns: `pointer` (`*T`) or `sqlnull` (`sql.Null[T]`). |
| `--json-tags` | `true` | Emit `json:"<column_name>"` tags on model fields. |

The output package's **import path** is derived by walking up from `--out` to the
nearest `go.mod` and joining the module path with the relative directory. Generated
table subpackages import `<import path>/enum`.

### 1.4 `atlagen.yaml`

```yaml
version: 1
schema: file://db/schema.hcl
dev_url: docker://postgres/16/dev
dialect: postgres
schema_name: public
out: internal/dbgen
package: dbgen
nullable: pointer          # pointer | sqlnull
json_tags: true

types:                     # per database type
  - db_type: uuid
    go_type: github.com/google/uuid.UUID
  - db_type: numeric
    go_type: github.com/shopspring/decimal.Decimal

columns:                   # per column (wins over `types`)
  - column: users.settings
    go_type: example.com/app/settings.Settings   # must implement sql.Scanner + driver.Valuer

names:                     # identifier overrides
  tables:
    people: Person         # model name when singularization guesses wrong
  edges:
    - fk: posts_author_id_fkey   # FK constraint symbol from Atlas
      name: Author                # edge on the child (M2O)
      inverse: AuthoredPosts      # edge on the parent (O2M / O2O)
```

The YAML parser is `go.yaml.in/yaml/v3`, the maintained fork of `gopkg.in/yaml.v3`.
It is a dependency of the tooling module only.

---

## 2. Internal Representation (`ir/`)

```go
package ir

type Schema struct {
    Name    string
    Dialect string
    Enums   []Enum
    Tables  []Table   // sorted by Name for deterministic output
}

type Enum struct {
    DBName string     // "user_role", or "<table>_<column>" for MySQL inline enums
    GoName string     // "UserRole"
    Values []EnumValue
}

type EnumValue struct {
    DB string         // "admin"
    Go string         // "UserRoleAdmin"
}

type Table struct {
    DBName      string    // "users"
    Model       string    // "User"
    Package     string    // "user" (column subpackage)
    Columns     []Column  // Atlas order
    PrimaryKey  []string  // column DB names, may be empty or composite
    Uniques     [][]string
    ForeignKeys []ForeignKey
    Edges       []Edge    // derived (§5)
}

type Column struct {
    DBName      string    // "created_at"
    GoName      string    // "CreatedAt"
    RawType     string    // "timestamp with time zone"
    GoType      GoType    // resolved by mapper
    ColumnKind  string    // "Column" | "NumberColumn" | "StringColumn", prefixed with "Null" when Nullable
    Nullable    bool
    HasDefault  bool      // DEFAULT, identity, serial, AUTO_INCREMENT, rowid alias
    InPK        bool
    Scan        ScanPlan  // how the generated scanner reads this column (§4.4)
}

type GoType struct {
    Name       string     // "time.Time", "int64", "enum.UserRole", "uuid.UUID"
    ImportPath string     // "time", "", "<pkg>/enum", "github.com/google/uuid"
}

type ForeignKey struct {
    Symbol     string
    Columns    []string
    RefTable   string
    RefColumns []string
}

type Edge struct {
    Name     string       // "Posts", "Author"
    Kind     EdgeKind     // O2M | M2O | O2O
    FK       ForeignKey
    Target   string       // target table DB name
    Inverse  string       // name of the edge on the other side
}
```

**Required for insert** = `!Nullable && !HasDefault`. The parser sets `HasDefault`
when Atlas reports `default`, `identity`, `auto_increment`, a `serial`-family type,
or (SQLite) an `INTEGER PRIMARY KEY` rowid alias.

---

## 3. Naming Rules (`naming/`)

| Input | Rule | Example |
| :--- | :--- | :--- |
| Table → model | singularize, then PascalCase with Go initialisms | `users` → `User`, `user_profiles` → `UserProfile`, `api_keys` → `APIKey` |
| Table → column package | lowercase model name, no underscores | `UserProfile` → `userprofile` |
| Column → field | PascalCase with initialisms | `user_id` → `UserID`, `avatar_url` → `AvatarURL` |
| Composite PK → key type | `<Model>Key` struct | `MembershipKey{OrgID, UserID}` |
| Enum → type | PascalCase of the DB name | `user_role` → `enum.UserRole`, values `enum.UserRoleAdmin` |

Initialisms follow the Go list `ACL API ASCII CPU CSS DNS EOF GUID HTML HTTP HTTPS ID IP
JSON LHS QPS RAM RHS RPC SLA SMTP SQL SSH TCP TLS TTL UDP UI UID UUID URI URL UTF8 VM
XML XMPP XSRF XSS`.

Collisions:

- A package name that is a Go keyword or predeclared identifier, or that equals
  `atla`, `enum`, `sql`, `time`, or the root package name, gets the suffix `tbl`
  (`type` → `typetbl`).
- A field named `Edges` gets the suffix `Col` (`EdgesCol`), since `Edges` is
  reserved for eager-loaded relations.
- Two tables that map to the same model name are a generation error. The error
  suggests a `names.tables` override.

The name of a generated variable such as `user` (package) can collide with a local
variable named `user`. This is the same trade-off ent makes, and it is documented
rather than worked around.

---

## 4. Type Mapping (`mapper/`)

The *Column kind* columns below are for NOT NULL columns. A nullable column uses the
`Null` variant of the same kind (`NullNumberColumn[int32]`, `NullStringColumn[string]`,
`NullColumn[time.Time]`), see `runtime-spec.md` §2.2.

Type strings are normalized before lookup: lowercased, length/precision removed,
`unsigned` recorded separately, and time-zone clauses folded. The mapping assumes the
recommended drivers (`pgx/v5/stdlib` and `modernc.org/sqlite`). Other drivers may
need `types:` overrides.

### 4.1 PostgreSQL

| SQL type | Go type | Column kind |
| :--- | :--- | :--- |
| `smallint`, `int2`, `smallserial` | `int16` | `NumberColumn[int16]` |
| `integer`, `int`, `int4`, `serial` | `int32` | `NumberColumn[int32]` |
| `bigint`, `int8`, `bigserial` | `int64` | `NumberColumn[int64]` |
| `real`, `float4` | `float32` | `NumberColumn[float32]` |
| `double precision`, `float8` | `float64` | `NumberColumn[float64]` |
| `numeric`, `decimal` | `string` (override recommended) | `Column[string]` |
| `money` | `string` | `Column[string]` |
| `boolean` | `bool` | `Column[bool]` |
| `text`, `varchar`, `char`, `citext` | `string` | `StringColumn[string]` |
| `inet`, `cidr`, `macaddr` | `string` | `StringColumn[string]` |
| `bytea` | `[]byte` | `Column[[]byte]` |
| `uuid` | `string` (override to `uuid.UUID` recommended) | `Column[string]` |
| `date`, `timestamp`, `timestamptz` | `time.Time` | `Column[time.Time]` |
| `time`, `timetz`, `interval` | `string` | `Column[string]` |
| `json`, `jsonb` | `json.RawMessage` | `Column[json.RawMessage]` |
| user enum | `enum.<Name>` | `StringColumn[enum.<Name>]` |
| `<type>[]` | **v1: generation error unless overridden** | — |

Arrays are excluded from v1 because `database/sql` exposes them only as text through
pgx/stdlib. A correct mapping needs `pgtype` scanners, which would make generated
code depend on pgx. A later phase may add an opt-in `pgx` codec package.

### 4.2 SQLite

| Declared type (affinity-normalized) | Go type | Column kind |
| :--- | :--- | :--- |
| `integer`, `int`, `bigint`, `smallint`, `tinyint` | `int64` | `NumberColumn[int64]` |
| `real`, `double`, `float` | `float64` | `NumberColumn[float64]` |
| `numeric`, `decimal` | `string` | `Column[string]` |
| `boolean`, `bool` | `bool` | `Column[bool]` |
| `text`, `varchar`, `char`, `clob` | `string` | `StringColumn[string]` |
| `blob` | `[]byte` | `Column[[]byte]` |
| `date`, `datetime`, `timestamp` | `time.Time` | `Column[time.Time]` |
| `uuid` | `string` | `Column[string]` |
| `json` | `json.RawMessage` | `Column[json.RawMessage]` |
| anything else | `string`, with a generation warning | `Column[string]` |

### 4.3 MySQL / TiDB (later phase)

The mapping follows atla's table: `tinyint(1)`/`bool` → `bool`; integer types →
sized ints, with `unsigned` → `uintN`; `decimal` → `string`; text types → `string`;
blob types → `[]byte`; `datetime`/`timestamp`/`date` → `time.Time` (requires
`parseTime=true`, which the generator documents and the runtime checks on first
use); `json` → `json.RawMessage`; inline `enum('a','b')` → `enum.<Table><Column>`.

### 4.4 Nullability, Write Types, and Scan Plans

| Column | Model field (`--nullable=pointer`) | Model field (`sqlnull`) | `XxxInsert` field | `XxxUpdate` field |
| :--- | :--- | :--- | :--- | :--- |
| NOT NULL, no default | `T` | `T` | `T` + tag `atla:"required"` | `atla.Opt[T]` |
| NOT NULL, has default | `T` | `T` | `atla.Opt[T]` | `atla.Opt[T]` |
| nullable | `*T` | `sql.Null[T]` | `atla.Opt[*T]` | `atla.Opt[*T]` |
| PK column | as above | as above | as above | *(not present)* |

The **scan plan** is the Go counterpart of atla's `row_codec`. It is chosen per
column so that the generated scanner avoids slow `database/sql` conversion paths:

| Plan | When | Generated code |
| :--- | :--- | :--- |
| `direct` | Go type matches the driver value (`int64`, `float64`, `string`, `[]byte`, `bool`, `time.Time`, `sql.Scanner` types such as enums and overrides) | `&u.Field` |
| `widen` | `int16`/`int32`/`float32` (`database/sql` would otherwise go through `strconv`) | scan into an `int64`/`float64` local, then convert |
| `nullptr` | nullable with `--nullable=pointer` | scan into `sql.Null[T]` local; `if n.Valid { v := n.V; u.Field = &v }` (allocates only for non-NULL) |
| `nullwiden` | combination of the two above | `sql.Null[int64]` local, then convert and take the address |

Generated enum types implement `sql.Scanner` and `driver.Valuer`. The scanner
rejects values outside the enum with an error, so enum columns take the `direct`
plan without reflection.

---

## 5. Edge Derivation

Each foreign key in the selected schema produces one pair of edges:

| Situation | Child edge (on table with FK) | Parent edge (on referenced table) |
| :--- | :--- | :--- |
| FK columns not unique | M2O | O2M |
| FK columns exactly covered by a UNIQUE index or the PK | M2O | O2O |

Default names:

1. **M2O name**: if the FK is a single column ending in `_id`, strip the suffix and
   PascalCase the rest (`author_id` → `Author`). Otherwise use the referenced model
   name (`User`). Composite FKs always use the referenced model name.
2. **O2M name**: the pluralized child model name (`Posts`). **O2O name**: the child
   model name (`Profile`).
3. **Disambiguation**: if a parent would get two edges with the same name (several
   FKs from the same child table, or a self-reference), every such parent edge is
   prefixed with its M2O name (`AuthorPosts`, `EditorPosts`;
   `users.manager_id` → `Manager` / `ManagerUsers`).
4. Overrides in `names.edges` (keyed by FK symbol) win over all rules.
5. Any remaining collision is a generation error that names the FK symbol to
   override.

Composite FKs (`(org_id, user_id) REFERENCES memberships (org_id, user_id)`) are
supported. The generator emits a comparable key struct for grouping and uses
row-value `IN` in the loader.

Cross-schema FKs are skipped with a warning in v1. Many-to-many edges through join
tables are a later phase (see `runtime-spec.md` §11).

---

## 6. `atlavet` Analyzer (`atlavet/`, `cmd/atlavet`)

`atlavet` is a `golang.org/x/tools/go/analysis` analyzer. It is generic: it does
not need to know the generated package path, because it keys off the struct tag that
`atlagen` emits.

Rules:

1. **Missing required fields.** A composite literal of a struct type with at least
   one field tagged `atla:"required"` must set every such field by key.
   `dbgen.UserInsert literal is missing required field(s): Name, Age`.
2. **Unkeyed literals.** Literals of such types must be keyed (positional literals
   would break silently when the schema changes).
3. **Zero-value construction.** `var x dbgen.UserInsert` and `new(dbgen.UserInsert)`
   are reported, because required fields cannot be tracked through later
   assignments. Code that intentionally fills such a value field by field suppresses
   the report with a trailing `//atlavet:ignore` comment.

Usage:

```bash
go tool atlavet ./...
# or: go vet -vettool=$(go tool -n atlavet) ./...
```

A golangci-lint module plugin is a later-phase convenience.

---

## 7. Emitted Code

Every generated file starts with `// Code generated by atlagen. DO NOT EDIT.`, the
form the Go toolchain and linters recognize. Each file also contains
`const _ = atla.GenVersionN`, the runtime compatibility guard
(`runtime-spec.md` §12). Output is passed through `go/format`,
and generation is byte-for-byte deterministic.

```text
internal/dbgen/
├── client.go        # Client, NewClient, Tx, repository fields
├── enum/enum.go     # package enum: enum types, constants, Values(), IsValid(), Scan, Value
├── user.go          # User, UserEdges, UserInsert, UserUpdate, UserQuery, UserRepo, UserTable, scan funcs, SQL consts
├── user/user.go     # package user: Table, Columns, As(), column variables
├── post.go
└── post/post.go
```

**Import direction**: the root package imports `enum` but does **not** import table
subpackages. Table subpackages import only `atla`, `atla/dialect`, and `enum`. This keeps the graph
acyclic even when Postgres enums are shared across tables. Inside the root package,
edge loaders and repositories reference columns through quoted-name constants.

The examples below use this schema (Postgres):

```sql
CREATE TYPE user_role AS ENUM ('admin', 'member', 'guest');
CREATE TABLE users (
  id bigserial PRIMARY KEY,
  email varchar(255) NOT NULL UNIQUE,
  name text NOT NULL,
  age integer NOT NULL,
  role user_role NOT NULL DEFAULT 'member',
  bio text,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE posts (
  id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  author_id bigint NOT NULL REFERENCES users (id),
  title text NOT NULL,
  published boolean NOT NULL DEFAULT false
);
```

### 7.1 `user/user.go`

```go
// Code generated by atlagen. DO NOT EDIT.

package user

import (
    "time"

    "github.com/hirokazumiyaji/atla-go"
    "github.com/hirokazumiyaji/atla-go/dialect"
    "example.com/app/internal/dbgen/enum"
)

// Table is the quoted table name.
const Table = `"users"`

// Columns holds column descriptors bound to a table name or alias.
type Columns struct {
    ID        atla.NumberColumn[int64]
    Email     atla.StringColumn[string]
    Name      atla.StringColumn[string]
    Age       atla.NumberColumn[int32]
    Role      atla.StringColumn[enum.UserRole]
    Bio       atla.NullStringColumn[string]
    DeletedAt atla.NullColumn[time.Time]
    CreatedAt atla.Column[time.Time]
}

// As returns column descriptors bound to the SQL alias, for self-joins.
func As(alias string) Columns { return columns(string(dialect.Postgres.AppendIdent(nil, alias))) }

func columns(t string) Columns {
    return Columns{
        ID:        atla.NewNumberColumn[int64](t, `"id"`),
        Email:     atla.NewStringColumn[string](t, `"email"`),
        Name:      atla.NewStringColumn[string](t, `"name"`),
        Age:       atla.NewNumberColumn[int32](t, `"age"`),
        Role:      atla.NewStringColumn[enum.UserRole](t, `"role"`),
        Bio:       atla.NewNullStringColumn[string](t, `"bio"`),
        DeletedAt: atla.NewNullColumn[time.Time](t, `"deleted_at"`),
        CreatedAt: atla.NewColumn[time.Time](t, `"created_at"`),
    }
}

var c = columns(Table)

var (
    ID        = c.ID
    Email     = c.Email
    Name      = c.Name
    Age       = c.Age
    Role      = c.Role
    Bio       = c.Bio
    DeletedAt = c.DeletedAt
    CreatedAt = c.CreatedAt
)
```

### 7.2 `user.go` (excerpt)

```go
// Code generated by atlagen. DO NOT EDIT.

package dbgen

// User is a row of "users".
type User struct {
    ID        int64         `json:"id"`
    Email     string        `json:"email"`
    Name      string        `json:"name"`
    Age       int32         `json:"age"`
    Role      enum.UserRole `json:"role"`
    Bio       *string       `json:"bio"`
    DeletedAt *time.Time    `json:"deleted_at"`
    CreatedAt time.Time     `json:"created_at"`

    Edges UserEdges `json:"edges"`
}

type UserEdges struct {
    Posts  []*Post `json:"posts,omitempty"`
    loaded [1]bool
}

func (e UserEdges) PostsOrErr() ([]*Post, error) {
    if e.loaded[0] {
        return e.Posts, nil
    }
    return nil, atla.ErrNotLoaded
}

// UserInsert is the input of UserRepo.Insert. Fields tagged atla:"required" are checked by atlavet.
type UserInsert struct {
    ID        atla.Opt[int64]
    Email     string `atla:"required"`
    Name      string `atla:"required"`
    Age       int32  `atla:"required"`
    Role      atla.Opt[enum.UserRole]
    Bio       atla.Opt[*string]
    DeletedAt atla.Opt[*time.Time]
    CreatedAt atla.Opt[time.Time]
}

// UserUpdate is the input of UserRepo.Update. Unset fields are left unchanged.
type UserUpdate struct {
    Email     atla.Opt[string]
    Name      atla.Opt[string]
    Age       atla.Opt[int32]
    Role      atla.Opt[enum.UserRole]
    Bio       atla.Opt[*string]
    DeletedAt atla.Opt[*time.Time]
    CreatedAt atla.Opt[time.Time]
}

// Precomputed SQL (dialect: postgres).
const (
    userSelectList = `"users"."id", "users"."email", "users"."name", "users"."age", "users"."role", "users"."bio", "users"."deleted_at", "users"."created_at"`
    userGetSQL     = `SELECT ` + userSelectList + ` FROM "users" WHERE "users"."id" = $1 LIMIT 1`
    userGetManyPre = `SELECT ` + userSelectList + ` FROM "users" WHERE "users"."id" IN (`
    userDeletePKSQL = `DELETE FROM "users" WHERE "id" = $1`
)

// UserTable is built by a constructor function. Alias calls the constructor rather
// than referencing the UserTable variable, which would be an initialization cycle.
var UserTable = newUserTable(`"users"`)

func newUserTable(name string) atla.Table[User] {
    return atla.Table[User]{
        Name: name, SelectList: userSelectListFor(name), NumCols: 8,
        Scan:                  scanUser, // single-table fast path (column order is the same for any alias)
        NewRowScanner:         func() atla.RowScanner[User] { return new(userRowScanner) },
        NewNullableRowScanner: func() atla.RowScanner[User] { return new(userNullableRowScanner) },
        Alias:                 aliasUserTable,
    }
}

func aliasUserTable(alias string) atla.Table[User] {
    return newUserTable(string(Dialect.AppendIdent(nil, alias)))
}

func scanUser(rows *sql.Rows) (*User, error) {
    u := new(User)
    var age int64
    var bio sql.Null[string]
    var deletedAt sql.Null[time.Time]
    if err := rows.Scan(&u.ID, &u.Email, &u.Name, &age, &u.Role, &bio, &deletedAt, &u.CreatedAt); err != nil {
        return nil, err
    }
    u.Age = int32(age)
    if bio.Valid {
        v := bio.V
        u.Bio = &v
    }
    if deletedAt.Valid {
        v := deletedAt.V
        u.DeletedAt = &v
    }
    return u, nil
}

type UserRepo struct{ ex atla.Executor }

func (r *UserRepo) Query() UserQuery
func (r *UserRepo) Get(ctx context.Context, id int64) (*User, error)
func (r *UserRepo) GetMany(ctx context.Context, ids []int64) ([]*User, error)
func (r *UserRepo) DeleteByPK(ctx context.Context, id int64) error
func (r *UserRepo) Insert(in UserInsert) UserInsertQuery
func (r *UserRepo) Update(set UserUpdate) UserUpdateQuery
func (r *UserRepo) Delete() UserDeleteQuery

// UserQuery: see runtime-spec.md §6.2. Edge methods:
func (q UserQuery) WithPosts(fns ...func(PostQuery) PostQuery) UserQuery
```

`post.go` correspondingly has `Post.Edges.Author *User`,
`PostQuery.WithAuthor(...)`, and `AuthorOrErr()`.

### 7.3 `client.go`

```go
// Code generated by atlagen. DO NOT EDIT.

package dbgen

type Client struct {
    ex   atla.Executor
    User *UserRepo
    Post *PostRepo
}

func NewClient(ex atla.Executor) *Client {
    return &Client{ex: ex, User: &UserRepo{ex: ex}, Post: &PostRepo{ex: ex}}
}

// Tx runs fn in a transaction (or a SAVEPOINT when already inside one).
func (c *Client) Tx(ctx context.Context, fn func(tx *Client) error, opts ...atla.TxOption) error {
    return atla.RunTx(ctx, c.ex, func(ex atla.Executor) error { return fn(NewClient(ex)) }, opts...)
}

// Dialect is the dialect the code was generated for.
var Dialect = dialect.Postgres
```

`NewClient` takes no dialect argument. The precomputed SQL already embeds the
dialect, so a client cannot be paired with the wrong one.

### 7.4 `enum/enum.go`

```go
type UserRole string

const (
    UserRoleAdmin  UserRole = "admin"
    UserRoleMember UserRole = "member"
    UserRoleGuest  UserRole = "guest"
)

func UserRoleValues() []UserRole
func (v UserRole) IsValid() bool
func (v *UserRole) Scan(src any) error          // rejects unknown values
func (v UserRole) Value() (driver.Value, error) // rejects unknown values
```

---

## 8. Generation Behaviour

- **Stale files**: `generate` deletes files in `--out` (and in its generated
  subpackages) that carry the generated header but are no longer produced. It never
  touches files without the header. Handwritten files can therefore live next to
  generated ones, as in ent.
- **`check`**: renders in memory and compares with disk, including stale-file
  detection. It prints a unified diff and exits with status 1 on mismatch.
- **Tables without a PK**: no `Get` / `GetMany` / `DeleteByPK` / `NewNullableRowScanner`;
  everything else works.
- **Views**: ignored in v1 (my call). Read-only view models are a later candidate.
- **Validation errors** (generation fails with a precise message): unknown dialect,
  unmapped type without override, name collision, array column without override,
  and an override type that does not implement `sql.Scanner` and `driver.Valuer`
  (checked with `go/types` when `go_type` is outside the standard library).
