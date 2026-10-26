# atla-go P2 (atlagen Front End) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the front half of `atlagen`, which turns Atlas schema JSON into a fully resolved Go-facing IR:
- configuration (`atlagen.yaml` plus flags)
- input loading (stdin, file, or the `atlas` CLI)
- parsing to IR
- Go naming
- SQL→Go type mapping with scan plans

It is exposed through `atlagen ir` (dumps the IR as JSON) and `atlagen version`. P3 then plugs emitters onto the same pipeline.

**Architecture:** All code lives in the tooling module `github.com/hirokazumiyaji/atla-go/atlagen` under `internal/`. The pipeline runs `config.Resolve → inspect.Load → parser.Parse → naming.Apply → mapper.Apply`. Each stage is a pure function over `*ir.Schema`, except `inspect`, which does I/O. Each stage is testable on its own against JSON fixtures, and the CLI is a thin `run(...) int` wrapper.

**Tech Stack:** Go 1.24 (`go 1.24.0`), standard library (`encoding/json`, `flag`, `os/exec`, `runtime/debug`), `go.yaml.in/yaml/v3` v3.0.4, `golang.org/x/mod` v0.27.0 (`modfile`).

**Spec:** `docs/generator-spec.md` §1–§4 and §8; `docs/roadmap.md` P2; `docs/architecture.md` §3. Reference implementation (Python): `/home/user/atla/packages/atlagen/src/atlagen/{parser,naming,mapper,inspect,config}.py`.

## Amendment A — input format is Atlas HCL (decided 2026-10-25, after Task 1)

Task 1 parsed `atlas schema inspect --format '{{ json . }}'`. Research then showed that this JSON is a lossy summary: it never contains defaults, identity/auto_increment, Postgres enums or partial-index `where`, and its real shape differs from the atla fixtures (see `.superpowers/sdd/2026-10-25-p2-atlagen-frontend/atlas-json-research.md`). The user chose to read **Atlas's default HCL output** (`atlas schema inspect -u <url>`, no `--format`) instead. Consequences:

- **New Task 8** replaces the JSON parser with an HCL parser, keeping the `parser.Parse` signature and the `ir` types. JSON input is rejected with a pointer to the HCL command.
- **Execution order:** Task 1 (done), Task 2, **Task 8**, then Tasks 3–7.
- Tasks 3, 4, 6 and 7 below already reflect HCL: fixture names `.hcl`, and the `atlas` command without `--format`.
- **New allowed dependency:** `github.com/hashicorp/hcl/v2 v2.24.0`. Its transitive deps (`github.com/zclconf/go-cty` etc.) come with it. v2.25.0 requires Go 1.25, so do not upgrade.

## Global Constraints

- **Module:** `github.com/hirokazumiyaji/atla-go/atlagen` keeps the `go 1.24.0` directive. The only allowed direct requires are `go.yaml.in/yaml/v3 v3.0.4`, `golang.org/x/mod v0.27.0` and `github.com/hashicorp/hcl/v2 v2.24.0` (plus their transitive deps). Add them with `GOTOOLCHAIN=local go get …@<version>` so the go directive is not bumped, and commit `go.sum`.
- **Root module:** `github.com/hirokazumiyaji/atla-go` is not touched, and its `go.mod` must still have no `require` lines.
- **Packages:** every package lives under `atlagen/internal/` (`ir`, `parser`, `naming`, `mapper`, `inspect`, `config`, `pipeline`). The only binary is `atlagen/cmd/atlagen`.
- **Tests:** no network access and no real `atlas` binary. `inspect` tests use a fake `atlas` shell script placed first on `PATH`.
- **Determinism:** tables are sorted by DB name; columns, enum values, PK columns and FK columns keep Atlas order; uniques keep index order. The same input always produces byte-identical IR JSON.
- **Errors:**
  - Internal errors are lowercase, carry no `atlagen:` prefix, and are wrapped with context in the form `table "users": column "age": …`.
  - The CLI prints `atlagen: <err>` to stderr. It exits 1 on a runtime error and 2 on a usage error.
- **Gate (run in `atlagen/`):** `gofmt -l .` prints nothing, `go vet ./...`, `go test -race ./...`, and `golangci-lint run` all pass. The repo root gate also stays green.

## Plan Decisions (spec gaps settled here; Task 7 writes them back into the docs)

