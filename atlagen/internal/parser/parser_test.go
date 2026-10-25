package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "atlas", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t *testing.T, name, dialect, schemaName string) *ir.Schema {
	t.Helper()
	s, err := Parse(readFixture(t, name), dialect, schemaName)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return s
}

func mustParse(t *testing.T, src, dialect string) *ir.Schema {
	t.Helper()
	s, err := Parse([]byte(src), dialect, "")
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, src)
	}
	return s
}

func mustTable(t *testing.T, s *ir.Schema, name string) ir.Table {
	t.Helper()
	for _, tbl := range s.Tables {
		if tbl.DBName == name {
			return tbl
		}
	}
	t.Fatalf("table %q not found", name)
	return ir.Table{}
}

func mustColumn(t *testing.T, tbl ir.Table, name string) ir.Column {
	t.Helper()
	c, ok := tbl.Column(name)
	if !ok {
		t.Fatalf("table %q: column %q not found", tbl.DBName, name)
	}
	return c
}

func tableNames(s *ir.Schema) []string {
	names := make([]string, 0, len(s.Tables))
	for _, tbl := range s.Tables {
		names = append(names, tbl.DBName)
	}
	return names
}

func equal(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

// inlineTables wraps table blocks in a document with a single "main" schema.
func inlineTables(tables string) string {
	return tables + "\nschema \"main\" {\n}\n"
}

// inlineTable builds a one-table document; body is the table body without
// the schema attribute.
func inlineTable(name, body string) string {
	return inlineTables("table \"" + name + "\" {\n  schema = schema.main\n" + body + "\n}")
}

func TestParsePostgresUsers(t *testing.T) {
	s := parseFixture(t, "postgres_users.hcl", "postgres", "")

	equal(t, "Name", s.Name, "public")
	equal(t, "Dialect", s.Dialect, "postgres")
	equal(t, "Enums", s.Enums, []ir.Enum{{
		DBName: "user_role",
		Values: []ir.EnumValue{{DB: "admin"}, {DB: "member"}, {DB: "guest"}},
	}})
	equal(t, "tables", tableNames(s), []string{"posts", "users"})

	users := mustTable(t, s, "users")
	equal(t, "users.PrimaryKey", users.PrimaryKey, []string{"id"})
	equal(t, "users.Uniques", users.Uniques, [][]string{{"email"}})
	equal(t, "users.ForeignKeys", users.ForeignKeys, []ir.ForeignKey{})

	id := mustColumn(t, users, "id")
	equal(t, "users.id", id, ir.Column{DBName: "id", RawType: "bigserial", HasDefault: true, InPK: true})

	email := mustColumn(t, users, "email")
	equal(t, "users.email", email, ir.Column{DBName: "email", RawType: "character varying(255)"})
	if !email.RequiredForInsert() {
		t.Error("users.email: RequiredForInsert = false, want true")
	}

	role := mustColumn(t, users, "role")
	equal(t, "users.role", role, ir.Column{DBName: "role", RawType: "user_role", Enum: "user_role", HasDefault: true})
	if role.RequiredForInsert() {
		t.Error("users.role: RequiredForInsert = true, want false")
	}

	from := mustColumn(t, users, "from")
	equal(t, "users.from", from, ir.Column{DBName: "from", RawType: "text", Nullable: true})
	if from.RequiredForInsert() {
		t.Error("users.from: RequiredForInsert = true, want false")
	}

	equal(t, "users.created_at", mustColumn(t, users, "created_at"),
		ir.Column{DBName: "created_at", RawType: "timestamptz", HasDefault: true})

	var colNames []string
	for _, c := range users.Columns {
		colNames = append(colNames, c.DBName)
	}
	equal(t, "users columns (Atlas order)", colNames, []string{"id", "email", "role", "from", "created_at"})

	posts := mustTable(t, s, "posts")
	equal(t, "posts.id.HasDefault (identity)", mustColumn(t, posts, "id").HasDefault, true)
	equal(t, "posts.user_id.HasDefault", mustColumn(t, posts, "user_id").HasDefault, false)
	equal(t, "posts.Uniques", posts.Uniques, [][]string{})
	equal(t, "posts.ForeignKeys", posts.ForeignKeys, []ir.ForeignKey{{
		Symbol:     "posts_user_id_fkey",
		Columns:    []string{"user_id"},
		RefSchema:  "",
		RefTable:   "users",
		RefColumns: []string{"id"},
	}})

	if _, ok := posts.Column("nope"); ok {
		t.Error(`posts.Column("nope") found a column`)
	}
}

func TestParseLeavesGoFieldsZero(t *testing.T) {
	for _, f := range []string{"postgres_users.hcl", "pg_types.hcl", "composite.hcl"} {
		s := parseFixture(t, f, "postgres", "")
		for _, e := range s.Enums {
			if e.GoName != "" {
				t.Errorf("%s: enum %q GoName = %q", f, e.DBName, e.GoName)
			}
			for _, v := range e.Values {
				if v.Go != "" {
					t.Errorf("%s: enum %q value %q Go = %q", f, e.DBName, v.DB, v.Go)
				}
			}
		}
		for _, tbl := range s.Tables {
			if tbl.Model != "" || tbl.Package != "" {
				t.Errorf("%s: table %q Model/Package set", f, tbl.DBName)
			}
			for _, c := range tbl.Columns {
				if c.GoName != "" || c.GoType != (ir.GoType{}) || c.Kind != "" || c.Scan != "" {
					t.Errorf("%s: table %q column %q has Go fields set: %#v", f, tbl.DBName, c.DBName, c)
				}
			}
		}
	}
}

func TestParseDeterministic(t *testing.T) {
	data := readFixture(t, "composite.hcl")
	var dumps [2]string
	for i := range dumps {
		s, err := Parse(data, "postgres", "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		dumps[i] = string(b)
	}
	if dumps[0] != dumps[1] {
		t.Error("two parses produced different IR JSON")
	}
	if strings.Contains(dumps[0], ":null") {
		t.Errorf("IR JSON contains null (slices must be non-nil): %s", dumps[0])
	}
}

func TestParseDialectFixtures(t *testing.T) {
	t.Run("sqlite_users", func(t *testing.T) {
		s := parseFixture(t, "sqlite_users.hcl", "sqlite", "")
		equal(t, "Name", s.Name, "main")
		users := mustTable(t, s, "users")
		// Atlas prints null = true for the INTEGER PRIMARY KEY; PK columns are never nullable.
		equal(t, "users.id", mustColumn(t, users, "id"), ir.Column{DBName: "id", RawType: "integer", HasDefault: true, InPK: true})
		equal(t, "users.email.HasDefault", mustColumn(t, users, "email").HasDefault, false)
		equal(t, "users.type.Nullable", mustColumn(t, users, "type").Nullable, true)
		equal(t, "users.created_at.HasDefault", mustColumn(t, users, "created_at").HasDefault, true)
		equal(t, "users.Uniques", users.Uniques, [][]string{{"email"}})
		equal(t, "Enums", s.Enums, []ir.Enum{})
	})

	t.Run("mysql_users", func(t *testing.T) {
		s := parseFixture(t, "mysql_users.hcl", "mysql", "")
		equal(t, "Name", s.Name, "app")
		equal(t, "tables", tableNames(s), []string{"categories", "users"})
		users := mustTable(t, s, "users")
		equal(t, "users.id.HasDefault", mustColumn(t, users, "id").HasDefault, true)
		equal(t, "users.email.HasDefault", mustColumn(t, users, "email").HasDefault, false)
		equal(t, "users.class.Nullable", mustColumn(t, users, "class").Nullable, true)
		equal(t, "users.status", mustColumn(t, users, "status"),
			ir.Column{DBName: "status", RawType: "enum('active','inactive','banned')", HasDefault: true})
		equal(t, "users.Uniques", users.Uniques, [][]string{{"email"}})
		categories := mustTable(t, s, "categories")
		equal(t, "categories.id.HasDefault", mustColumn(t, categories, "id").HasDefault, true)
		equal(t, "categories.name.HasDefault", mustColumn(t, categories, "name").HasDefault, false)
		equal(t, "categories.name.RawType", mustColumn(t, categories, "name").RawType, "varchar(100)")
		equal(t, "categories.PrimaryKey", categories.PrimaryKey, []string{"id"})
		equal(t, "categories.id.InPK", mustColumn(t, categories, "id").InPK, true)
	})

	t.Run("typecheck_users", func(t *testing.T) {
		s := parseFixture(t, "typecheck_users.hcl", "postgres", "")
		users := mustTable(t, s, "users")
		equal(t, "users.age.RequiredForInsert", mustColumn(t, users, "age").RequiredForInsert(), true)
		equal(t, "users.id.RequiredForInsert", mustColumn(t, users, "id").RequiredForInsert(), false)
		equal(t, "users.Uniques", users.Uniques, [][]string{{"email"}})
	})
}

func TestParseHCLVariants(t *testing.T) {
	hasDefault := []struct {
		name string
		col  string
		want bool
	}{
		{"default number", `type = integer` + "\n" + `default = 0`, true},
		{"default string", `type = text` + "\n" + `default = "bob"`, true},
		{"default sql", `type = timestamptz` + "\n" + `default = sql("CURRENT_TIMESTAMP")`, true},
		{"auto_increment true", `type = int` + "\n" + `auto_increment = true`, true},
		{"auto_increment false", `type = int` + "\n" + `auto_increment = false`, false},
		{"identity by default", "type = bigint\nidentity {\n  generated = BY_DEFAULT\n}", true},
		{"identity always", "type = bigint\nidentity {\n  generated = ALWAYS\n  start = 10\n}", true},
		{"serial4", `type = serial4`, true},
		{"smallserial", `type = smallserial`, true},
		{"plain", `type = int`, false},
	}
	for _, tc := range hasDefault {
		t.Run("has_default/"+tc.name, func(t *testing.T) {
			s := mustParse(t, inlineTable("t", "column \"c\" {\nnull = false\n"+tc.col+"\n}"), "mysql")
			equal(t, "HasDefault", s.Tables[0].Columns[0].HasDefault, tc.want)
		})
	}

	rawType := []struct {
		name string
		typ  string
		want string
	}{
		{"identifier", `integer`, "integer"},
		{"call", `varchar(255)`, "varchar(255)"},
		{"call two args", `numeric(10, 2)`, "numeric(10,2)"},
		{"sql", `sql("geometry(Point,4326)")`, "geometry(Point,4326)"},
		{"sql array", `sql("text[]")`, "text[]"},
		{"underscore identifier", `double_precision`, "double precision"},
		{"underscore call", `character_varying(255)`, "character varying(255)"},
		{"bit varying", `bit_varying(8)`, "bit varying(8)"},
		{"time precision", `timestamptz(3)`, "timestamptz(3)"},
		{"mysql enum", `enum("a","it's")`, "enum('a','it''s')"},
		{"interval", `interval`, "interval"},
		{"interval field", `day_to_second`, "interval day to second"},
		{"interval field precision", `second(3)`, "interval second(3)"},
		{"domain reference", `domain.email_address`, "email_address"},
	}
	for _, tc := range rawType {
		t.Run("raw_type/"+tc.name, func(t *testing.T) {
			s := mustParse(t, inlineTable("t", "column \"c\" {\nnull = false\ntype = "+tc.typ+"\n}"), "postgres")
			c := s.Tables[0].Columns[0]
			equal(t, "RawType", c.RawType, tc.want)
			equal(t, "Enum", c.Enum, "")
		})
	}

	t.Run("interval fields are postgres-only", func(t *testing.T) {
		s := mustParse(t, inlineTable("t", "column \"c\" {\ntype = year\n}"), "mysql")
		equal(t, "RawType", s.Tables[0].Columns[0].RawType, "year")
	})

	t.Run("enum type", func(t *testing.T) {
		s := mustParse(t, `
table "t" {
  schema = schema.public
  column "role" {
    null = false
    type = enum.user_role
  }
  column "tags" {
    null = true
    type = sql("user_role[]")
  }
}
enum "user_role" {
  schema = schema.public
  values = ["admin", "member"]
}
schema "public" {
}
`, "postgres")
		tbl := s.Tables[0]
		equal(t, "role", mustColumn(t, tbl, "role"), ir.Column{DBName: "role", RawType: "user_role", Enum: "user_role"})
		equal(t, "tags", mustColumn(t, tbl, "tags"), ir.Column{DBName: "tags", RawType: "user_role[]", Nullable: true})
		equal(t, "Enums", s.Enums, []ir.Enum{{DBName: "user_role", Values: []ir.EnumValue{{DB: "admin"}, {DB: "member"}}}})
	})

	t.Run("null", func(t *testing.T) {
		s := mustParse(t, inlineTable("t", `
column "a" {
  null = true
  type = text
}
column "b" {
  null = false
  type = text
}
column "c" {
  type = text
}`), "postgres")
		tbl := s.Tables[0]
		equal(t, "a.Nullable", mustColumn(t, tbl, "a").Nullable, true)
		equal(t, "b.Nullable", mustColumn(t, tbl, "b").Nullable, false)
		equal(t, "c.Nullable", mustColumn(t, tbl, "c").Nullable, false)
	})

	t.Run("primary key forms", func(t *testing.T) {
		for _, pk := range []string{
			"primary_key {\n  columns = [column.a, column.b]\n}",
			"primary_key {\n  on {\n    column = column.a\n  }\n  on {\n    desc = true\n    column = column.b\n  }\n}",
		} {
			s := mustParse(t, inlineTable("t", `
column "a" {
  type = int
}
column "b" {
  type = int
}
column "x" {
  null = true
  type = int
}
`+pk), "postgres")
			tbl := s.Tables[0]
			equal(t, pk+" PrimaryKey", tbl.PrimaryKey, []string{"a", "b"})
			equal(t, pk+" a.InPK", mustColumn(t, tbl, "a").InPK, true)
			equal(t, pk+" x.InPK", mustColumn(t, tbl, "x").InPK, false)
		}
	})

	t.Run("no primary key", func(t *testing.T) {
		s := mustParse(t, inlineTable("t", "column \"a\" {\ntype = int\n}"), "postgres")
		equal(t, "PrimaryKey", s.Tables[0].PrimaryKey, []string{})
	})

	t.Run("nullable pk column is not nullable", func(t *testing.T) {
		s := mustParse(t, inlineTable("t", "column \"a\" {\nnull = true\ntype = text\n}\nprimary_key {\ncolumns = [column.a]\n}"), "sqlite")
		equal(t, "a.Nullable", s.Tables[0].Columns[0].Nullable, false)
	})

	t.Run("uniques", func(t *testing.T) {
		s := mustParse(t, inlineTable("t", `
column "a" {
  type = int
}
column "b" {
  type = int
}
index "i_columns" {
  unique  = true
  columns = [column.a, column.b]
}
index "i_on" {
  unique = true
  on {
    column = column.a
  }
  on {
    column = column.b
  }
}
index "i_desc" {
  unique = true
  on {
    desc   = true
    column = column.b
  }
}
index "i_expr" {
  unique = true
  on {
    expr = "lower(a)"
  }
}
index "i_mixed" {
  unique = true
  on {
    column = column.b
  }
  on {
    expr = "a + 1"
  }
}
index "i_where" {
  unique  = true
  columns = [column.a]
  where   = "a > 0"
}
index "i_not_unique" {
  columns = [column.a]
}
index "i_unique_false" {
  unique  = false
  columns = [column.b]
}
index "i_bracket" {
  unique  = true
  columns = [column["a"]]
}
unique "u_constraint" {
  columns = [column.b]
}`), "postgres")
		equal(t, "Uniques", s.Tables[0].Uniques, [][]string{{"a", "b"}, {"a", "b"}, {"b"}, {"a"}, {"b"}})
	})

	t.Run("foreign keys", func(t *testing.T) {
		s := mustParse(t, inlineTables(`
table "parents" {
  schema = schema.main
  column "id" {
    type = int
  }
  column "2fa" {
    type = int
  }
}
table "children" {
  schema = schema.main
  column "id" {
    type = int
  }
  column "p" {
    type = int
  }
  column "q" {
    type = int
  }
  foreign_key "fk_parent" {
    columns     = [column.p]
    ref_columns = [table.parents.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
  foreign_key "fk_pair" {
    columns     = [column.p, column.q]
    ref_columns = [table.parents.column.id, table.parents.column["2fa"]]
  }
  foreign_key "fk_self" {
    columns     = [column.q]
    ref_columns = [column.id]
  }
  foreign_key "fk_qualified_self_schema" {
    columns     = [column.p]
    ref_columns = [table.main.parents.column.id]
  }
}`), "postgres")
		children := mustTable(t, s, "children")
		equal(t, "children.ForeignKeys", children.ForeignKeys, []ir.ForeignKey{
			{Symbol: "fk_parent", Columns: []string{"p"}, RefTable: "parents", RefColumns: []string{"id"}},
			{Symbol: "fk_pair", Columns: []string{"p", "q"}, RefTable: "parents", RefColumns: []string{"id", "2fa"}},
			{Symbol: "fk_self", Columns: []string{"q"}, RefTable: "children", RefColumns: []string{"id"}},
			{Symbol: "fk_qualified_self_schema", Columns: []string{"p"}, RefTable: "parents", RefColumns: []string{"id"}},
		})
	})

	t.Run("ignored blocks and attributes", func(t *testing.T) {
		s := mustParse(t, `
table "t" {
  schema  = schema.public
  comment = "a table"
  column "a" {
    null    = false
    type    = int
    comment = "a column"
  }
  check "positive" {
    expr = "(a > 0)"
  }
}
view "v" {
  schema = schema.public
  column "a" {
    type = int
  }
  as = "SELECT a FROM t"
}
sequence "s" {
  schema = schema.public
}
function "f" {
  schema = schema.public
}
extension "citext" {
  schema = schema.public
}
schema "public" {
  comment = "standard public schema"
}
`, "postgres")
		equal(t, "tables", tableNames(s), []string{"t"})
	})
}

func TestParseRejectsJSON(t *testing.T) {
	for _, in := range []string{`{"schemas":[]}`, " \n\t{}"} {
		s, err := Parse([]byte(in), "postgres", "")
		if err == nil {
			t.Fatalf("Parse(%q) succeeded with %#v, want error", in, s)
		}
		for _, want := range []string{"input looks like JSON", "atlas schema inspect", "--format"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	}
}

func TestParseSyntaxError(t *testing.T) {
	_, err := Parse([]byte("schema \"main\" {\n}\ntable \"t\" {"), "postgres", "")
	if err == nil {
		t.Fatal("Parse succeeded, want error")
	}
	if !strings.Contains(err.Error(), "schema.hcl:3:") {
		t.Errorf("error %q does not contain file:line:col %q", err, "schema.hcl:3:")
	}
}

func TestParseRefSchemaNormalized(t *testing.T) {
	// Atlas qualifies table labels and references with the schema when two
	// schemas hold a table with the same name.
	src := `
table "audit" "users" {
  schema = schema.audit
  column "id" {
    null = false
    type = bigint
  }
  primary_key {
    columns = [column.id]
  }
}
table "public" "users" {
  schema = schema.public
  column "id" {
    null = false
    type = bigint
  }
  column "a" {
    null = false
    type = bigint
  }
  column "b" {
    null = false
    type = bigint
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "self" {
    columns     = [column.a]
    ref_columns = [table.public.users.column.id]
  }
  foreign_key "x" {
    columns     = [column.b]
    ref_columns = [table.audit.users.column.id]
  }
}
schema "audit" {
}
schema "public" {
}
`
	s, err := Parse([]byte(src), "postgres", "public")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "tables", tableNames(s), []string{"users"})
	equal(t, "users.ForeignKeys", s.Tables[0].ForeignKeys, []ir.ForeignKey{
		{Symbol: "self", Columns: []string{"a"}, RefSchema: "", RefTable: "users", RefColumns: []string{"id"}},
		{Symbol: "x", Columns: []string{"b"}, RefSchema: "audit", RefTable: "users", RefColumns: []string{"id"}},
	})

	audit, err := Parse([]byte(src), "postgres", "audit")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "audit users columns", len(audit.Tables[0].Columns), 1)

	// An unqualified reference to a table of another schema (multi_schema.hcl).
	public := parseFixture(t, "multi_schema.hcl", "postgres", "public")
	equal(t, "events.ForeignKeys", mustTable(t, public, "events").ForeignKeys, []ir.ForeignKey{{
		Symbol:     "events_log_id_fkey",
		Columns:    []string{"log_id"},
		RefSchema:  "audit",
		RefTable:   "logs",
		RefColumns: []string{"id"},
	}})
}

func TestParseRefExternal(t *testing.T) {
	// A single-schema inspect (search_path=public) of public.profiles with an
	// FK to auth.users: Atlas stubs the target and prints it unqualified, and
	// no users table appears anywhere in the document.
	s := mustParse(t, `
table "profiles" {
  schema = schema.public
  column "id" {
    null = false
    type = uuid
  }
  column "user_id" {
    null = false
    type = uuid
  }
  column "org_id" {
    null = false
    type = uuid
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "profiles_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = CASCADE
  }
  foreign_key "profiles_org_id_fkey" {
    columns     = [column.org_id]
    ref_columns = [table.orgs.column.id]
  }
  foreign_key "profiles_self_fkey" {
    columns     = [column.org_id]
    ref_columns = [column.id]
  }
}
table "orgs" {
  schema = schema.public
  column "id" {
    null = false
    type = uuid
  }
}
schema "public" {
}
`, "postgres")
	equal(t, "profiles.ForeignKeys", mustTable(t, s, "profiles").ForeignKeys, []ir.ForeignKey{
		{Symbol: "profiles_user_id_fkey", Columns: []string{"user_id"}, RefSchema: "", RefTable: "users", RefColumns: []string{"id"}, RefExternal: true},
		{Symbol: "profiles_org_id_fkey", Columns: []string{"org_id"}, RefSchema: "", RefTable: "orgs", RefColumns: []string{"id"}},
		{Symbol: "profiles_self_fkey", Columns: []string{"org_id"}, RefSchema: "", RefTable: "profiles", RefColumns: []string{"id"}},
	})

	// A table of another schema block is not external (multi_schema.hcl).
	public := parseFixture(t, "multi_schema.hcl", "postgres", "public")
	fk := mustTable(t, public, "events").ForeignKeys[0]
	equal(t, "events fk RefSchema", fk.RefSchema, "audit")
	equal(t, "events fk RefExternal", fk.RefExternal, false)

	// Same-schema references in the fixtures are not external.
	for _, tbl := range parseFixture(t, "composite.hcl", "postgres", "").Tables {
		for _, fk := range tbl.ForeignKeys {
			equal(t, tbl.DBName+"."+fk.Symbol+".RefExternal", fk.RefExternal, false)
		}
	}
}

func TestParseEnumExactMatch(t *testing.T) {
	s := mustParse(t, `
table "t" {
  schema = schema.public
  column "a" {
    null = false
    type = enum.mood
  }
  column "b" {
    null = false
    type = enum.Mood
  }
  column "c" {
    null = false
    type = enum.public.mood
  }
  column "d" {
    null = false
    type = text
  }
}
enum "Mood" {
  schema = schema.public
  values = ["HAPPY"]
}
enum "mood" {
  schema = schema.public
  values = ["happy", "sad"]
}
enum "empty_enum" {
  schema = schema.public
  values = []
}
schema "public" {
}
`, "postgres")
	tbl := s.Tables[0]
	equal(t, "a.Enum", mustColumn(t, tbl, "a").Enum, "mood")
	equal(t, "b.Enum", mustColumn(t, tbl, "b").Enum, "Mood")
	equal(t, "c.Enum", mustColumn(t, tbl, "c").Enum, "mood")
	equal(t, "d.Enum", mustColumn(t, tbl, "d").Enum, "")
	equal(t, "Enums", s.Enums, []ir.Enum{
		{DBName: "Mood", Values: []ir.EnumValue{{DB: "HAPPY"}}},
		{DBName: "mood", Values: []ir.EnumValue{{DB: "happy"}, {DB: "sad"}}},
		{DBName: "empty_enum", Values: []ir.EnumValue{}},
	})
}

func TestParseEnumsOfOtherSchemas(t *testing.T) {
	src := `
table "t" {
  schema = schema.public
  column "local" {
    null = false
    type = enum.public.mood
  }
  column "foreign" {
    null = false
    type = enum.audit.mood
  }
  column "unique_foreign" {
    null = false
    type = enum.level
  }
}
enum "audit" "mood" {
  schema = schema.audit
  values = ["a"]
}
enum "level" {
  schema = schema.audit
  values = ["low"]
}
enum "public" "mood" {
  schema = schema.public
  values = ["b"]
}
schema "audit" {
}
schema "public" {
}
`
	s, err := Parse([]byte(src), "postgres", "public")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "Enums", s.Enums, []ir.Enum{{DBName: "mood", Values: []ir.EnumValue{{DB: "b"}}}})
	tbl := s.Tables[0]
	equal(t, "local", mustColumn(t, tbl, "local"), ir.Column{DBName: "local", RawType: "mood", Enum: "mood"})
	// An enum of another schema is not part of the IR: the column keeps the
	// qualified type name and no Enum.
	equal(t, "foreign", mustColumn(t, tbl, "foreign"), ir.Column{DBName: "foreign", RawType: "audit.mood"})
	equal(t, "unique_foreign", mustColumn(t, tbl, "unique_foreign"), ir.Column{DBName: "unique_foreign", RawType: "audit.level"})
}

