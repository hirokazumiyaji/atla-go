# Atlas inspect fixtures

Each `.hcl` file is what `atlas schema inspect -u <url>` prints in its default
HCL format. Nothing was edited by hand after it was produced.

Atlas version: a community build of https://github.com/ariga/atlas at commit
`ed994a77898e48377f9a9c391f2d1c49f5703b8d` (2026-10-21).

## SQLite: real `atlas` output

`sqlite_users.hcl` and `sqlite_types.hcl` were produced by the community
`atlas` binary against real SQLite databases:

```sh
atlas schema inspect -u "sqlite://$PWD/sqlite_users.db" > sqlite_users.hcl
atlas schema inspect -u "sqlite://$PWD/sqlite_types.db" > sqlite_types.hcl
```

The databases were created with Python's `sqlite3` module:

```sql
-- sqlite_users.db
CREATE TABLE users(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  type TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- sqlite_types.db
CREATE TABLE things(
  id INTEGER PRIMARY KEY,
  created_at DATETIME NOT NULL,
  active BOOLEAN NOT NULL,
  code VARCHAR(10) NOT NULL,
  rank MEDIUMINT NOT NULL,
  price NUMERIC(10,2) NOT NULL,
  extra FOO
);
CREATE TABLE pairs(a INTEGER NOT NULL, b INTEGER NOT NULL, PRIMARY KEY(a, b));
CREATE TABLE codes(
  id BIGINT NOT NULL PRIMARY KEY,
  label TEXT NOT NULL,
  label_upper TEXT NOT NULL GENERATED ALWAYS AS (upper(label)) STORED
);
```

## Postgres and MySQL: Atlas's own marshaller, hand-built input

No Postgres or MySQL server was available. For these files, a `schema.Realm`
was built by hand in Go, shaped the way Atlas's inspectors build it
(`sql/postgres/inspect.go`, `sql/mysql/inspect.go`). It was then printed with
Atlas's own `postgres.MarshalHCL` / `mysql.MarshalHCL` from the same commit.
These are the functions `atlas schema inspect` calls for its default output
(`cmd/atlas/internal/cmdlog`, `SchemaInspect.MarshalHCL`).

The shapes used are what the inspectors produce:

- Inspected type names: `character varying` (printed `character_varying(255)`), `timestamp with time zone` (printed `timestamptz`).
- `nextval()` defaults become `bigserial` with no default.
- Identity columns carry `postgres.Identity` (printed `identity { generated = BY_DEFAULT }`).
- PG `UNIQUE` constraints carry `postgres.Constraint{T: "u"}` (printed as `unique "<name>" {}` blocks, not `index`).
- Partial-index predicates come out as `pg_get_expr` prints them (`(deleted_at IS NULL)`).
- Schemas and tables are ordered by name.
- `public` has its default comment.

| File | Covers |
| :--- | :--- |
| `postgres_users.hcl` | enum `user_role`, defaults (`"member"`, `sql("now()")`), `bigserial`, identity, a `unique` constraint, a FK |
| `typecheck_users.hcl` | a NOT NULL column without default, a unique index |
| `composite.hcl` | composite PK and FK, a plain unique index, an expression index (`on { expr }`), a partial index (`where`), a non-unique index, a generated column (`as { expr … }`) |
| `pg_types.hcl` | one column per Postgres type of generator-spec §4.1, plus `text[]`, an enum column and a nullable `integer` |
| `multi_schema.hcl` | two schemas (`audit`, `public`) and a cross-schema FK `events.log_id → audit.logs.id` |
| `mysql_users.hcl` | `auto_increment`, `varchar(n)`, a MySQL `enum(...)` type with a default, a unique index |

`pg_types.hcl` is a type catalog rather than a dump of one database. Its realm
uses the alias type names directly (`int2`, `int4`, `int8`, `int`, `float4`,
`float8`, `decimal`, `char`, `varchar`). Atlas's HCL type registry accepts
and prints these names, but a live Postgres inspect would report the
canonical names instead (`smallint`, `integer`, `bigint`, …).

The generator is `fxgen/main.go`. It is not part of the atlagen module:

- It sits under `testdata`, which `go build ./...`, `go vet ./...`, `go test ./...` and golangci-lint skip.
- It has its own `go.mod` (module `fxgen`), so it never adds Atlas to atlagen's dependencies.

To regenerate the Postgres/MySQL fixtures:

```sh
git clone https://github.com/ariga/atlas /path/to/atlas
git -C /path/to/atlas checkout ed994a77898e48377f9a9c391f2d1c49f5703b8d
cd atlagen/internal/testdata/atlas/fxgen
go mod edit -replace ariga.io/atlas=/path/to/atlas
cp /path/to/atlas/go.sum .
GOFLAGS=-mod=mod go run . ..   # writes ../<name>.hcl; needs Go 1.26 (Atlas's go directive)
```

Do not commit the edited `go.mod` or the copied `go.sum`.