1. **Packages are `internal/`.** The generator has no public Go API. `atlavet` (P3) stays outside `internal/` so the golangci-lint plugin can import it later.
2. **`generate` / `check` move to P3** together with the emitters. P2 ships `atlagen ir`, which uses the full flag set of §1.3 and prints the resolved IR as indented JSON. This is a permanent debugging command, plus `atlagen version`. No stub commands.
3. **Edges are derived in P4 (roadmap P4.1).** In P2, `ir` has no `Edge` type, and `names.edges` is parsed and validated for shape but otherwise unused. `ir.ForeignKey` carries `RefSchema` so P4 can skip cross-schema FKs.
4. **IR additions beyond spec §2:** `Column.Enum` (the DB enum name when the column's type is a schema enum, else `""`), `ForeignKey.RefSchema`, typed `ColumnKind` / `ScanPlan` string types, and JSON tags in snake_case.
5. **Identifier sanitizing.**
   - Splitting: `naming.Pascal` splits on every non-ASCII-alphanumeric rune and on camelCase boundaries.
   - Leading digit: a result that starts with a digit gets the prefix `X` (`Pascal("2fa_codes")` = `X2faCodes`; the table's model, after singularization, is `X2faCode`).
   - Enum values: an enum value whose Pascal form is empty becomes `Empty`.
6. **Reserved table-package names:** Go keywords, predeclared identifiers, `atla`, `dialect`, `enum`, `sql`, `driver`, `time`, `json`, `context`, `main`, `internal`, `vendor`, and the root package name. Any of these gets the suffix `tbl`. This extends the spec's list with the packages that generated files import and the directory names the Go toolchain will not import as an ordinary package.
7. **Root-package identifier collisions are generation errors.**
   - Reserved names: `Client`, `NewClient`, `Dialect`.
   - Each model reserves itself plus `<Model>Edges`, `<Model>Insert`, `<Model>Update`, `<Model>Query`, `<Model>Repo`, `<Model>Table`, `<Model>Key`.
   - Models become fields of `Client`, so a model named `Tx` collides with the `Client.Tx` method and is an error.
   - Duplicate column Go names within a table, duplicate package names, and enum identifier clashes (type names plus constants) are also errors.
8. **`names.tables` override keys must match a table.** An unused key is an error, as in atla. Override values must be exported Go identifiers.
9. **`types:` overrides for a DB type absent from the schema produce a warning.** `columns:` overrides naming an unknown `table.column` produce an error.
10. **Override `go_type` parsing.**
    - A type with a package path is written `path.Type`, split at the last `.` after the last `/`. For example, `github.com/google/uuid.UUID` gives import `github.com/google/uuid` and name `uuid.UUID`, and `time.Duration` gives import `time` and name `time.Duration`.
    - A builtin (`string`, `int64`, …) has no import.
    - Kind: builtin integer/float → `NumberColumn`, `string` → `StringColumn`, anything else → `Column`.
    - Scan plan: builtins follow the normal rules; non-builtin types use `direct`. They are assumed to be `sql.Scanner`s; validation with `go/types` happens in P3.
11. **Scan plans.**
    - `widen` applies to builtin integer and float types whose values fit an `int64`/`float64` intermediate: `int`, `int8`–`int32`, `uint8`–`uint32`, `byte`, `rune`, `float32`, and `uintptr`. `int64`, `float64`, `uint` and `uint64` use `direct`: the unsigned types can exceed `int64` and `database/sql` parses them with `strconv.ParseUint`, but `uintptr` cannot be a scan destination, so it keeps `widen`.
    - A nullable column with `--nullable=pointer` gets `nullptr`, or `nullwiden` if it is a widen type.
    - A nullable column with `sqlnull` gets `direct` (the field is a `sql.Null[T]`), or `nullwiden` if it is a widen type.
    - SQLite `json` keeps the `json.RawMessage` Go type but gets `textbytes` (`nulltextbytes` when nullable): the driver returns text as a `string`, so the scanner goes through `[]byte` and converts.
12. **SQLite unknown types.** First try the explicit table in §4.2, then SQLite affinity rules: contains `int` → `int64`; contains `char`/`clob`/`text` → `string`; contains `blob` → `[]byte`; contains `real`/`floa`/`doub` → `float64`. Only if both fail is the type mapped to `string` with a warning.
13. **Uniques** are unique indexes whose parts are all plain columns. Indexes that have an expression part or a `where` predicate are excluded, because they cannot prove O2O.
14. **Relative paths.**
    - Values from `atlagen.yaml` (a schema file path, a relative `file://` URL, `out`) resolve against the config file's directory, and the `atlas` subprocess runs with that directory as its working directory.
    - Values from flags resolve against the current working directory.
    - A relative SQLite path in `dev_url` / `--dev-url` resolves against the directory of its own source and is then made absolute, because the atlas subprocess has one working directory.
15. **`--dev-url` without an Atlas URL in `--schema`** is a usage error.
16. **Unsupported dialects:** `mysql` and `tidb` fail with `dialect "mysql" is not supported yet`. Any other value fails with `unknown dialect`.

## Review Focus

1. **Names that produce clashing Go identifiers** must fail with an error naming both sources, never generate silently broken code. Examples: tables `user` and `user_insert` (`UserInsert` clashes), columns `user_id` and `userId`, a table named `client`. Owned by Task 2 (`TestApplyCollisions`).
2. **Identifiers starting with digits or containing hyphens or spaces** must still become valid Go identifiers. Examples: `2fa_codes`, `order-items`, enum value `in-progress`. Owned by Task 2 (`TestPascalSanitizes`).
3. **Relative paths in `atlagen.yaml`** must resolve against the config file's directory when atlagen runs via `go:generate` from a package directory. Owned by Task 5 (`TestResolveRelativeToConfigDir`).
4. **A missing or failing `atlas` binary** must produce an actionable message that includes atlas's stderr. Owned by Task 4 (`TestLoadAtlasMissing`, `TestLoadAtlasFails`).
5. **Atlas HCL variants** must parse to the same IR: `default` given as a literal or `sql("…")`, `auto_increment = true`, an `identity {}` block, index `columns = [...]` or `on {}` blocks, and a type given as an identifier, a call (`varchar(255)`) or `sql("…")`. Owned by Task 8 (`TestParseHCLVariants`).

---

## File Structure

```text
atlagen/
├── go.mod, go.sum                         # T5 adds yaml + x/mod
├── cmd/atlagen/main.go                    # T6  main → run()
├── cmd/atlagen/main_test.go               # T6
└── internal/
    ├── testdata/atlas/*.json              # T1  fixtures (4 copied from atla + 4 new)
    ├── testdata/golden/*.ir.json          # T6
    ├── ir/ir.go                           # T1  IR types
    ├── parser/parser.go, parser_test.go   # T1  Atlas JSON → ir.Schema (DB facts)
    ├── naming/naming.go                   # T2  Pascal, Singular, PackageName
    ├── naming/apply.go, *_test.go         # T2  Apply (models, packages, fields, enums, collisions)
    ├── mapper/mapper.go, *_test.go        # T3  GoType, ColumnKind, ScanPlan, overrides
    ├── inspect/inspect.go, *_test.go      # T4  stdin / file / atlas subprocess
    ├── config/config.go, *_test.go        # T5  YAML, flags, resolution, import path
    └── pipeline/pipeline.go, *_test.go    # T6  Run: resolved config → *ir.Schema
```

---

### Task 1: IR types and Atlas JSON parser

**Files:**
- Create: `atlagen/internal/ir/ir.go`, `atlagen/internal/parser/parser.go`, `atlagen/internal/parser/parser_test.go`
- Create fixtures in `atlagen/internal/testdata/atlas/`:
  - Copy `postgres_users.json`, `sqlite_users.json`, `mysql_users.json` and `typecheck_users.json` verbatim from `/home/user/atla/tests/fixtures/atlas/`.
  - Add four new files:
    - `composite.json` (Postgres): tables `orgs`, `users`, `memberships` with `PRIMARY KEY (org_id, user_id)`, and `membership_notes` with a composite FK `(org_id, user_id) → memberships`. Indexes: a unique 2-column index, a unique index with an expression part, and a unique index with a `"where"` key.
    - `sqlite_types.json`: an `INTEGER` single-column PK with no attrs (rowid alias), and the column types `DATETIME`, `BOOLEAN`, `VARCHAR(10)`, `MEDIUMINT`, `NUMERIC(10,2)`, `FOO`.
    - `pg_types.json`: one column per Postgres type in generator-spec §4.1, plus `text[]`, an enum column, and a nullable `integer`.
    - `multi_schema.json`: schemas `public` and `audit`, with an FK from `public.events` to `audit.logs` whose `ref_table` is `{"name":"logs","schema":{"name":"audit"}}`.

**Interfaces:**
- Produces (package `ir`; JSON tags snake_case):
  ```go
  type Schema struct { Name, Dialect string; Enums []Enum; Tables []Table }
  type Enum struct { DBName, GoName string; Values []EnumValue }
  type EnumValue struct { DB, Go string }
  type Table struct { DBName, Model, Package string; Columns []Column; PrimaryKey []string; Uniques [][]string; ForeignKeys []ForeignKey }
  type Column struct { DBName, GoName, RawType, Enum string; GoType GoType; Kind ColumnKind; Nullable, HasDefault, InPK bool; Scan ScanPlan }
  type GoType struct { Name, ImportPath string }
  type ColumnKind string // KindColumn "Column", KindNumber "NumberColumn", KindString "StringColumn", KindNullColumn "NullColumn", KindNullNumber "NullNumberColumn", KindNullString "NullStringColumn"
  type ScanPlan string   // ScanDirect "direct", ScanWiden "widen", ScanNullPtr "nullptr", ScanNullWiden "nullwiden", ScanTextBytes "textbytes", ScanNullTextBytes "nulltextbytes"
  type ForeignKey struct { Symbol string; Columns []string; RefSchema, RefTable string; RefColumns []string }
  func (c Column) RequiredForInsert() bool // !Nullable && !HasDefault
  func (t Table) Column(dbName string) (Column, bool)
  ```
- Produces (package `parser`):
  ```go
  func Parse(data []byte, dialect, schemaName string) (*ir.Schema, error)
  ```
  `Parse` fills only DB facts: `Schema.{Name,Dialect,Enums[].DBName,Enums[].Values[].DB,Tables}`, `Table.{DBName,Columns,PrimaryKey,Uniques,ForeignKeys}`, and `Column.{DBName,RawType,Enum,Nullable,HasDefault,InPK}`. All Go-name and Go-type fields stay zero.

- [x] **Step 1: Write the failing tests** (`parser_test.go`, table-driven where possible).
  ```go
  func TestParsePostgresUsers(t *testing.T) {
      // postgres_users.json, dialect "postgres", schemaName "":
      // Name "public"; Enums [{DBName "user_role", Values DB ["admin","member","guest"]}]
      // Tables sorted: ["posts", "users"]
      // users.id: RawType "bigserial", Nullable false, HasDefault true (serial), InPK true
      // users.email: RawType "character varying(255)", HasDefault false → RequiredForInsert true
      // users.role: Enum "user_role", HasDefault true (default)
      // users.from: Nullable true
      // users.Uniques == [["email"]]; posts.id HasDefault true (identity)
      // posts.ForeignKeys == [{Symbol "posts_user_id_fkey", Columns ["user_id"], RefSchema "", RefTable "users", RefColumns ["id"]}]
  }
  func TestParseShapeVariants(t *testing.T) {
      // sqlite_users.json: users.id HasDefault true (attrs [{name: auto_increment}]); Uniques [["email"]] from "parts"
      // mysql_users.json: users.id HasDefault true (auto_increment: true); categories.id HasDefault true
      //   (attrs [{type: auto_increment, auto_increment: true}]); categories PK ["id"] from bare-string columns
      // inline variants built in-test: "auto_increment": "true" → HasDefault; "type": {"raw": "text"} → RawType "text"
  }
  func TestParseComposite(t *testing.T) {
      // memberships.PrimaryKey == ["org_id","user_id"], both InPK
      // membership_notes FK Columns ["org_id","user_id"] → RefTable "memberships", RefColumns ["org_id","user_id"]
      // Uniques contains the 2-column unique index; excludes the expression index and the partial ("where") index
  }
  func TestParseSQLiteRowidAlias(t *testing.T) { /* sqlite_types.json: INTEGER single-column PK without attrs → HasDefault true */ }
  func TestParseSchemaSelection(t *testing.T) {
      // multi_schema.json: schemaName "" → first schema ("public"); "audit" → audit tables
      // FK to audit.logs → RefSchema "audit", RefTable "logs"
      // schemaName "nope" → error containing `schema "nope" not found` and `available: public, audit`
  }
  func TestParseErrors(t *testing.T) {
      // invalid JSON; {"schemas": []} → "no schemas"; a selected schema with no tables → `schema "x" has no tables`
      // table without name; column without name; column without type (error names table and column)
      // duplicate table names; PK or FK naming an unknown column → error naming table and column
  }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./internal/parser/`. Expected: FAIL (build fails because names are undefined).
- [x] **Step 3: Implement.**
  - Decode into lenient intermediate structs, using `json.RawMessage` for polymorphic fields (`type`, column refs, `ref_table`, attrs).
  - Mirror the Python helpers in `parser.py`: `_extract_raw_type`, `_column_has_default` (default set; a serial-family type `serial|serial2|serial4|serial8|smallserial|bigserial`; `auto_increment` truthy; `identity` set; attrs whose type or name contains `auto_increment`/`identity` or which carry `auto_increment: true`), `_column_name_tuple`, `_index_part_columns`, and `_extract_ref_table`.
  - Add the SQLite rowid rule: `dialect == "sqlite"`, a single-column PK, and the lowercased raw type is exactly `integer`.
  - `Column.Enum` is set when the column's raw type, lowercased with any `schema.` prefix stripped, equals a schema enum's lowercased name.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./internal/...`. Expected: PASS.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): add IR types and Atlas JSON parser"`

### Task 2: Naming

**Files:**
- Create: `atlagen/internal/naming/naming.go`, `atlagen/internal/naming/apply.go`, `atlagen/internal/naming/naming_test.go`, `atlagen/internal/naming/apply_test.go`

**Interfaces:**
- Consumes: `ir.Schema` with the DB facts that Task 1 fills.
- Produces:
  ```go
  func Pascal(s string) string                    // Go initialisms, sanitizing (Plan Decision 5)
  func Singular(word string) string               // lowercase English singular
  func PackageName(model, rootPkg string) string  // lowercase model, + "tbl" if reserved (Plan Decision 6)
  type Overrides struct { Tables map[string]string; RootPackage string }
  func Apply(s *ir.Schema, o Overrides) error
  ```
  `Apply` fills `Table.Model`, `Table.Package`, `Column.GoName`, `Enum.GoName` and `EnumValue.Go`.

- [x] **Step 1: Write the failing tests.**
  ```go
  func TestPascal(t *testing.T) {
      // "user_id"→"UserID", "avatar_url"→"AvatarURL", "api_keys"→"APIKeys", "http_status"→"HTTPStatus",
      // "user_ids"→"UserIDs", "created_at"→"CreatedAt", "userName"→"UserName", "json_data"→"JSONData", "uuid"→"UUID"
  }
  func TestPascalSanitizes(t *testing.T) {
      // "order-items"→"OrderItems", "first name"→"FirstName", "2fa_codes"→"X2faCodes", "in-progress"→"InProgress", "--"→""
  }
  func TestSingular(t *testing.T) {
      // users→user, categories→category, addresses→address, boxes→box, statuses→status, people→person,
      // children→child, movies→movie, series→series, status→status, analysis→analysis, data→data, class→class
  }
  func TestPackageName(t *testing.T) {
      // ("UserProfile","dbgen")→"userprofile"; ("Type","dbgen")→"typetbl"; ("Enum","dbgen")→"enumtbl";
      // ("String","dbgen")→"stringtbl"; ("Dbgen","dbgen")→"dbgentbl"; ("Dialect","dbgen")→"dialecttbl";
      // ("Main","dbgen")→"maintbl"; ("Internal","dbgen")→"internaltbl"; ("Vendor","dbgen")→"vendortbl"
  }
  func TestApply(t *testing.T) {
      // parsed postgres_users.json: users → Model "User", Package "user"; posts → "Post"/"post"
      // user_profiles → "UserProfile"/"userprofile"; api_keys → "APIKey" (singularize the last word only)
      // column "user_id" → "UserID"; column "edges" → "EdgesCol"
      // enum user_role → GoName "UserRole", values "UserRoleAdmin", "UserRoleMember", "UserRoleGuest"
      // enum value "" → "UserRoleEmpty"; "in-progress" → "UserRoleInProgress"
      // Overrides{Tables: {"people": "Person"}} → Model "Person"
  }
  func TestApplyCollisions(t *testing.T) {
      // tables "user" + "users" → error mentioning both and `names.tables`
      // tables "user" + "user_insert" → error (UserInsert clashes with User's insert type)
      // table "client" → error (reserved root identifier "Client")
      // table "tx" → error (model Tx clashes with the Client.Tx method)
      // columns "user_id" + "userId" in one table → error naming both columns
      // tables "user_profiles" + "userprofiles" → package clash error
      // enum "user_role" with value "admin" + enum "user_role_admin" → enum identifier clash error
      // override key "nope" not matching any table → error; override value "lower" (unexported) → error
  }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./internal/naming/`. Expected: FAIL.
- [x] **Step 3: Implement.**
  - **Initialisms:** use exactly the list in generator-spec §3. A word matches when it equals an initialism case-insensitively; it is then upper-cased. Plural `<initialism>s` becomes `<INITIALISM>s`.
  - **Singular:** port the `_UNCHANGING` and `_IRREGULAR_SINGULAR` tables and the suffix rules from `/home/user/atla/packages/atlagen/src/atlagen/naming.py`.
  - **Collision errors:** report every clash, not just the first. Join them with `errors.Join`, in deterministic order.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./internal/...`. Expected: PASS.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): add naming rules and collision checks"`

### Task 8: Replace the JSON parser with an Atlas HCL parser (runs after Task 2)

**Files:**
- Modify: `atlagen/go.mod`, `atlagen/go.sum`: `GOTOOLCHAIN=local go get github.com/hashicorp/hcl/v2@v2.24.0`. The go directive stays `go 1.24.0`.
- Rewrite: `atlagen/internal/parser/parser.go`, `atlagen/internal/parser/parser_test.go`. The JSON-decoding code is removed.
- Replace fixtures in `atlagen/internal/testdata/atlas/`: delete every `*.json` and add `.hcl` equivalents with the same base names (`postgres_users`, `sqlite_users`, `mysql_users`, `typecheck_users`, `composite`, `sqlite_types`, `pg_types`, `multi_schema`), plus `testdata/atlas/README.md` recording where each file came from.
  - **SQLite fixtures** are real atlas output. Create the DB with `python3 -c 'import sqlite3…'` and run `/tmp/claude-0/-home-user/7f79205c-c024-59bd-8c4f-22ee24c02d98/scratchpad/atlasbin schema inspect -u "sqlite://<file>"`. This is a community atlas build made from source.
  - **Postgres/MySQL fixtures** are hand-written. Follow the HCL that atlas prints, as defined in the atlas source at `/tmp/claude-0/-home-user/7f79205c-c024-59bd-8c4f-22ee24c02d98/scratchpad/atlas-src` (`sql/postgres/sqlspec.go`, `sql/mysql/sqlspec.go`, `sql/sqlspec/sqlspec.go`, `sql/internal/specutil`).
  - The fixtures cover the same facts the JSON fixtures covered: enum, defaults, identity, serial, composite PK/FK, an expression index, a partial index, cross-schema FK, the rowid alias and every type in pg_types.
- Modify: tests in `atlagen/internal/naming/` that load JSON fixtures, so they load the `.hcl` fixtures (same assertions).

**Interfaces:**
- Consumes/Produces: unchanged — `parser.Parse(data []byte, dialect, schemaName string) (*ir.Schema, error)` and the `ir` types from Task 1. `Parse` still fills only DB facts.

**Rules (the HCL that `atlas schema inspect` prints):**
- **Parsing:** use `hclsyntax.ParseConfig` and walk the `*hclsyntax.Body` AST. There is no evaluation context: references such as `schema.public`, `column.id` and `table.users.column.id` are read as `hcl.Traversal`s, and only literal expressions are evaluated (with a nil context).
  - Top-level blocks: `schema "<name>" {}`, `table "<name>" { schema = schema.<s> … }`, `enum "<name>" { schema = schema.<s>  values = [...] }`.
  - Any other top-level block (`view`, `function`, `sequence`, …) is ignored.
- **Schema selection:** `schemaName == ""` picks the first `schema` block in file order. Only tables and enums whose `schema` attribute references the selected schema are included. Error messages stay as Task 1 defined them (`schema "x" not found; available: …`, `has no tables`).
- **Column:**
  - `null = true` → Nullable (absent → false), except where the database guarantees NOT NULL: Postgres/MySQL primary keys, SQLite `WITHOUT ROWID` primary keys, and the SQLite `INTEGER PRIMARY KEY` rowid alias. Ordinary SQLite rowid tables allow NULL in non-INTEGER and composite primary keys.
  - `type`: an identifier (`integer`, `bigserial`, `timestamptz`, `double_precision`) or a call (`varchar(255)`, `numeric(10,2)`) becomes RawType. The identifier's `_` is replaced by a space (`double_precision` → `double precision`, `character_varying(255)` → `character varying(255)`), and args are rendered back, comma-joined, without spaces.
  - `sql("<t>")` → RawType `<t>`.
  - `enum.<name>` → `Column.Enum = <name>` and RawType `<name>`.
  - Verify these renderings against atlas-src's type specs and record any exception in the parser's comments.
- **HasDefault:** true when any of these holds: a `default` attribute is present (any expression); `auto_increment = true`; an `identity` block is present; the type is in the Postgres serial family; the SQLite rowid rule (single-column PK with RawType lowercased equal to `integer`). SQLite assigns no default semantics to serial-family type names.
- **Keys and indexes:**
  - `primary_key { columns = [column.a, column.b] }` → PrimaryKey in that order.
  - `index "<n>"` contributes to Uniques only when `unique = true`, it has no `where`, and every part is a column (`columns = [...]`, or `on { column = column.x }` blocks, with no `on { expr = … }`).
- **Foreign keys:** `foreign_key "<n>" { columns = [...]; ref_columns = [table.<t>.column.<c>, …] }`.
  - Symbol = the block label.
  - RefTable and RefSchema come from the ref traversal. Check in atlas-src how a cross-schema reference is printed, for example `table.<schema>.<table>.column.<c>` or a `schema`-qualified form.
  - RefSchema is `""` when the referenced schema is the selected one; this resolves Task 1's deferred minor item about RefSchema.
- **Enums:** `values` must be a list of string literals. A column's enum must match the enum name exactly; there is no case-folding (this resolves Task 1's deferred minor item).
- **Errors:**
  - HCL syntax diagnostics → an error with `file:line:col`. The filename passed to the parser is `schema.hcl`.
  - Input whose first non-space byte is `{` → `input looks like JSON; atlagen reads Atlas HCL — run "atlas schema inspect" without --format`.
  - All structural errors from Task 1 are kept (unknown column in PK/FK/index, duplicate table/column, missing type, …) in the same lowercase `table "t": column "c": …` form.

- [x] **Step 1: Write the failing tests.** Port every Task 1 test to the `.hcl` fixtures, keeping the same expected IR. Then add these:
  ```go
  func TestParseHCLVariants(t *testing.T) {
      // inline HCL: default = 0, default = "bob", default = sql("CURRENT_TIMESTAMP") → HasDefault
      // auto_increment = true → HasDefault; identity { generated = BY_DEFAULT } → HasDefault
      // type = varchar(255) → RawType "varchar(255)"; type = sql("geometry(Point,4326)") → RawType "geometry(Point,4326)"
      // type = double_precision → "double precision"; type = enum.user_role → Enum "user_role", RawType "user_role"
      // index with on { column = column.a } on { column = column.b } unique = true → Uniques [["a","b"]]
      // index with on { expr = "lower(a)" } unique = true → excluded; unique = true + where = "a > 0" → excluded
  }
  func TestParseRejectsJSON(t *testing.T) { /* `{"schemas":[]}` → error containing "input looks like JSON" and "atlas schema inspect" */ }
  func TestParseSyntaxError(t *testing.T) { /* `table "t" {` (unclosed) → error containing "schema.hcl:" */ }
  func TestParseRefSchemaNormalized(t *testing.T) { /* same-schema FK written with explicit schema qualifier → RefSchema ""; cross-schema (multi_schema.hcl) → "audit" */ }
  func TestParseEnumExactMatch(t *testing.T) { /* enums "Mood" and "mood"; column type enum.mood → Enum "mood" */ }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./internal/parser/`. Expected: FAIL.
- [x] **Step 3: Implement.** Write the parser as described in Rules, and update the naming tests' fixture paths.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./... && grep '^go 1.24.0$' go.mod`. Expected: PASS, and the go directive is unchanged.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): parse Atlas HCL instead of lossy inspect JSON"`

### Task 3: Type mapper and scan plans

**Files:**
- Create: `atlagen/internal/mapper/mapper.go`, `atlagen/internal/mapper/mapper_test.go`

**Interfaces:**
- Consumes: an `ir.Schema` after `naming.Apply`. It uses `Enum.GoName`.
- Produces:
  ```go
  type NullStyle string // NullPointer "pointer", NullSQLNull "sqlnull"
  type TypeOverride struct { DBType, GoType string }
  type ColumnOverride struct { Column, GoType string } // Column is "table.column"
  type Options struct { Dialect string; Nullable NullStyle; Types []TypeOverride; Columns []ColumnOverride; EnumImportPath string }
  func Apply(s *ir.Schema, o Options) (warnings []string, err error)
  func NormalizeType(raw string) string
  ```
  `Apply` fills `Column.GoType`, `Column.Kind` and `Column.Scan`.

- [x] **Step 1: Write the failing tests.**
  ```go
  func TestNormalizeType(t *testing.T) {
      // "character varying(255)"→"character varying", "NUMERIC(10, 2)"→"numeric", "timestamp with time zone"→"timestamptz",
      // "timestamp without time zone"→"timestamp", "time with time zone"→"timetz", "  Double   Precision "→"double precision",
      // "int unsigned"→"int" (unsigned recorded separately; not used by PG/SQLite), "text[]"→"text[]"
  }
  func TestPostgresTable(t *testing.T) {
      // one subtest per generator-spec §4.1 row, using pg_types.hcl (non-null columns):
      // smallint→int16 NumberColumn widen; integer→int32 NumberColumn widen; bigint→int64 NumberColumn direct;
      // real→float32 widen; double precision→float64 direct; numeric→string Column; money→string Column;
      // boolean→bool Column; text/varchar/char/citext/inet/cidr/macaddr→string StringColumn; bytea→[]byte Column;
      // uuid→string Column; date/timestamp/timestamptz→time.Time (ImportPath "time") Column;
      // time/timetz/interval→string Column; json/jsonb→json.RawMessage (ImportPath "encoding/json") Column;
      // enum column→Name "enum.UserRole", ImportPath EnumImportPath, StringColumn
  }
  func TestPostgresArrayRequiresOverride(t *testing.T) {
      // text[] column without override → error containing `table "t": column "tags": array type "text[]"` and "override"
      // with ColumnOverride{"t.tags", "github.com/lib/pq.StringArray"} → Name "pq.StringArray", ImportPath "github.com/lib/pq", Column, direct
  }
  func TestPostgresUnmapped(t *testing.T) { /* raw type "xml" → error naming table, column, type and "types:" */ }
  func TestSQLiteTable(t *testing.T) {
      // sqlite_types.hcl: INTEGER→int64 direct; DATETIME→time.Time; BOOLEAN→bool; VARCHAR(10)→string StringColumn;
      // MEDIUMINT→int64 (affinity); NUMERIC(10,2)→string; FOO→string + exactly one warning naming table, column and "FOO";
      // JSON→json.RawMessage with the textbytes scan plan (nulltextbytes when nullable)
  }
  func TestNullability(t *testing.T) {
      // nullable integer, pointer → Kind NullNumberColumn, Scan nullwiden; nullable text, pointer → NullStringColumn, nullptr
      // nullable text, sqlnull → direct; nullable integer, sqlnull → nullwiden; nullable bigint, pointer → nullptr
  }
  func TestOverrides(t *testing.T) {
      // TypeOverride{"uuid","github.com/google/uuid.UUID"} → Name "uuid.UUID", ImportPath "github.com/google/uuid", Column, direct
      // TypeOverride{"numeric","github.com/shopspring/decimal.Decimal"} → "decimal.Decimal"
      // ColumnOverride beats TypeOverride for the same column
      // TypeOverride{"interval","time.Duration"} → Name "time.Duration", ImportPath "time"
      // ColumnOverride{"users.age","int64"} → int64 NumberColumn direct (builtin); ColumnOverride{"users.id","int32"} → widen;
      // uint64/uint overrides → direct (they can exceed int64); uintptr → widen (not a valid scan destination)
      // TypeOverride for a db type not in the schema → warning; ColumnOverride "users.nope" → error
      // override db_type matching is on NormalizeType of both sides ("VARCHAR(10)" override matches "varchar")
  }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./internal/mapper/`. Expected: FAIL.
- [x] **Step 3: Implement.**
  - Mapping tables come from generator-spec §4.1 and §4.2 plus Plan Decisions 10–12.
  - Postgres aliases: `int2`/`int4`/`int8`, `float4`/`float8`, `bool`, `serial2`/`serial4`/`serial8`, `bpchar`, `character`.
  - Arrays are detected by the suffix `[]` on the normalized type.
  - Warnings are sorted.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./internal/...`. Expected: PASS.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): add SQL to Go type mapper with scan plans"`

### Task 4: Input loading (`inspect`)

**Files:**
- Create: `atlagen/internal/inspect/inspect.go`, `atlagen/internal/inspect/inspect_test.go`

**Interfaces:**
- Produces:
  ```go
  type Source struct { Schema, DevURL, Dir string; Stdin io.Reader }
  func Load(ctx context.Context, src Source) ([]byte, error)
  func IsURL(schema string) bool // contains "://"
  ```
  Dispatch:
  - `Schema` is `""` or `"-"` → read `Stdin`.
  - `IsURL` → run `atlas schema inspect -u <Schema> [--dev-url <DevURL>]` with `cmd.Dir = Dir`. No `--format` flag: atlas's default output is the HCL that the parser reads.
  - Otherwise → read the file. A relative path is joined to `Dir` when `Dir` is non-empty.
  - Empty input from any source → error `empty schema input`.

- [x] **Step 1: Write the failing tests.** Fake atlas: a test helper writes an executable `atlas` sh script into `t.TempDir()` and prepends that directory to `PATH` with `t.Setenv`. The script appends its args to `args.txt`, writes its working directory to `pwd.txt`, prints a fixture to stdout, and exits with `$FAKE_ATLAS_EXIT`. Skip these tests on Windows.
  ```go
  func TestLoadStdin(t *testing.T) { /* Schema "-" and "" read Stdin bytes verbatim */ }
  func TestLoadFile(t *testing.T) { /* absolute path; relative path + Dir; missing file → error containing the path */ }
  func TestLoadAtlas(t *testing.T) {
      // Schema "file://schema.hcl", DevURL "docker://postgres/16/dev", Dir d → args.txt == `schema inspect -u file://schema.hcl --dev-url docker://postgres/16/dev`
      // pwd.txt == d; returned bytes == fixture
      // without DevURL → no --dev-url flag
  }
  func TestLoadAtlasMissing(t *testing.T) { /* PATH without atlas → error containing "atlas CLI not found on PATH" and "https://atlasgo.io" */ }
  func TestLoadAtlasFails(t *testing.T) { /* FAKE_ATLAS_EXIT=1, script prints "boom" to stderr → error containing "atlas schema inspect failed" and "boom" */ }
  func TestLoadEmpty(t *testing.T) { /* empty stdin → "empty schema input" */ }
  func TestLoadContextCancel(t *testing.T) { /* cancelled ctx → error; the fake atlas sleeps */ }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./internal/inspect/`. Expected: FAIL.
- [x] **Step 3: Implement.** Use `exec.LookPath("atlas")` and `exec.CommandContext`. Capture stderr in a buffer and trim it to at most 4 KiB in the error.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./internal/...`. Expected: PASS.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): add schema input loading (stdin, file, atlas CLI)"`

