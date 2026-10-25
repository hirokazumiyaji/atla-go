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

func parseFixture(t *testing.T, name, dialect, schemaName string) *ir.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "atlas", name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(data, dialect, schemaName)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
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

// inlineSchema wraps one table's JSON in a single-schema Atlas document.
func inlineSchema(tableJSON string) []byte {
	return []byte(`{"schemas":[{"name":"main","tables":[` + tableJSON + `]}]}`)
}

func TestParsePostgresUsers(t *testing.T) {
	s := parseFixture(t, "postgres_users.json", "postgres", "")

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

	equal(t, "users.created_at.HasDefault", mustColumn(t, users, "created_at").HasDefault, true)

	var colNames []string
	for _, c := range users.Columns {
		colNames = append(colNames, c.DBName)
	}
	equal(t, "users columns (Atlas order)", colNames, []string{"id", "email", "role", "from", "created_at"})

	posts := mustTable(t, s, "posts")
	equal(t, "posts.id.HasDefault", mustColumn(t, posts, "id").HasDefault, true)
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
	for _, f := range []string{"postgres_users.json", "pg_types.json", "composite.json"} {
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
	data, err := os.ReadFile(filepath.Join("..", "testdata", "atlas", "composite.json"))
	if err != nil {
		t.Fatal(err)
	}
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

func TestParseShapeVariants(t *testing.T) {
	t.Run("sqlite_users", func(t *testing.T) {
		s := parseFixture(t, "sqlite_users.json", "sqlite", "")
		equal(t, "Name", s.Name, "main")
		users := mustTable(t, s, "users")
		equal(t, "users.id.HasDefault", mustColumn(t, users, "id").HasDefault, true)
		equal(t, "users.email.HasDefault", mustColumn(t, users, "email").HasDefault, false)
		equal(t, "users.Uniques", users.Uniques, [][]string{{"email"}})
		equal(t, "Enums", s.Enums, []ir.Enum{})
	})

	t.Run("mysql_users", func(t *testing.T) {
		s := parseFixture(t, "mysql_users.json", "mysql", "")
		equal(t, "tables", tableNames(s), []string{"categories", "users"})
		users := mustTable(t, s, "users")
		equal(t, "users.id.HasDefault", mustColumn(t, users, "id").HasDefault, true)
		equal(t, "users.email.HasDefault", mustColumn(t, users, "email").HasDefault, false)
		equal(t, "users.class.Nullable", mustColumn(t, users, "class").Nullable, true)
		categories := mustTable(t, s, "categories")
		equal(t, "categories.id.HasDefault", mustColumn(t, categories, "id").HasDefault, true)
		equal(t, "categories.name.HasDefault", mustColumn(t, categories, "name").HasDefault, false)
		equal(t, "categories.PrimaryKey", categories.PrimaryKey, []string{"id"})
		equal(t, "categories.id.InPK", mustColumn(t, categories, "id").InPK, true)
	})

	t.Run("typecheck_users", func(t *testing.T) {
		s := parseFixture(t, "typecheck_users.json", "postgres", "")
		users := mustTable(t, s, "users")
		equal(t, "users.age.RequiredForInsert", mustColumn(t, users, "age").RequiredForInsert(), true)
		equal(t, "users.id.RequiredForInsert", mustColumn(t, users, "id").RequiredForInsert(), false)
	})

	hasDefault := []struct {
		name   string
		column string
		want   bool
	}{
		{"auto_increment string true", `{"name":"c","type":"int","auto_increment":"true"}`, true},
		{"auto_increment string yes", `{"name":"c","type":"int","auto_increment":"YES"}`, true},
		{"auto_increment string false", `{"name":"c","type":"int","auto_increment":"false"}`, false},
		{"auto_increment bool false", `{"name":"c","type":"int","auto_increment":false}`, false},
		{"identity object", `{"name":"c","type":"bigint","identity":{"generated":"ALWAYS"}}`, true},
		{"identity null", `{"name":"c","type":"bigint","identity":null}`, false},
		{"default null", `{"name":"c","type":"bigint","default":null}`, false},
		{"default number", `{"name":"c","type":"bigint","default":0}`, true},
		{"serial4", `{"name":"c","type":"serial4"}`, true},
		{"SmallSerial", `{"name":"c","type":"SmallSerial"}`, true},
		{"attr type identity", `{"name":"c","type":"bigint","attrs":[{"type":"Identity"}]}`, true},
		{"attr @type auto_increment", `{"name":"c","type":"int","attrs":[{"@type":"mysql.AutoIncrement_auto_increment"}]}`, true},
		{"attr name identity", `{"name":"c","type":"int","attrs":[{"name":"identity"}]}`, true},
		{"attr auto_increment true", `{"name":"c","type":"int","attrs":[{"auto_increment":true}]}`, true},
		{"attr unrelated", `{"name":"c","type":"int","attrs":[{"name":"collate"},"x"]}`, false},
		{"type object auto_increment", `{"name":"c","type":{"raw":"int","auto_increment":"1"}}`, true},
		{"plain", `{"name":"c","type":"int"}`, false},
	}
	for _, tc := range hasDefault {
		t.Run("has_default/"+tc.name, func(t *testing.T) {
			s, err := Parse(inlineSchema(`{"name":"t","columns":[`+tc.column+`]}`), "mysql", "")
			if err != nil {
				t.Fatal(err)
			}
			equal(t, "HasDefault", s.Tables[0].Columns[0].HasDefault, tc.want)
		})
	}

	rawType := []struct {
		name string
		typ  string
		want string
	}{
		{"string", `"text"`, "text"},
		{"raw", `{"raw":"text"}`, "text"},
		{"type key", `{"type":"varchar(5)"}`, "varchar(5)"},
		{"name key", `{"name":"uuid"}`, "uuid"},
		{"raw wins", `{"raw":"character varying(3)","type":"varchar","name":"x"}`, "character varying(3)"},
		{"nested T", `{"T":{"raw":"jsonb"}}`, "jsonb"},
		{"nested Type", `{"Type":"x","T":"y","raw":""}`, ""},
	}
	for _, tc := range rawType {
		t.Run("raw_type/"+tc.name, func(t *testing.T) {
			s, err := Parse(inlineSchema(`{"name":"t","columns":[{"name":"c","type":`+tc.typ+`}]}`), "postgres", "")
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), `table "t": column "c": missing type`) {
					t.Fatalf("err = %v, want missing type", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equal(t, "RawType", s.Tables[0].Columns[0].RawType, tc.want)
		})
	}

	t.Run("primary key variants", func(t *testing.T) {
		for _, pk := range []string{
			`{"columns":["a","b"]}`,
			`{"columns":[{"name":"a"},{"column":"b"}]}`,
			`{"columns":[{"c":"a"},{"column":{"name":"b"}}]}`,
			`{"parts":[{"column":"a"},{"c":{"name":"b"}}]}`,
		} {
			s, err := Parse(inlineSchema(`{"name":"t","columns":[{"name":"a","type":"int"},{"name":"b","type":"int"},{"name":"x","type":"int","null":true}],"primary_key":`+pk+`}`), "postgres", "")
			if err != nil {
				t.Fatalf("%s: %v", pk, err)
			}
			tbl := s.Tables[0]
			equal(t, pk+" PrimaryKey", tbl.PrimaryKey, []string{"a", "b"})
			equal(t, pk+" a.InPK", mustColumn(t, tbl, "a").InPK, true)
			equal(t, pk+" x.InPK", mustColumn(t, tbl, "x").InPK, false)
		}
	})

	t.Run("no primary key", func(t *testing.T) {
		s, err := Parse(inlineSchema(`{"name":"t","columns":[{"name":"a","type":"int"}]}`), "postgres", "")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "PrimaryKey", s.Tables[0].PrimaryKey, []string{})
	})

	t.Run("nullable pk column is not nullable", func(t *testing.T) {
		s, err := Parse(inlineSchema(`{"name":"t","columns":[{"name":"a","type":"TEXT","null":true}],"primary_key":{"columns":["a"]}}`), "sqlite", "")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "a.Nullable", s.Tables[0].Columns[0].Nullable, false)
	})

	t.Run("index variants", func(t *testing.T) {
		s, err := Parse(inlineSchema(`{"name":"t",
			"columns":[{"name":"a","type":"int"},{"name":"b","type":"int"}],
			"indexes":[
				{"name":"i1","unique":true,"columns":["a","b"]},
				{"name":"i2","unique":true,"parts":["b"]},
				{"name":"i3","unique":true,"parts":[{"name":"a"}]},
				{"name":"i4","unique":true,"parts":[{"c":"b"},{"x":"lower(a)"}]},
				{"name":"i5","unique":true,"parts":[{"column":"a"}],"where":null},
				{"name":"i6","unique":true,"parts":[{"column":"a"}],"where":"a > 0"},
				{"name":"i7","parts":[{"column":"a"}]},
				{"unique":true,"parts":[{"column":{"name":"b"}}]}
			]}`), "postgres", "")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "Uniques", s.Tables[0].Uniques, [][]string{{"a", "b"}, {"b"}, {"a"}, {"a"}, {"b"}})
	})

	t.Run("foreign key variants", func(t *testing.T) {
		s, err := Parse([]byte(`{"schemas":[{"name":"main","tables":[
			{"name":"parents","columns":[{"name":"id","type":"int"}]},
			{"name":"children","columns":[{"name":"p","type":"int"}],"foreign_keys":[
				{"symbol":"fk_sym","columns":["p"],"referenced_columns":["id"],"referenced_table":"parents"},
				{"columns":["p"],"ref":{"table":{"name":"parents"},"columns":["id"]}},
				{"name":"fk_ref_string","columns":["p"],"ref_columns":["id"],"ref":"parents"},
				{"name":"fk_qualified","columns":["p"],"ref_columns":["id"],"ref_table":"other.parents"},
				{"name":"fk_schema_string","columns":["p"],"ref_columns":["id"],"ref_table":{"name":"parents","schema":"other"}},
				{"name":"fk_nested","columns":["p"],"ref_columns":["id"],"table":{"table":{"name":"parents"}}},
				{"name":"fk_references","columns":["p"],"references":{"table":"parents","columns":["id"]}}
			]}]}]}`), "postgres", "")
		if err != nil {
			t.Fatal(err)
		}
		children := mustTable(t, s, "children")
		fk := func(sym, schema string) ir.ForeignKey {
			return ir.ForeignKey{Symbol: sym, Columns: []string{"p"}, RefSchema: schema, RefTable: "parents", RefColumns: []string{"id"}}
		}
		equal(t, "children.ForeignKeys", children.ForeignKeys, []ir.ForeignKey{
			fk("fk_sym", ""),
			fk("fk_p_parents", ""),
			fk("fk_ref_string", ""),
			fk("fk_qualified", "other"),
			fk("fk_schema_string", "other"),
			fk("fk_nested", ""),
			fk("fk_references", ""),
		})
	})

	t.Run("enum detection", func(t *testing.T) {
		s, err := Parse([]byte(`{"schemas":[{"name":"public",
			"enums":[{"name":"Mood","values":["happy","sad"]},{"name":"empty_enum","values":[]}],
			"tables":[{"name":"t","columns":[
				{"name":"a","type":"Mood"},
				{"name":"b","type":"public.mood"},
				{"name":"c","type":"mood[]"},
				{"name":"d","type":"text"},
				{"name":"e","type":"empty_enum"}
			]}]}]}`), "postgres", "")
		if err != nil {
			t.Fatal(err)
		}
		tbl := s.Tables[0]
		equal(t, "a.Enum", mustColumn(t, tbl, "a").Enum, "Mood")
		equal(t, "b.Enum", mustColumn(t, tbl, "b").Enum, "Mood")
		equal(t, "c.Enum", mustColumn(t, tbl, "c").Enum, "")
		equal(t, "d.Enum", mustColumn(t, tbl, "d").Enum, "")
		equal(t, "e.Enum", mustColumn(t, tbl, "e").Enum, "empty_enum")
		equal(t, "Enums", s.Enums, []ir.Enum{
			{DBName: "Mood", Values: []ir.EnumValue{{DB: "happy"}, {DB: "sad"}}},
			{DBName: "empty_enum", Values: []ir.EnumValue{}},
		})
	})
}