func TestParsePGTypes(t *testing.T) {
	s := parseFixture(t, "pg_types.hcl", "postgres", "")
	tbl := mustTable(t, s, "t")
	want := map[string]string{
		"c_smallint": "smallint", "c_int2": "int2", "c_smallserial": "smallserial",
		"c_integer": "integer", "c_int": "int", "c_int4": "int4", "c_serial": "serial",
		"c_bigint": "bigint", "c_int8": "int8", "c_bigserial": "bigserial",
		"c_real": "real", "c_float4": "float4", "c_double_precision": "double precision", "c_float8": "float8",
		"c_numeric": "numeric", "c_decimal": "decimal", "c_money": "money", "c_boolean": "boolean",
		"c_text": "text", "c_varchar": "varchar", "c_char": "char", "c_citext": "citext",
		"c_inet": "inet", "c_cidr": "cidr", "c_macaddr": "macaddr", "c_bytea": "bytea", "c_uuid": "uuid",
		"c_date": "date", "c_timestamp": "timestamp", "c_timestamptz": "timestamptz",
		"c_time": "time", "c_timetz": "timetz", "c_interval": "interval",
		"c_json": "json", "c_jsonb": "jsonb", "tags": "text[]", "role": "user_role", "nullable_int": "integer",
	}
	equal(t, "column count", len(tbl.Columns), len(want))
	for _, c := range tbl.Columns {
		if w, ok := want[c.DBName]; !ok || c.RawType != w {
			t.Errorf("%s.RawType = %q, want %q", c.DBName, c.RawType, w)
		}
	}
	equal(t, "tags.Enum", mustColumn(t, tbl, "tags").Enum, "")
	equal(t, "role.Enum", mustColumn(t, tbl, "role").Enum, "user_role")
	equal(t, "nullable_int", mustColumn(t, tbl, "nullable_int"), ir.Column{DBName: "nullable_int", RawType: "integer", Nullable: true})
	equal(t, "c_integer.HasDefault", mustColumn(t, tbl, "c_integer").HasDefault, false)
	equal(t, "c_serial.HasDefault", mustColumn(t, tbl, "c_serial").HasDefault, true)
	equal(t, "c_smallserial.HasDefault", mustColumn(t, tbl, "c_smallserial").HasDefault, true)
	equal(t, "PrimaryKey", tbl.PrimaryKey, []string{"c_bigserial"})
}