### Task 5: Configuration and resolution

**Files:**
- Modify: `atlagen/go.mod`, `atlagen/go.sum`. Add `go.yaml.in/yaml/v3@v3.0.4` and `golang.org/x/mod@v0.27.0` with `GOTOOLCHAIN=local go get`, and verify that the go directive stays `go 1.24.0`.
- Create: `atlagen/internal/config/config.go`, `atlagen/internal/config/config_test.go`

**Interfaces:**
- Consumes: `mapper.TypeOverride`, `mapper.ColumnOverride` and `mapper.NullStyle` (Task 3).
- Produces:
  ```go
  type File struct {
      Version int `yaml:"version"`; Schema string `yaml:"schema"`; DevURL string `yaml:"dev_url"`; Dialect string `yaml:"dialect"`
      SchemaName string `yaml:"schema_name"`; Out string `yaml:"out"`; Package string `yaml:"package"`; Nullable string `yaml:"nullable"`
      JSONTags *bool `yaml:"json_tags"`; Types []TypeOverride `yaml:"types"`; Columns []ColumnOverride `yaml:"columns"`; Names Names `yaml:"names"`
  }
  type TypeOverride struct { DBType string `yaml:"db_type"`; GoType string `yaml:"go_type"` }
  type ColumnOverride struct { Column string `yaml:"column"`; GoType string `yaml:"go_type"` }
  type Names struct { Tables map[string]string `yaml:"tables"`; Edges []EdgeName `yaml:"edges"` }
  type EdgeName struct { FK string `yaml:"fk"`; Name string `yaml:"name"`; Inverse string `yaml:"inverse"` }
  func LoadFile(path string) (*File, error)
  type Flags struct { Config, Schema, DevURL, Dialect, SchemaName, Out, Package, Nullable *string; JSONTags *bool } // nil = not set
  type Resolved struct {
      Schema, DevURL, BaseDir, Dialect, SchemaName, OutDir, Package, ImportPath string
      Nullable mapper.NullStyle; JSONTags bool
      Types []mapper.TypeOverride; Columns []mapper.ColumnOverride; TableNames map[string]string; Edges []EdgeName
  }
  func Resolve(cwd string, f Flags) (*Resolved, error)
  func ModuleImportPath(outDir string) (string, error)
  ```