func TestParsePGTypes(t *testing.T) {
	s := parseFixture(t, "pg_types.json", "postgres", "")
	tbl := mustTable(t, s, "t")
	equal(t, "c_double_precision.RawType", mustColumn(t, tbl, "c_double_precision").RawType, "double precision")
	equal(t, "tags.RawType", mustColumn(t, tbl, "tags").RawType, "text[]")
	equal(t, "tags.Enum", mustColumn(t, tbl, "tags").Enum, "")
	equal(t, "role.Enum", mustColumn(t, tbl, "role").Enum, "user_role")
	equal(t, "nullable_int", mustColumn(t, tbl, "nullable_int"), ir.Column{DBName: "nullable_int", RawType: "integer", Nullable: true})
	equal(t, "c_integer.HasDefault", mustColumn(t, tbl, "c_integer").HasDefault, false)
	equal(t, "c_serial.HasDefault", mustColumn(t, tbl, "c_serial").HasDefault, true)
	equal(t, "c_smallserial.HasDefault", mustColumn(t, tbl, "c_smallserial").HasDefault, true)
	equal(t, "PrimaryKey", tbl.PrimaryKey, []string{"c_bigserial"})
}

func TestParseComposite(t *testing.T) {
	s := parseFixture(t, "composite.json", "postgres", "")
	equal(t, "tables", tableNames(s), []string{"membership_notes", "memberships", "orgs", "users"})

	orgs := mustTable(t, s, "orgs")
	equal(t, "orgs.PrimaryKey", orgs.PrimaryKey, []string{"id"})

	m := mustTable(t, s, "memberships")
	equal(t, "memberships.PrimaryKey", m.PrimaryKey, []string{"org_id", "user_id"})
	equal(t, "memberships.org_id.InPK", mustColumn(t, m, "org_id").InPK, true)
	equal(t, "memberships.user_id.InPK", mustColumn(t, m, "user_id").InPK, true)
	equal(t, "memberships.slug.InPK", mustColumn(t, m, "slug").InPK, false)
	equal(t, "memberships.org_id.RequiredForInsert", mustColumn(t, m, "org_id").RequiredForInsert(), true)
	// Only the plain 2-column unique index qualifies: the expression index,
	// the partial index and the non-unique index are excluded.
	equal(t, "memberships.Uniques", m.Uniques, [][]string{{"org_id", "slug"}})
	equal(t, "memberships FK count", len(m.ForeignKeys), 2)

	notes := mustTable(t, s, "membership_notes")
	equal(t, "membership_notes.ForeignKeys", notes.ForeignKeys, []ir.ForeignKey{{
		Symbol:     "membership_notes_membership_fkey",
		Columns:    []string{"org_id", "user_id"},
		RefTable:   "memberships",
		RefColumns: []string{"org_id", "user_id"},
	}})
}