func TestParseComposite(t *testing.T) {
	s := parseFixture(t, "composite.hcl", "postgres", "")
	equal(t, "tables", tableNames(s), []string{"membership_notes", "memberships", "orgs", "users"})

	orgs := mustTable(t, s, "orgs")
	equal(t, "orgs.PrimaryKey", orgs.PrimaryKey, []string{"id"})

	m := mustTable(t, s, "memberships")
	equal(t, "memberships.PrimaryKey", m.PrimaryKey, []string{"org_id", "user_id"})
	equal(t, "memberships.org_id.InPK", mustColumn(t, m, "org_id").InPK, true)
	equal(t, "memberships.user_id.InPK", mustColumn(t, m, "user_id").InPK, true)
	equal(t, "memberships.slug.InPK", mustColumn(t, m, "slug").InPK, false)
	equal(t, "memberships.role.HasDefault", mustColumn(t, m, "role").HasDefault, true)
	equal(t, "memberships.org_id.RequiredForInsert", mustColumn(t, m, "org_id").RequiredForInsert(), true)
	// Only the plain 2-column unique index qualifies: the expression index,
	// the partial index and the non-unique index are excluded.
	equal(t, "memberships.Uniques", m.Uniques, [][]string{{"org_id", "slug"}})
	equal(t, "memberships.ForeignKeys", m.ForeignKeys, []ir.ForeignKey{
		{Symbol: "memberships_org_id_fkey", Columns: []string{"org_id"}, RefTable: "orgs", RefColumns: []string{"id"}},
		{Symbol: "memberships_user_id_fkey", Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}},
	})

	notes := mustTable(t, s, "membership_notes")
	equal(t, "membership_notes.ForeignKeys", notes.ForeignKeys, []ir.ForeignKey{{
		Symbol:     "membership_notes_membership_fkey",
		Columns:    []string{"org_id", "user_id"},
		RefTable:   "memberships",
		RefColumns: []string{"org_id", "user_id"},
	}})
}