- [x] **Step 1: Write the failing tests.**
  ```go
  func TestLoadFile(t *testing.T) {
      // the full generator-spec §1.4 example parses into File with every field set
      // unknown key ("dialekt: postgres") → error containing "dialekt"
      // version missing or 2 → error containing "version: 1"
      // edges entry missing fk or name → error
  }
  func TestResolveDefaults(t *testing.T) {
      // flags only: Dialect "postgres", Out "internal/dbgen", cwd = temp module root (go.mod "module example.com/app")
      // → Schema "-", Nullable "pointer", JSONTags true, Package "dbgen", OutDir abs, ImportPath "example.com/app/internal/dbgen", BaseDir cwd
  }
  func TestResolveFlagsOverrideConfig(t *testing.T) { /* config dialect sqlite + flag postgres → postgres; config json_tags false + no flag → false; flag JSONTags true → true */ }
  func TestResolveDefaultConfigDiscovery(t *testing.T) { /* atlagen.yaml in cwd and Flags.Config nil → used; absent → flags only */ }
  func TestResolveRelativeToConfigDir(t *testing.T) {
      // repo/atlagen.yaml with out: internal/dbgen, schema: db/schema.json; cwd = repo/internal/dbgen; Flags.Config "../../atlagen.yaml"
      // → OutDir repo/internal/dbgen, Schema repo/db/schema.json (absolute), BaseDir repo
      // schema: file://db/schema.hcl (URL) → Schema unchanged, BaseDir repo
      // dev_url: sqlite://config.db → repo/config.db (absolute); flag --dev-url sqlite://flag.db → cwd/flag.db
      // flag --out ./x from cwd → cwd/x
  }
  func TestResolveValidation(t *testing.T) {
      // missing dialect → "dialect is required"; "mysql" → `dialect "mysql" is not supported yet`; "oracle" → "unknown dialect"
      // missing out → "out is required"; nullable "ptr" → error listing pointer|sqlnull
      // out base "db-gen" without package → error containing "set --package"; package "Bad" or "type" → error
      // DevURL with schema "-" → error containing "--dev-url requires"
      // no go.mod above out → error containing "go.mod"
  }
  func TestModuleImportPath(t *testing.T) { /* nested existing dir; not-yet-existing out dir (walks from nearest existing ancestor); module root itself → module path */ }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./internal/config/`. Expected: FAIL.
