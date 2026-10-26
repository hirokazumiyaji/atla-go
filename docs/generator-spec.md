# `atlagen` Code Generator Specification

This document specifies `atlagen`, the Ahead-of-Time code generator that reads
the schema HCL printed by `atlas schema inspect` and emits typed Go models, column
descriptors, query builders, and a client. It also specifies `atlavet`, the static analyzer that reports missing
required `INSERT` columns.

Both live in the tooling module `github.com/hirokazumiyaji/atla-go/atlagen`, which
is separate from the zero-dependency runtime module. The generator has no public Go
API: its packages live under `atlagen/internal/`. `atlavet` stays outside `internal/`
so that a golangci-lint plugin can import it.

---

## 1. CLI & Configuration (`cmd/atlagen`, `internal/config`, `internal/inspect`)

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
atlas schema inspect -u "postgres://localhost:5432/app?sslmode=disable" \
  | atlagen generate --dialect postgres --out ./internal/dbgen

# 2. saved inspect output
atlas schema inspect -u "sqlite://app.db" > schema.inspect.hcl
atlagen generate --schema ./schema.inspect.hcl --dialect sqlite --out ./internal/dbgen

# 3. atlagen runs `atlas schema inspect -u <url>` itself
atlagen generate --schema file://schema.hcl --dev-url docker://postgres/16/dev \
  --dialect postgres --out ./internal/dbgen

# 4. CI: exit status 1 and a unified diff when the generated files are stale
atlagen check --config atlagen.yaml

# debugging: print the resolved IR (§2) as indented JSON
atlagen ir --config atlagen.yaml