func TestParseSQLiteRowidAlias(t *testing.T) {
	s := parseFixture(t, "sqlite_types.hcl", "sqlite", "")
	things := mustTable(t, s, "things")
	// Atlas prints the rowid alias with null = true and no auto_increment.
	equal(t, "things.id", mustColumn(t, things, "id"), ir.Column{DBName: "id", RawType: "integer", HasDefault: true, InPK: true})
	for _, name := range []string{"created_at", "active", "code", "rank", "price"} {
		equal(t, "things."+name+".HasDefault", mustColumn(t, things, name).HasDefault, false)
	}
	equal(t, "things.extra", mustColumn(t, things, "extra"), ir.Column{DBName: "extra", RawType: "FOO", Nullable: true})
	equal(t, "things.price.RawType", mustColumn(t, things, "price").RawType, "numeric(10,2)")
	equal(t, "things.code.RawType", mustColumn(t, things, "code").RawType, "varchar(10)")

	// Composite INTEGER PKs and non-INTEGER single-column PKs are not rowid aliases.
	pairs := mustTable(t, s, "pairs")
	equal(t, "pairs.a.HasDefault", mustColumn(t, pairs, "a").HasDefault, false)
	codes := mustTable(t, s, "codes")
	equal(t, "codes.id.HasDefault", mustColumn(t, codes, "id").HasDefault, false)

	// A WITHOUT ROWID table (printed without_rowid = true) has no rowid to alias.
	kvID := mustColumn(t, mustTable(t, s, "kv"), "id")
	equal(t, "kv.id", kvID, ir.Column{DBName: "id", RawType: "integer", InPK: true})
	equal(t, "kv.id.RequiredForInsert", kvID.RequiredForInsert(), true)

	// The rule is SQLite-only.
	pg := parseFixture(t, "sqlite_types.hcl", "postgres", "")
	equal(t, "postgres things.id.HasDefault", mustColumn(t, mustTable(t, pg, "things"), "id").HasDefault, false)
}