- [x] **Step 3: Implement.**
  - Parse with `yaml.NewDecoder` plus `KnownFields(true)`.
  - Read the module path with `modfile.ModulePath`.
  - Convert `File.Types` and `File.Columns` to the mapper types.
  - A flag value counts as set only when its pointer is non-nil.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./... && grep '^go ' go.mod` (expect `go 1.24.0`). Expected: PASS.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): add config file and option resolution"`

### Task 6: Pipeline and `atlagen` CLI (`ir`, `version`)

**Files:**
- Create: `atlagen/internal/pipeline/pipeline.go`, `atlagen/internal/pipeline/pipeline_test.go`, `atlagen/cmd/atlagen/main.go`, `atlagen/cmd/atlagen/main_test.go`, `atlagen/internal/testdata/golden/postgres_users.ir.json`, `atlagen/internal/testdata/golden/composite.ir.json`
- Delete: `atlagen/doc.go` only if `golangci-lint` or `go vet` rejects a module root with no package code after the new packages exist; otherwise keep it.

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  ```go
  // package pipeline
  func Run(ctx context.Context, r *config.Resolved, stdin io.Reader) (*ir.Schema, []string, error)
  // inspect.Load(Source{Schema: r.Schema, DevURL: r.DevURL, Dir: r.BaseDir, Stdin: stdin})
  // → parser.Parse(data, r.Dialect, r.SchemaName) → naming.Apply(s, {Tables: r.TableNames, RootPackage: r.Package})
  // → mapper.Apply(s, {Dialect, Nullable, Types, Columns, EnumImportPath: r.ImportPath + "/enum"})
  // package main
  func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) int
  ```
  CLI behaviour:
  - `atlagen version` prints `atlagen <version>\n`. The version comes from `debug.ReadBuildInfo().Main.Version`; `(devel)` or an empty value prints `devel`.
  - `atlagen ir [flags]` takes the flags from generator-spec §1.3, with `-s`/`--schema` and `-o`/`--out` as aliases.
    - It prints the IR as `json.MarshalIndent(schema, "", "  ")` plus a newline to stdout.
    - Each warning goes to stderr as `atlagen: warning: <w>`.
  - No args, `-h`, or an unknown command prints usage listing `ir` and `version` to stderr and exits 2. A flag parse error also exits 2. A pipeline or resolve error prints `atlagen: <err>` and exits 1.