func TestParseSQLiteRowidAlias(t *testing.T) {
	s := parseFixture(t, "sqlite_types.json", "sqlite", "")
	things := mustTable(t, s, "things")
	id := mustColumn(t, things, "id")
	equal(t, "things.id", id, ir.Column{DBName: "id", RawType: "INTEGER", HasDefault: true, InPK: true})
	for _, name := range []string{"created_at", "active", "code", "rank", "price"} {
		equal(t, "things."+name+".HasDefault", mustColumn(t, things, name).HasDefault, false)
	}
	equal(t, "things.extra.Nullable", mustColumn(t, things, "extra").Nullable, true)
	equal(t, "things.price.RawType", mustColumn(t, things, "price").RawType, "NUMERIC(10,2)")

	// Composite INTEGER PKs and non-INTEGER single-column PKs are not rowid aliases.
	pairs := mustTable(t, s, "pairs")
	equal(t, "pairs.a.HasDefault", mustColumn(t, pairs, "a").HasDefault, false)
	codes := mustTable(t, s, "codes")
	equal(t, "codes.id.HasDefault", mustColumn(t, codes, "id").HasDefault, false)

	// The rule is SQLite-only.
	pg := parseFixture(t, "sqlite_types.json", "postgres", "")
	equal(t, "postgres things.id.HasDefault", mustColumn(t, mustTable(t, pg, "things"), "id").HasDefault, false)
}