func TestParseSchemaSelection(t *testing.T) {
	// Atlas prints schema blocks ordered by name ("audit" first); without a
	// schema name, "public" wins over the order.
	s := parseFixture(t, "multi_schema.hcl", "postgres", "")
	equal(t, "default Name", s.Name, "public")
	equal(t, "default tables", tableNames(s), []string{"events"})

	audit := parseFixture(t, "multi_schema.hcl", "postgres", "audit")
	equal(t, "audit Name", audit.Name, "audit")
	equal(t, "audit tables", tableNames(audit), []string{"logs"})

	public := parseFixture(t, "multi_schema.hcl", "postgres", "public")
	equal(t, "public Name", public.Name, "public")
	equal(t, "public tables", tableNames(public), []string{"events"})

	_, err := Parse(readFixture(t, "multi_schema.hcl"), "postgres", "nope")
	if err == nil {
		t.Fatal("Parse with unknown schema: want error")
	}
	for _, want := range []string{`schema "nope" not found`, `available: audit, public`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestParseSchemaSelectionFallbacks(t *testing.T) {
	doc := func(schemas ...string) []byte {
		var b strings.Builder
		for _, name := range schemas {
			b.WriteString("table \"t\" {\n  schema = schema." + name + "\n  column \"" + name + "_col\" {\n    type = int\n  }\n}\n")
		}
		for _, name := range schemas {
			b.WriteString("schema \"" + name + "\" {\n}\n")
		}
		return []byte(b.String())
	}
	for _, tc := range []struct {
		name    string
		schemas []string
		want    string
	}{
		{"only schema", []string{"app"}, "app"},
		{"public over others", []string{"a", "main", "public"}, "public"},
		{"main over others", []string{"a", "main", "z"}, "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(doc(tc.schemas...), "postgres", "")
			if err != nil {
				t.Fatal(err)
			}
			equal(t, "Name", s.Name, tc.want)
			equal(t, "columns", s.Tables[0].Columns[0].DBName, tc.want+"_col")
		})
	}

	t.Run("ambiguous", func(t *testing.T) {
		s, err := Parse(doc("a", "b"), "postgres", "")
		if err == nil {
			t.Fatalf("Parse succeeded with %#v, want error", s)
		}
		for _, want := range []string{"several schemas", "a, b", "schema_name", "--schema-name"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	})

	t.Run("explicit name wins", func(t *testing.T) {
		s, err := Parse(doc("a", "b"), "postgres", "b")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "Name", s.Name, "b")
	})
}

func TestParseGeneratedColumns(t *testing.T) {
	check := func(t *testing.T, c ir.Column) {
		t.Helper()
		equal(t, c.DBName+".Generated", c.Generated, true)
		equal(t, c.DBName+".HasDefault", c.HasDefault, false)
		equal(t, c.DBName+".Nullable", c.Nullable, false)
		equal(t, c.DBName+".RequiredForInsert", c.RequiredForInsert(), false)
	}
	t.Run("postgres fixture", func(t *testing.T) {
		m := mustTable(t, parseFixture(t, "composite.hcl", "postgres", ""), "memberships")
		check(t, mustColumn(t, m, "slug_lower"))
		equal(t, "slug.Generated", mustColumn(t, m, "slug").Generated, false)
		equal(t, "slug.RequiredForInsert", mustColumn(t, m, "slug").RequiredForInsert(), true)
	})
	t.Run("sqlite fixture", func(t *testing.T) {
		codes := mustTable(t, parseFixture(t, "sqlite_types.hcl", "sqlite", ""), "codes")
		check(t, mustColumn(t, codes, "label_upper"))
		equal(t, "label.Generated", mustColumn(t, codes, "label").Generated, false)
	})
	t.Run("as attribute", func(t *testing.T) {
		s := mustParse(t, inlineTable("t", "column \"a\" {\nnull = false\ntype = int\n}\ncolumn \"b\" {\nnull = false\ntype = int\nas = \"a + 1\"\n}"), "mysql")
		check(t, mustColumn(t, s.Tables[0], "b"))
	})
}

func TestParseErrors(t *testing.T) {
	col := func(name string) string { return "column \"" + name + "\" {\n  type = int\n}\n" }
	tests := []struct {
		name       string
		data       string
		schemaName string
		want       string
	}{
		{"empty input", ``, "", "atlas hcl has no schemas"},
		{"no schema blocks", `table "t" {` + "\n" + `schema = schema.main` + "\n}", "", "atlas hcl has no schemas"},
		{"no tables", `schema "x" {` + "\n}", "", `schema "x" has no tables`},
		{"no tables selected", inlineTable("t", col("c")) + "schema \"x\" {\n}\n", "x", `schema "x" has no tables`},
		{"enum without name", inlineTable("t", col("c")) + "enum \"\" {\nschema = schema.main\nvalues = [\"a\"]\n}", "", "enum at index 0 has no name"},
		{"enum values not strings", inlineTable("t", col("c")) + "enum \"e\" {\nschema = schema.main\nvalues = [1]\n}", "", `enum "e": values must be a list of strings`},
		{"enum values missing", inlineTable("t", col("c")) + "enum \"e\" {\nschema = schema.main\n}", "", `enum "e": values must be a list of strings`},
		{"duplicate enum", inlineTable("t", col("c")) + "enum \"e\" {\nschema = schema.main\nvalues = []\n}\nenum \"e\" {\nschema = schema.main\nvalues = []\n}", "", `duplicate enum "e"`},
		{"enum without schema", inlineTable("t", col("c")) + "enum \"e\" {\nvalues = []\n}", "", `enum "e": missing schema reference`},
		{"table without name", inlineTables("table {\nschema = schema.main\n" + col("c") + "}"), "", "table at index 0 has no name"},
		{"table without schema", inlineTables("table \"users\" {\n" + col("c") + "}"), "", `table "users": missing schema reference`},
		{"table bad schema ref", inlineTables("table \"users\" {\nschema = \"main\"\n" + col("c") + "}"), "", `table "users": schema: expected a reference like schema.<name>`},
		{"column without name", inlineTable("users", col("a")+"column \"\" {\ntype = int\n}"), "", `table "users": column at index 1 has no name`},
		{"column without type", inlineTable("users", "column \"age\" {\nnull = false\n}"), "", `table "users": column "age": missing type`},
		{"column bad type", inlineTable("users", "column \"age\" {\ntype = \"int\"\n}"), "", `table "users": column "age": unsupported type expression`},
		{"column bad type arg", inlineTable("users", "column \"age\" {\ntype = varchar(n)\n}"), "", `table "users": column "age": unsupported type expression`},
		{"column bad null", inlineTable("users", "column \"age\" {\ntype = int\nnull = \"yes\"\n}"), "", `table "users": column "age": null:`},
		{"column unknown enum", inlineTable("users", "column \"r\" {\ntype = enum.role\n}"), "", `table "users": column "r": unknown enum "role"`},
		{"duplicate column", inlineTable("users", col("a")+col("a")), "", `table "users": duplicate column "a"`},
		{"duplicate table", inlineTables("table \"users\" {\nschema = schema.main\n" + col("a") + "}\ntable \"users\" {\nschema = schema.main\n" + col("a") + "}"), "", `duplicate table "users"`},
		{"pk unknown column", inlineTable("users", col("id")+"primary_key {\ncolumns = [column.uid]\n}"), "", `table "users": primary key: unknown column "uid"`},
		{"pk bad reference", inlineTable("users", col("id")+"primary_key {\non {\nexpr = \"id + 1\"\n}\n}"), "", `table "users": primary key: unrecognized column reference`},
		{"pk bad columns", inlineTable("users", col("id")+"primary_key {\ncolumns = [\"id\"]\n}"), "", `table "users": primary key: unrecognized column reference`},
		{"fk unknown column", inlineTable("posts", col("user_id")+"foreign_key \"posts_fk\" {\ncolumns = [column.author_id]\nref_columns = [table.users.column.id]\n}"), "", `table "posts": foreign key "posts_fk": unknown column "author_id"`},
		{"fk without ref columns", inlineTable("posts", col("user_id")+"foreign_key \"posts_fk\" {\ncolumns = [column.user_id]\n}"), "", `table "posts": foreign key "posts_fk": missing referenced columns`},
		{"fk bad ref column", inlineTable("posts", col("user_id")+"foreign_key \"posts_fk\" {\ncolumns = [column.user_id]\nref_columns = [users.id]\n}"), "", `table "posts": foreign key "posts_fk": unrecognized referenced column`},
		{"fk ref columns span tables", inlineTable("posts", col("a")+col("b")+"foreign_key \"posts_fk\" {\ncolumns = [column.a, column.b]\nref_columns = [table.users.column.id, table.orgs.column.id]\n}"), "", `table "posts": foreign key "posts_fk": referenced columns must belong to one table`},
		{"fk without columns", inlineTable("posts", col("user_id")+"foreign_key \"posts_fk\" {\nref_columns = [table.users.column.id]\n}"), "", `table "posts": foreign key "posts_fk": missing columns`},
		{"fk bad column", inlineTable("posts", col("user_id")+"foreign_key \"posts_fk\" {\ncolumns = [lower(user_id)]\nref_columns = [table.users.column.id]\n}"), "", `table "posts": foreign key "posts_fk": unrecognized column reference`},
		{"fk column count mismatch", inlineTable("posts", col("user_id")+"foreign_key \"posts_fk\" {\ncolumns = [column.user_id]\nref_columns = [table.users.column.id, table.users.column.x]\n}"), "", `table "posts": foreign key "posts_fk": 1 columns reference 2 columns`},
		{"unique unknown column", inlineTable("users", col("id")+"index \"u\" {\nunique = true\ncolumns = [column.email]\n}"), "", `table "users": index "u": unknown column "email"`},
		{"unique block unknown column", inlineTable("users", col("id")+"unique \"u\" {\ncolumns = [column.email]\n}"), "", `table "users": unique "u": unknown column "email"`},
		{"unique bad flag", inlineTable("users", col("id")+"index \"u\" {\nunique = \"yes\"\ncolumns = [column.id]\n}"), "", `table "users": index "u": unique:`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(tc.data), "postgres", tc.schemaName)
			if err == nil {
				t.Fatalf("Parse succeeded with %#v, want error", s)
			}
			if s != nil {
				t.Errorf("Parse returned a schema alongside error %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}