- [x] **Step 1: Write the failing tests.**
  ```go
  func TestPipelineRun(t *testing.T) {
      // composite.hcl via r.Schema (file), dialect postgres, temp module → no error;
      // memberships Model "Membership", columns GoName "OrgID"/"UserID", Kind NumberColumn, Scan direct (bigint)
  }
  func TestPipelineErrorsCarryStage(t *testing.T) { /* unmapped type → error from mapper containing the table/column context; bad HCL → parse error */ }
  func TestCLIVersion(t *testing.T) { /* run(["version"]) → exit 0, stdout "atlagen devel\n" (test binary has no module version) */ }
  func TestCLIIRGolden(t *testing.T) {
      // temp module (go.mod "module example.com/app"); run(["ir","-s",<abs fixture>,"--dialect","postgres","-o","internal/dbgen"], cwd=temp)
      // → exit 0; stdout == golden file bytes; -update flag rewrites goldens (postgres_users, composite)
  }
  func TestCLIIRStdin(t *testing.T) { /* fixture piped via stdin with --schema "-" → same output as the file variant */ }
  func TestCLIWarnings(t *testing.T) { /* sqlite_types.hcl → stderr contains "atlagen: warning: " and "FOO"; exit 0 */ }
  func TestCLIUsageErrors(t *testing.T) { /* no args, "bogus", "ir --nope" → exit 2 with usage on stderr; missing --dialect → exit 1 "atlagen: dialect is required" */ }
  func TestCLIDeterministic(t *testing.T) { /* two runs → identical stdout bytes */ }
  ```