atlagen version
```

| Command | Description |
| :--- | :--- |
| `ir` | Runs the front end (load, parse, name, map) with the flags of §1.3 and prints the resolved IR (§2) as indented JSON on stdout. Warnings go to stderr as `atlagen: warning: <msg>`. It writes no files. This is a permanent debugging command. |
| `version` | Prints `atlagen <module version>`, or `atlagen devel` for a build outside a released module. |
| `generate` | Writes the generated code (§7, §8). Arrives in P3 together with the emitters. |
| `check` | Compares the generated code with the files on disk (§8). Arrives in P3. |

**Input format.** `atlagen` reads the HCL that `atlas schema inspect -u <url>` prints
by default, without `--format`, and parses it with `github.com/hashicorp/hcl/v2`.
JSON input (anything whose first non-blank character is `{`) is rejected with an error
that points to the HCL command, because Atlas's JSON output omits defaults, identity
columns, Postgres enums and partial-index predicates.

**Exit status.** `0` on success. `2` on a usage error: no command, an unknown command,
an unknown flag or a flag that fails to parse, positional arguments, or `-h` (each
prints the usage text to stderr). `1` on every other error, including invalid option
values and every config, input, parse, naming and mapping error; the error is printed
to stderr as `atlagen: <err>`.

### 1.3 Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--config` | `./atlagen.yaml` if present | Config file. A flag given on the command line overrides the config value, even when the flag's value is empty. |
| `--schema`, `-s` | `-` | `-` (stdin), path to a file holding `atlas schema inspect` HCL, or an Atlas URL (anything containing `://`: `file://`, `postgres://`, `sqlite://`, …). For a URL, `atlagen` runs `atlas schema inspect -u <url>`, and `atlas` must be on `PATH`. |
| `--dev-url` | — | Passed to `atlas schema inspect --dev-url`. Requires `--schema` to be an Atlas URL; with stdin or a file it is an error. |
| `--dialect` | required | `postgres` or `sqlite`. `mysql` and `tidb` fail with `dialect "mysql" is not supported yet` (later phase); any other value is an unknown dialect. |
| `--schema-name` | the only schema, else `public`, else `main` | Schema to use when the input has several. When the input has several schemas and none is `public` or `main`, it is required. |
| `--out`, `-o` | required | Output directory. It must be inside a Go module (see below). |
| `--package` | base name of `--out` | Go package name of the root generated package: a lowercase identifier (`[a-z][a-z0-9_]*`) that is not a Go keyword. When the base name of `--out` is not such a name, `--package` is required. |
| `--nullable` | `pointer` | Model field style for nullable columns: `pointer` (`*T`) or `sqlnull` (`sql.Null[T]`). Any other value is an error. |
| `--json-tags` | `true` | Emit `json:"<column_name>"` tags on model fields. |

The output package's **import path** is derived by walking up from `--out` to the
nearest `go.mod` and joining the module path with the relative directory. `--out` need
not exist yet. No `go.mod` at or above it, or one that cannot be read, is an error.
Generated table subpackages import `<import path>/enum`.

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

- **Discovery.** The config file is `--config <file>` when given (relative to the
  current directory), else `atlagen.yaml` in the current directory when it exists,
  else none.
- **Validation.** Unknown keys are errors, `version: 1` is required, and every entry
  needs its required fields (`db_type` and `go_type`; `column` and `go_type`; `fk` and
  `name`). The override values are checked by the naming and mapping stages (§3, §4.5).
- **Relative paths.** Values from `atlagen.yaml` (a schema file path, a relative
  `file://` URL, a relative `sqlite://` path in `dev_url`, `out`) resolve against the
  config file's directory, and the `atlas` subprocess runs with that directory as its
  working directory. Values from flags resolve against the current working directory,
  which is then also the working directory of `atlas`. A relative SQLite path in a dev
  URL resolves against its own source, so `--config ../atlagen.yaml` with a
  command-line `--dev-url sqlite://dev.db` opens the database in the current directory.
- **`dev_url`** requires `schema` to be an Atlas URL, like `--dev-url`.
- **`names.tables`** keys must name a table of the selected schema; an unused key is an
  error. Values must be exported Go identifiers.
- **`names.edges`** entries are checked for shape only. Edges are derived in P4 (§5),
  which applies them.

---

## 2. Internal Representation (`internal/ir`)

```go
package ir

type Schema struct {
    Name    string
    Dialect string
    Enums   []Enum
    Tables  []Table      // sorted by DBName
}

type Enum struct {
    DBName string        // "user_role"
    GoName string        // "UserRole"
    Values []EnumValue   // Atlas order
}

type EnumValue struct {
    DB string            // "admin"
    Go string            // "UserRoleAdmin"
}

type Table struct {
    DBName      string       // "users"
    Model       string       // "User"
    Package     string       // "user" (column subpackage)
    Columns     []Column     // Atlas order
    PrimaryKey  []string     // column DB names; may be empty or composite
    Uniques     [][]string   // plain-column unique indexes, index order
    ForeignKeys []ForeignKey
}

type Column struct {
    DBName     string      // "created_at"
    GoName     string      // "CreatedAt"
    RawType    string      // "timestamptz", "character varying(255)"
    Enum       string      // DB name of the schema enum this column uses, or ""
    GoType     GoType      // resolved by the mapper
    Kind       ColumnKind  // "Column" | "NumberColumn" | "StringColumn", prefixed with "Null" when Nullable
    Nullable   bool
    HasDefault bool        // DEFAULT, identity, serial, AUTO_INCREMENT, rowid alias
    Generated  bool        // generated (computed) column; never written
    InPK       bool
    Scan       ScanPlan    // how the generated scanner reads this column (§4.4)
}

type GoType struct {
    Name       string      // "time.Time", "int64", "enum.UserRole", "uuid.UUID"
    ImportPath string      // "time", "", "<pkg>/enum", "github.com/google/uuid"
}

type ColumnKind string     // KindColumn, KindNumber, KindString, KindNullColumn, KindNullNumber, KindNullString
type ScanPlan string       // ScanDirect, ScanWiden, ScanNullPtr, ScanNullWiden, ScanTextBytes, ScanNullTextBytes

type ForeignKey struct {
    Symbol      string
    Columns     []string
    RefSchema   string     // "" for the selected schema, else the referenced table's schema
    RefTable    string
    RefColumns  []string
    RefExternal bool       // the referenced table is not in the inspected document
}

func (c Column) RequiredForInsert() bool       // !Nullable && !HasDefault && !Generated
func (t Table) Column(dbName string) (Column, bool)
```

Every field has a snake_case JSON tag (`db_name`, `go_type`, `has_default`,
`ref_external`, …), which is the format `atlagen ir` prints. Slices are never nil, so
they print as `[]`, not `null`. The same input always produces byte-identical IR JSON.

The IR has no edge type. Edges are derived from `ForeignKeys` in P4 (§5).

**Required for insert** = `!Nullable && !HasDefault && !Generated`. A generated column
can never be written, so P3 omits it from `XxxInsert` and `XxxUpdate`.

### 2.1 Parsing Atlas HCL (`internal/parser`)

The parser fills the database facts; the Go names and Go types are filled by the
naming (§3) and mapping (§4) stages.

- **Schema selection.** `--schema-name` selects a schema by name; a name the input does
  not hold is an error that lists the available schemas. Without a name, the parser
  picks the only schema, else `public`, else `main`. Any other set of schemas is an
  error that asks for `schema_name`. Only tables and enums of the selected schema enter
  the IR, and a selected schema without tables is an error.
- **Raw types** are the type as Atlas prints it. In an identifier, `_` is turned back
  into a space (`character_varying(255)` → `character varying(255)`); `sql("<t>")` gives
  `<t>` verbatim. Atlas prints SQLite types in lowercase (`integer`, `numeric(10,2)`),
  except for declared types it has no spec for, such as `sql("FOO")`, which keep their
  case. Postgres interval types that Atlas prints by their field alone (`day_to_second`)
  get the `interval` keyword back. A column of a schema enum (`enum.user_role`) gets
  `RawType` and `Enum` set to the enum name. A column of an enum from another schema
  gets `RawType` `<schema>.<name>` and no `Enum`, so it needs an override (§4.5).
- **Nullable** is `null = true`, except where the database guarantees NOT NULL:
  Postgres and MySQL primary keys, SQLite `WITHOUT ROWID` primary keys, and the
  SQLite `INTEGER PRIMARY KEY` rowid alias. An ordinary SQLite rowid table allows NULL
  in non-INTEGER and composite primary keys, and Atlas prints `null = true` for them.
- **HasDefault** is set when the column has a `default`, an `identity` block,
  `auto_increment = true`, a Postgres `serial`-family type (`serial`, `serial2`,
  `serial4`, `serial8`, `smallserial`, `bigserial`), or (SQLite) is the single-column
  `INTEGER PRIMARY KEY`, an alias of the rowid. A `WITHOUT ROWID` table (printed
  `without_rowid = true`) has no rowid, so its `INTEGER PRIMARY KEY` has no default.
  SQLite assigns no implicit defaults to the serial-family type names.
- **Generated** is set by an `as { expr = "…" }` block (what Atlas prints) or an
  `as = "…"` attribute.
- **Uniques** come from Postgres `unique "<name>" {}` blocks (Atlas prints UNIQUE
  constraints this way) and from `index` blocks with `unique = true`, in file order.
  Only indexes whose parts are all plain columns and that have no `where` predicate
  count. Expression and partial indexes are excluded, because they cannot prove an O2O
  edge.
- **Foreign keys** locate the referenced table with `RefSchema` and `RefExternal`:
  - `RefSchema ""`, `RefExternal false`: the table is in the selected schema.
  - `RefSchema "<s>"`: the table is in schema `<s>` of the inspected document (a
    realm-wide inspect). P4 skips such FKs with a warning.
  - `RefExternal true` (with `RefSchema ""`): the table is not in the inspected
    document. A single-schema inspect prints an FK into a schema that was not inspected
    (for example Supabase's `public.profiles` → `auth.users`) as an unqualified
    reference to a table that does not appear. P4 skips such FKs with a warning.

  Limitation: when the selected schema also has a table with the name of such an
  external target, the unqualified reference cannot be told apart from a same-schema
  reference, and it resolves to the selected schema. Inspect every schema involved (a
  realm-wide URL) to avoid this.
- **Order.** Tables are sorted by DB name. Columns, enum values, primary-key columns and
  FK columns keep Atlas order; uniques keep index order.
- **Errors** carry their context (`table "users": column "age": …`): HCL syntax errors
  (`invalid atlas hcl: schema.hcl:<line>:<col>: …`), duplicate tables, enums or columns,
  key or index parts naming unknown columns, unsupported type expressions, and unknown
  enums.

---

## 3. Naming Rules (`internal/naming`)

| Input | Rule | Example |
| :--- | :--- | :--- |
| Table → model | singularize the last word, then PascalCase with Go initialisms; `names.tables` wins | `users` → `User`, `user_profiles` → `UserProfile`, `api_keys` → `APIKey`, `people` → `Person`, `dns` → `DNS` |
| Table → column package | lowercase model name, no underscores | `UserProfile` → `userprofile` |
| Column → field | PascalCase with initialisms | `user_id` → `UserID`, `avatar_url` → `AvatarURL`, `userIDs` → `UserIDs` |
| Composite PK → key type | `<Model>Key` struct | `MembershipKey{OrgID, UserID}` |
| Enum → type | PascalCase of the DB name | `user_role` → `enum.UserRole` |
| Enum value → constant | `<Type>` + PascalCase of the value, without the `X` prefix | `admin` → `enum.UserRoleAdmin`, `1st` → `enum.UserRole1st` |

**PascalCase** splits a name on every rune that is not an ASCII letter or digit and on
camelCase boundaries (`userName` → `User`·`Name`, `HTTPStatus` → `HTTP`·`Status`), then
capitalizes each word. Initialisms are upper-cased, and so are their plurals
(`ids` → `IDs`, `urls` → `URLs`). A model, package, field or enum type name that starts
with a digit gets the prefix `X`: `2fa_codes` gives the model `X2faCode` and the package
`x2facode`. An enum value constant follows its type name, so it keeps a leading digit:
the value `1st` of `user_role` gives `UserRole1st`. An enum value whose PascalCase form
is empty (such as `""`) becomes `Empty` (`UserRoleEmpty`).

Initialisms follow the Go list `ACL API ASCII CPU CSS DNS EOF GUID HTML HTTP HTTPS ID IP
JSON LHS QPS RAM RHS RPC SLA SMTP SQL SSH TCP TLS TTL UDP UI UID UUID URI URL UTF8 VM
XML XMPP XSRF XSS`.

Singularization applies English suffix rules (`-ies` → `-y`, `-sses`/`-xes`/`-zes`/
`-ches`/`-shes` → drop `es`, otherwise drop a final `s` but not `ss`), with a list of
irregular plurals (`people`, `children`, `indices`, `movies`, `statuses`, …) and words
that stay unchanged (`series`, `news`, `data`, `media`, `status`, `analysis`, …). A last
word that is itself an initialism is not singularized (`dns` → `DNS`, `https` → `HTTPS`,
`user_dns` → `UserDNS`). When it guesses wrong, set the model with `names.tables`.

Renames (no error):

- A package name that is a Go keyword or predeclared identifier, one of `atla`,
  `dialect`, `enum`, `sql`, `driver`, `time`, `json`, `context` (packages that
  generated files import), `main`, `internal` or `vendor` (directory names the Go
  toolchain does not import as an ordinary package), or the root package name gets
  the suffix `tbl` (`type` → `typetbl`, `internal` → `internaltbl`).
- A field named `Edges`, `Table`, `Columns` or `As` gets the suffix `Col`
  (`EdgesCol`, `TableCol`, `ColumnsCol`, `AsCol`). `Edges` is reserved for
  eager-loaded relations, and each column package declares `const Table`,
  `type Columns` and `func As` (§7.1).

Generation errors (all of them are reported together, each naming the tables, columns
or enums involved):

- A table, column or enum name with no ASCII letters or digits, from which no
  identifier can be derived. For a table, the error suggests `names.tables`.
- Two tables that map to the same model name. The error suggests a `names.tables`
  override.
- A root-package identifier generated twice. The root package reserves `Client`,
  `NewClient` and `Dialect`, and each model reserves itself plus `<Model>Edges`,
  `<Model>Insert`, `<Model>Update`, `<Model>Query`, `<Model>Repo`, `<Model>Table`,
  `<Model>Key`, `<Model>InsertQuery`, `<Model>UpdateQuery` and `<Model>DeleteQuery`.
  For example, a table `user_queries` (model `UserQuery`) clashes with the query type
  of `users`, and a table `user_deletes` (model `UserDelete`) clashes with the delete
  query type of `users`.
- A model named `Tx`. Each model becomes a field of `Client`, which also has the
  `Tx` transaction method (§7.3), so the field and method would collide.
- Two tables with different models that map to the same package.
- Two columns of one table that map to the same field (`user_id` and `userId`).
- Two enums that map to the same type, or an identifier of the enum package generated
  twice: the enum types, their `<Type>Values` functions and the value constants (an
  enum value `values` of `user_role` clashes with `UserRoleValues`).
- A `names.tables` key that matches no table, or a value that is not an exported Go
  identifier.

The name of a generated variable such as `user` (package) can collide with a local
variable named `user`. This is the same trade-off ent makes, and it is documented
rather than worked around.

---

## 4. Type Mapping (`internal/mapper`)

The *Column kind* columns below are for NOT NULL columns. A nullable column uses the
`Null` variant of the same kind (`NullNumberColumn[int32]`, `NullStringColumn[string]`,
`NullColumn[time.Time]`), see `runtime-spec.md` §2.2.

Each column resolves in this order: a `columns:` override, a `types:` override, the
schema enum, then the dialect table (§4.5 for overrides).

Type strings are normalized before lookup: lowercased, whitespace collapsed,
length/precision arguments removed, time-zone clauses folded
(`timestamp with time zone` → `timestamptz`, `time without time zone` → `time`),
interval fields dropped (`interval hour` → `interval`), array dimensions reduced to one
`[]` suffix, and an `unsigned` modifier dropped (it matters only for MySQL). For
Postgres, synonyms are then folded onto the names in §4.1 (`int4` → `integer`,
`character varying` → `varchar`, `bpchar` → `char`, `serial4` → `serial`, …). The
mapping assumes the recommended drivers (`pgx/v5/stdlib` and `modernc.org/sqlite`).
Other drivers may need `types:` overrides.

### 4.1 PostgreSQL

| SQL type | Go type | Column kind |
| :--- | :--- | :--- |
| `smallint`, `int2`, `smallserial`, `serial2` | `int16` | `NumberColumn[int16]` |
| `integer`, `int`, `int4`, `serial`, `serial4` | `int32` | `NumberColumn[int32]` |
| `bigint`, `int8`, `bigserial`, `serial8` | `int64` | `NumberColumn[int64]` |
| `real`, `float4` | `float32` | `NumberColumn[float32]` |
| `double precision`, `float8` | `float64` | `NumberColumn[float64]` |
| `numeric`, `decimal` | `string` (override recommended) | `Column[string]` |
| `money` | `string` | `Column[string]` |
| `boolean`, `bool` | `bool` | `Column[bool]` |
| `text`, `varchar` (`character varying`), `char` (`character`, `bpchar`), `citext` | `string` | `StringColumn[string]` |
| `inet`, `cidr`, `macaddr` | `string` | `StringColumn[string]` |
| `bytea` | `[]byte` | `Column[[]byte]` |
| `uuid` | `string` (override to `uuid.UUID` recommended) | `Column[string]` |
| `date`, `timestamp`, `timestamptz` | `time.Time` | `Column[time.Time]` |
| `time`, `timetz`, `interval` | `string` | `Column[string]` |
| `json`, `jsonb` | `json.RawMessage` | `Column[json.RawMessage]` |
| user enum | `enum.<Name>` | `StringColumn[enum.<Name>]` |
| `<type>[]` | **v1: generation error unless overridden** | — |
| anything else | **generation error unless overridden** | — |

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

SQLite stores JSON as text, and the recommended `modernc.org/sqlite` driver returns it
as a Go `string`. A `json.RawMessage` column therefore uses the `textbytes` scan plan
(§4.4), which scans through `[]byte` and converts; a `string` driver value cannot be
scanned into `json.RawMessage` directly.

A type that is not in the table goes through SQLite's affinity rules, in this order:
contains `int` → `int64`; contains `char`, `clob` or `text` → `string`
(`StringColumn`); contains `blob` → `[]byte`; contains `real`, `floa` or `doub` →
`float64`. Only a type that matches neither the table nor a rule (`FOO`) maps to
`string` (`Column[string]`) with a generation warning. For example, `mediumint` maps to
`int64` and `varying character(20)` to `string`.

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
| generated column | as above | as above | *(not present)* | *(not present)* |

The **scan plan** is the Go counterpart of atla's `row_codec`. It is chosen per
column so that the generated scanner avoids slow `database/sql` conversion paths:

| Plan | When | Generated code |
| :--- | :--- | :--- |
| `direct` | NOT NULL and not a widen type: the Go type matches the driver value (`int64`, `float64`, `string`, `[]byte`, `bool`, `time.Time`, `sql.Scanner` types such as enums and non-builtin overrides). Also a nullable non-widen column with `--nullable=sqlnull`, whose field is a `sql.Null[T]`. | `&u.Field` |
| `widen` | NOT NULL with a widen type: builtin integer and float types whose values fit an `int64`/`float64` intermediate (`int`, `int8`–`int32`, `uint8`–`uint32`, `byte`, `rune`, `float32`, and `uintptr`). `int64`, `float64`, `uint` and `uint64` take `direct`: the unsigned types can exceed `int64` and `database/sql` parses them with `strconv.ParseUint`, but `uintptr` cannot be a scan destination, so it keeps `widen`. | scan into an `int64`/`float64` local, then convert |
| `nullptr` | nullable with `--nullable=pointer`, not a widen type | scan into `sql.Null[T]` local; `if n.Valid { v := n.V; u.Field = &v }` (allocates only for non-NULL) |
| `nullwiden` | nullable with a widen type, with either `--nullable` style | `sql.Null[int64]` local, then convert and take the address |
| `textbytes` | NOT NULL SQLite `json`: the driver returns text as a `string`, which `database/sql` cannot scan into `json.RawMessage` | scan into a `[]byte` local, then convert to `json.RawMessage` |
| `nulltextbytes` | nullable SQLite `json`, with either `--nullable` style | scan into `sql.Null[[]byte]`; for `pointer` take the address of the converted value, for `sqlnull` fill `sql.Null[json.RawMessage]` |

Generated enum types implement `sql.Scanner` and `driver.Valuer`. The scanner
rejects values outside the enum with an error, so enum columns take the `direct`
plan without reflection.

### 4.5 Overrides (`types:`, `columns:`)

- **`types:`** maps every column of a database type. `db_type` is matched after the
  normalization above, applied to both sides, so length arguments are ignored and
  Postgres synonyms match each other: `db_type: varchar` matches
  `character varying(255)`, and `db_type: int4` matches `integer`. Serial types are not
  folded into integer types: `db_type: integer` does not match a `serial` column, nor
  `bigint` a `bigserial` column. An override whose type no column has produces a
  warning. Two overrides for the same normalized type are an error.
- **`columns:`** maps one column, written `table.column`, and wins over `types:`. A
  malformed key, a column that does not exist, or two overrides for one column are
  errors.
- **`go_type`** is a builtin (`string`, `bool`, `[]byte`, `int`, `int8` … `int64`,
  `uint` … `uint64`, `uintptr`, `byte`, `rune`, `float32`, `float64`), which needs no
  import, or `<import path>.<Type>`, split at the last `.` after the last `/`:
  `github.com/google/uuid.UUID` gives import `github.com/google/uuid` and type
  `uuid.UUID`, and `time.Duration` gives import `time` and type `time.Duration`. The
  qualifier is the last path element, skipping a `/vN` major-version element and a
  gopkg.in `.vN` suffix (`github.com/foo/bar/v2.Thing` → `bar.Thing`). The type name
  must be exported, and the qualifier must be a Go identifier.
- **Kind.** A builtin integer or float type in `atla.Number` gives `NumberColumn`,
  `string` gives `StringColumn`, anything else gives `Column` (the `Null` variants for
  nullable columns). `uint` and `uintptr` are integers but not in `atla.Number`, so
  they use `Column`; every generated `NumberColumn[T]` therefore satisfies the
  runtime constraint.
- **Scan plan.** Builtins follow §4.4, including the SQLite JSON text-to-bytes plans.
  Non-builtin types are never widened: they are assumed to implement `sql.Scanner` and
  take `direct` (or `nullptr` when nullable with `--nullable=pointer`). P3 validates
  them with `go/types` (§8).

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

Cross-schema FKs (`RefSchema` set) and FKs into a table outside the inspected document
(`RefExternal`, §2.1) are skipped with a warning in v1. Many-to-many edges through join
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

`generate` and `check` arrive in P3. The validation below runs in every command that
resolves a schema, including `atlagen ir`.

- **Stale files**: `generate` deletes files in `--out` (and in its generated
  subpackages) that carry the generated header but are no longer produced. It never
  touches files without the header. Handwritten files can therefore live next to
  generated ones, as in ent.
- **`check`**: renders in memory and compares with disk, including stale-file
  detection. It prints a unified diff and exits with status 1 on mismatch.
- **Tables without a PK**: no `Get` / `GetMany` / `DeleteByPK` / `NewNullableRowScanner`;
  everything else works.
- **Views**: ignored in v1 (my call). Read-only view models are a later candidate.
- **Validation errors** (generation fails with a precise message and exit status 1):
  - options: a missing, unknown or unsupported (`mysql`, `tidb`) dialect, a missing
    `out`, an invalid `nullable` or package name, `dev_url` / `--dev-url` without an
    Atlas URL in `schema` / `--schema`, no `go.mod` at or above `out`, and an invalid
    config file (§1.4);
  - input: JSON instead of HCL, invalid HCL, an ambiguous or unknown schema, a
    selected schema without tables, and the other parse errors of §2.1;
  - naming: every collision and invalid `names.tables` entry of §3;
  - mapping: an unmapped Postgres type or array column without an override, a
    `columns:` override naming an unknown `table.column`, an invalid `go_type`, and
    duplicate overrides (§4.5);
  - P3: an override type that does not implement `sql.Scanner` and `driver.Valuer`
    (checked with `go/types` when `go_type` is outside the standard library).
- **Warnings** (printed to stderr as `atlagen: warning: <msg>`; generation continues):
  an unknown SQLite type mapped to `string` (§4.2), and a `types:` override whose type
  no column has (§4.5).