func TestParseSchemaSelection(t *testing.T) {
	s := parseFixture(t, "multi_schema.json", "postgres", "")
	equal(t, "default Name", s.Name, "public")
	equal(t, "default tables", tableNames(s), []string{"events"})
	equal(t, "events.ForeignKeys", mustTable(t, s, "events").ForeignKeys, []ir.ForeignKey{{
		Symbol:     "events_log_id_fkey",
		Columns:    []string{"log_id"},
		RefSchema:  "audit",
		RefTable:   "logs",
		RefColumns: []string{"id"},
	}})

	audit := parseFixture(t, "multi_schema.json", "postgres", "audit")
	equal(t, "audit Name", audit.Name, "audit")
	equal(t, "audit tables", tableNames(audit), []string{"logs"})

	public := parseFixture(t, "multi_schema.json", "postgres", "public")
	equal(t, "public tables", tableNames(public), []string{"events"})

	data, err := os.ReadFile(filepath.Join("..", "testdata", "atlas", "multi_schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse(data, "postgres", "nope")
	if err == nil {
		t.Fatal("Parse with unknown schema: want error")
	}
	for _, want := range []string{`schema "nope" not found`, `available: public, audit`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		schemaName string
		want       []string
	}{
		{"invalid json", `{"schemas": [`, "", []string{"invalid atlas json"}},
		{"root not object", `[]`, "", []string{"invalid atlas json"}},
		{"empty schemas", `{"schemas": []}`, "", []string{"no schemas"}},
		{"missing schemas", `{}`, "", []string{"no schemas"}},
		{"no tables", `{"schemas":[{"name":"x"}]}`, "", []string{`schema "x" has no tables`}},
		{"no tables selected", `{"schemas":[{"name":"a","tables":[{"name":"t","columns":[{"name":"c","type":"int"}]}]},{"name":"x","tables":[]}]}`, "x", []string{`schema "x" has no tables`}},
		{"enum without name", `{"schemas":[{"name":"x","enums":[{"values":["a"]}],"tables":[{"name":"t","columns":[{"name":"c","type":"int"}]}]}]}`, "", []string{"enum at index 0 has no name"}},
		{"table without name", inlineSchema2(`{"columns":[{"name":"c","type":"int"}]}`), "", []string{"table at index 0 has no name"}},
		{"column without name", inlineSchema2(`{"name":"users","columns":[{"name":"a","type":"int"},{"type":"int"}]}`), "", []string{`table "users": column at index 1 has no name`}},
		{"column without type", inlineSchema2(`{"name":"users","columns":[{"name":"age"}]}`), "", []string{`table "users": column "age": missing type`}},
		{"duplicate column", inlineSchema2(`{"name":"users","columns":[{"name":"a","type":"int"},{"name":"a","type":"int"}]}`), "", []string{`table "users": duplicate column "a"`}},
		{"duplicate table", inlineSchema2(`{"name":"users","columns":[{"name":"a","type":"int"}]},{"name":"users","columns":[{"name":"a","type":"int"}]}`), "", []string{`duplicate table "users"`}},
		{"pk unknown column", inlineSchema2(`{"name":"users","columns":[{"name":"id","type":"int"}],"primary_key":{"columns":["uid"]}}`), "", []string{`table "users": primary key: unknown column "uid"`}},
		{"pk bad reference", inlineSchema2(`{"name":"users","columns":[{"name":"id","type":"int"}],"primary_key":{"columns":[{"x":"id+1"}]}}`), "", []string{`table "users": primary key: unrecognized column reference`}},
		{"fk unknown column", inlineSchema2(`{"name":"posts","columns":[{"name":"user_id","type":"int"}],"foreign_keys":[{"name":"posts_fk","columns":["author_id"],"ref_columns":["id"],"ref_table":"users"}]}`), "", []string{`table "posts": foreign key "posts_fk": unknown column "author_id"`}},
		{"fk without ref table", inlineSchema2(`{"name":"posts","columns":[{"name":"user_id","type":"int"}],"foreign_keys":[{"name":"posts_fk","columns":["user_id"],"ref_columns":["id"]}]}`), "", []string{`table "posts": foreign key "posts_fk": missing referenced table`}},
		{"fk without columns", inlineSchema2(`{"name":"posts","columns":[{"name":"user_id","type":"int"}],"foreign_keys":[{"name":"posts_fk","ref_columns":["id"],"ref_table":"users"}]}`), "", []string{`table "posts": foreign key "posts_fk": missing columns`}},
		{"fk column count mismatch", inlineSchema2(`{"name":"posts","columns":[{"name":"user_id","type":"int"}],"foreign_keys":[{"name":"posts_fk","columns":["user_id"],"ref_columns":["id","x"],"ref_table":"users"}]}`), "", []string{`table "posts": foreign key "posts_fk": 1 columns reference 2 columns`}},
		{"unique unknown column", inlineSchema2(`{"name":"users","columns":[{"name":"id","type":"int"}],"indexes":[{"name":"u","unique":true,"columns":["email"]}]}`), "", []string{`table "users": index "u": unknown column "email"`}},
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
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func inlineSchema2(tablesJSON string) string {
	return string(inlineSchema(tablesJSON))
}