- [x] **Step 2: Run the tests and confirm they fail.** Run: `cd atlagen && go test ./...`. Expected: FAIL.
- [x] **Step 3: Implement.** `main()` calls `os.Exit(run(signal.NotifyContext(...), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getwd()))`. Each subcommand gets its own `flag.FlagSet` with `ContinueOnError`, and only flags that were actually set are copied into `config.Flags` (via `fs.Visit`). Generate the golden files with `-update`, inspect them by hand for correctness (Go names, kinds, scan plans), then commit them.
- [x] **Step 4: Run the tests and confirm they pass.** Run: `cd atlagen && go test -race ./... && go vet ./... && golangci-lint run && gofmt -l .`, then the repo-root gate `cd .. && go test -race ./... && bash scripts/check-no-deps.sh`. Expected: all pass, no gofmt output.
- [x] **Step 5: Commit.** `git commit -m "feat(atlagen): add pipeline and atlagen ir/version commands"`

### Task 7: Docs sync and final verification

**Files:**
- Modify: `docs/generator-spec.md`:
  - §1.2/§1.3: add `atlagen ir`; state that `generate`/`check` arrive in P3.
  - §2: IR fields as implemented, per Plan Decision 4.
  - §3: sanitizing, reserved names and collision rules, per Plan Decisions 5–8.
  - §4: overrides, scan plans and SQLite affinity, per Plan Decisions 9–12.
  - §1.4 and §8: relative paths and dev-url, per Plan Decisions 14–15.
- Modify: `docs/generator-spec.md` §1.2 and §2, `docs/architecture.md` §1.1/§4.1, `docs/roadmap.md` P2 item 2 and `README.md`: the input is Atlas's default HCL output (`atlas schema inspect -u …`, no `--format`), parsed with `hashicorp/hcl/v2`; JSON input is rejected.
- Modify: `docs/roadmap.md`: P2 item 5 lists `ir`/`version`; P3 gains `generate`/`check`.
- Modify: `docs/architecture.md` §3: the generator packages live under `atlagen/internal/`.

- [x] **Step 1: Update the docs.** They must match the exported surface and the behaviour of Tasks 1–6. Write them in the present tense, describing current behaviour; no history wording.
- [x] **Step 2: Run the full verification.** Run: `cd atlagen && gofmt -l . && go vet ./... && go test -race ./... && golangci-lint run && grep '^go 1.24.0$' go.mod && cd .. && go test -race ./... && bash scripts/check-no-deps.sh && GOWORK=off sh -c 'cd atlagen && go build ./...'`. Expected: every command exits 0 with no gofmt output.
- [x] **Step 3: Commit.** `git commit -m "docs: sync generator spec and roadmap with P2 implementation"`
