package mapper

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/naming"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/parser"
)

const enumPkg = "example.com/app/dbgen/enum"

// fixture runs parser → naming on a testdata/atlas fixture.
func fixture(t *testing.T, name, dialect string) *ir.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "atlas", name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := parser.Parse(data, dialect, "")
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	if err := naming.Apply(s, naming.Overrides{RootPackage: "dbgen"}); err != nil {
		t.Fatalf("naming.Apply(%s): %v", name, err)
	}
	return s
}

func mustApply(t *testing.T, s *ir.Schema, o Options) []string {
	t.Helper()
	warnings, err := Apply(s, o)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return warnings
}

func applyErr(t *testing.T, s *ir.Schema, o Options) string {
	t.Helper()
	_, err := Apply(s, o)
	if err == nil {
		t.Fatal("Apply succeeded, want error")
	}
	return err.Error()
}

func wantContains(t *testing.T, msg string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(msg, sub) {
			t.Errorf("%q does not contain %q", msg, sub)
		}
	}
}

func column(t *testing.T, s *ir.Schema, table, col string) ir.Column {
	t.Helper()
	for _, tbl := range s.Tables {
		if tbl.DBName != table {
			continue
		}
		c, ok := tbl.Column(col)
		if !ok {
			t.Fatalf("table %q: column %q not found", table, col)
		}
		return c
	}
	t.Fatalf("table %q not found", table)
	return ir.Column{}
}

// want is the mapper's output for one column.
type want struct {
	name, importPath string
	kind             ir.ColumnKind
	scan             ir.ScanPlan
}

func checkColumn(t *testing.T, c ir.Column, w want) {
	t.Helper()
	got := want{c.GoType.Name, c.GoType.ImportPath, c.Kind, c.Scan}
	if got != w {
		t.Errorf("column %q (%s): got %+v, want %+v", c.DBName, c.RawType, got, w)
	}
}

// users builds a small hand-made postgres schema for override tests.
func users() *ir.Schema {
	return &ir.Schema{Name: "public", Dialect: "postgres", Tables: []ir.Table{{
		DBName: "users",
		Columns: []ir.Column{
			{DBName: "id", RawType: "integer"},
			{DBName: "age", RawType: "integer"},
			{DBName: "ext_id", RawType: "uuid"},
			{DBName: "other_id", RawType: "uuid"},
			{DBName: "price", RawType: "numeric(10,2)"},
			{DBName: "ttl", RawType: "interval"},
			{DBName: "email", RawType: "character varying(255)"},
		},
	}}}
}

func TestNormalizeType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"character varying(255)", "character varying"},
		{"NUMERIC(10, 2)", "numeric"},
		{"timestamp with time zone", "timestamptz"},
		{"timestamp without time zone", "timestamp"},
		{"time with time zone", "timetz"},
		{"time without time zone", "time"},
		{"  Double   Precision ", "double precision"},
		{"int unsigned", "int"},
		{"text[]", "text[]"},
		{"timestamp(3) with time zone", "timestamptz"},
		{"timestamptz(3)", "timestamptz"},
		{"interval", "interval"},
		{"interval hour", "interval"},
		{"interval day to second", "interval"},
		{"interval second(3)", "interval"},
		{"character varying(64)[]", "character varying[]"},
		{"INTEGER[][]", "integer[]"},
		{"varchar(10)", "varchar"},
		{"FOO", "foo"},
	}
	for _, tc := range cases {
		if got := NormalizeType(tc.in); got != tc.want {
			t.Errorf("NormalizeType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPostgresTable(t *testing.T) {
	s := fixture(t, "pg_types.hcl", "postgres")
	o := Options{
		Dialect:        "postgres",
		Nullable:       NullPointer,
		EnumImportPath: enumPkg,
		Columns:        []ColumnOverride{{Column: "t.tags", GoType: "github.com/lib/pq.StringArray"}},
	}
	if w := mustApply(t, s, o); len(w) != 0 {
		t.Errorf("warnings = %q, want none", w)
	}

	var (
		int16W   = want{"int16", "", ir.KindNumber, ir.ScanWiden}
		int32W   = want{"int32", "", ir.KindNumber, ir.ScanWiden}
		int64D   = want{"int64", "", ir.KindNumber, ir.ScanDirect}
		float32W = want{"float32", "", ir.KindNumber, ir.ScanWiden}
		float64D = want{"float64", "", ir.KindNumber, ir.ScanDirect}
		strCol   = want{"string", "", ir.KindColumn, ir.ScanDirect}
		strStr   = want{"string", "", ir.KindString, ir.ScanDirect}
		boolCol  = want{"bool", "", ir.KindColumn, ir.ScanDirect}
		bytesCol = want{"[]byte", "", ir.KindColumn, ir.ScanDirect}
		timeCol  = want{"time.Time", "time", ir.KindColumn, ir.ScanDirect}
		jsonCol  = want{"json.RawMessage", "encoding/json", ir.KindColumn, ir.ScanDirect}
	)
	rows := []struct {
		name    string
		columns []string
		want    want
	}{
		{"smallint", []string{"c_smallint", "c_int2", "c_smallserial"}, int16W},
		{"integer", []string{"c_integer", "c_int", "c_int4", "c_serial"}, int32W},
		{"bigint", []string{"c_bigint", "c_int8", "c_bigserial"}, int64D},
		{"real", []string{"c_real", "c_float4"}, float32W},
		{"double precision", []string{"c_double_precision", "c_float8"}, float64D},
		{"numeric", []string{"c_numeric", "c_decimal"}, strCol},
		{"money", []string{"c_money"}, strCol},
		{"boolean", []string{"c_boolean"}, boolCol},
		{"text", []string{"c_text", "c_varchar", "c_char", "c_citext"}, strStr},
		{"network", []string{"c_inet", "c_cidr", "c_macaddr"}, strStr},
		{"bytea", []string{"c_bytea"}, bytesCol},
		{"uuid", []string{"c_uuid"}, strCol},
		{"timestamp", []string{"c_date", "c_timestamp", "c_timestamptz"}, timeCol},
		{"time", []string{"c_time", "c_timetz", "c_interval"}, strCol},
		{"json", []string{"c_json", "c_jsonb"}, jsonCol},
		{"enum", []string{"role"}, want{"enum.UserRole", enumPkg, ir.KindString, ir.ScanDirect}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for _, c := range row.columns {
				checkColumn(t, column(t, s, "t", c), row.want)
			}
		})
	}
}

// TestPostgresRawTypeSpellings covers raw types the parser can produce that
// pg_types.hcl does not contain.
func TestPostgresRawTypeSpellings(t *testing.T) {
	raws := map[string]want{
		"character varying(255)":       {"string", "", ir.KindString, ir.ScanDirect},
		"character(2)":                 {"string", "", ir.KindString, ir.ScanDirect},
		"bpchar":                       {"string", "", ir.KindString, ir.ScanDirect},
		"varchar(10)":                  {"string", "", ir.KindString, ir.ScanDirect},
		"numeric(10,2)":                {"string", "", ir.KindColumn, ir.ScanDirect},
		"bool":                         {"bool", "", ir.KindColumn, ir.ScanDirect},
		"serial2":                      {"int16", "", ir.KindNumber, ir.ScanWiden},
		"serial4":                      {"int32", "", ir.KindNumber, ir.ScanWiden},
		"serial8":                      {"int64", "", ir.KindNumber, ir.ScanDirect},
		"timestamp with time zone":     {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
		"timestamp(3) with time zone":  {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
		"timestamptz(3)":               {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
		"timestamp without time zone":  {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
		"time with time zone":          {"string", "", ir.KindColumn, ir.ScanDirect},
		"interval hour":                {"string", "", ir.KindColumn, ir.ScanDirect},
		"interval day to second":       {"string", "", ir.KindColumn, ir.ScanDirect},
		"interval second(3)":           {"string", "", ir.KindColumn, ir.ScanDirect},
		"double precision":             {"float64", "", ir.KindNumber, ir.ScanDirect},
		"time(6) without time zone":    {"string", "", ir.KindColumn, ir.ScanDirect},
		"timestamp(6) with time zone ": {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
	}
	for raw, w := range raws {
		s := &ir.Schema{Dialect: "postgres", Tables: []ir.Table{{DBName: "t", Columns: []ir.Column{{DBName: "c", RawType: raw}}}}}
		mustApply(t, s, Options{Dialect: "postgres", Nullable: NullPointer})
		checkColumn(t, s.Tables[0].Columns[0], w)
	}
}

func TestPostgresArrayRequiresOverride(t *testing.T) {
	s := fixture(t, "pg_types.hcl", "postgres")
	msg := applyErr(t, s, Options{Dialect: "postgres", Nullable: NullPointer, EnumImportPath: enumPkg})
	wantContains(t, msg, `table "t": column "tags": array type "text[]"`, "override")

	s = fixture(t, "pg_types.hcl", "postgres")
	mustApply(t, s, Options{
		Dialect: "postgres", Nullable: NullPointer, EnumImportPath: enumPkg,
		Columns: []ColumnOverride{{Column: "t.tags", GoType: "github.com/lib/pq.StringArray"}},
	})
	checkColumn(t, column(t, s, "t", "tags"), want{"pq.StringArray", "github.com/lib/pq", ir.KindColumn, ir.ScanDirect})

	// A types: override on the array type works too.
	s = fixture(t, "pg_types.hcl", "postgres")
	mustApply(t, s, Options{
		Dialect: "postgres", Nullable: NullPointer, EnumImportPath: enumPkg,
		Types: []TypeOverride{{DBType: "text[]", GoType: "github.com/lib/pq.StringArray"}},
	})
	checkColumn(t, column(t, s, "t", "tags"), want{"pq.StringArray", "github.com/lib/pq", ir.KindColumn, ir.ScanDirect})
}

func TestPostgresUnmapped(t *testing.T) {
	s := &ir.Schema{Dialect: "postgres", Tables: []ir.Table{
		{DBName: "docs", Columns: []ir.Column{{DBName: "body", RawType: "xml"}, {DBName: "id", RawType: "bigint"}}},
		{DBName: "points", Columns: []ir.Column{{DBName: "geom", RawType: "geometry(Point,4326)"}}},
	}}
	msg := applyErr(t, s, Options{Dialect: "postgres", Nullable: NullPointer})
	wantContains(t, msg, `table "docs": column "body"`, `"xml"`, "types:")
	// Every unmapped column is reported, not only the first.
	wantContains(t, msg, `table "points": column "geom"`, `"geometry(Point,4326)"`)

	// An enum of another schema is not in the IR and needs an override.
	s = &ir.Schema{Dialect: "postgres", Tables: []ir.Table{{DBName: "t", Columns: []ir.Column{{DBName: "m", RawType: "audit.mood"}}}}}
	wantContains(t, applyErr(t, s, Options{Dialect: "postgres", Nullable: NullPointer}), `table "t": column "m"`, `"audit.mood"`)
}

func TestSQLiteTable(t *testing.T) {
	s := fixture(t, "sqlite_types.hcl", "sqlite")
	warnings := mustApply(t, s, Options{Dialect: "sqlite", Nullable: NullPointer})

	// The rowid alias is printed null = true, but PK columns are never nullable.
	checkColumn(t, column(t, s, "things", "id"), want{"int64", "", ir.KindNumber, ir.ScanDirect})
	checkColumn(t, column(t, s, "things", "created_at"), want{"time.Time", "time", ir.KindColumn, ir.ScanDirect})
	checkColumn(t, column(t, s, "things", "active"), want{"bool", "", ir.KindColumn, ir.ScanDirect})
	checkColumn(t, column(t, s, "things", "code"), want{"string", "", ir.KindString, ir.ScanDirect})
	checkColumn(t, column(t, s, "things", "rank"), want{"int64", "", ir.KindNumber, ir.ScanDirect})
	checkColumn(t, column(t, s, "things", "price"), want{"string", "", ir.KindColumn, ir.ScanDirect})
	checkColumn(t, column(t, s, "things", "extra"), want{"string", "", ir.KindNullColumn, ir.ScanNullPtr})
	checkColumn(t, column(t, s, "pairs", "a"), want{"int64", "", ir.KindNumber, ir.ScanDirect})
	// A generated column maps like any other column.
	checkColumn(t, column(t, s, "codes", "label_upper"), want{"string", "", ir.KindString, ir.ScanDirect})

	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one", warnings)
	}
	wantContains(t, warnings[0], `table "things"`, `column "extra"`, "FOO")
}

func TestSQLiteAffinity(t *testing.T) {
	cases := map[string]want{
		"unsigned big int":  {"int64", "", ir.KindNumber, ir.ScanDirect},
		"tinyint":           {"int64", "", ir.KindNumber, ir.ScanDirect},
		"nvarchar(100)":     {"string", "", ir.KindString, ir.ScanDirect},
		"varying character": {"string", "", ir.KindString, ir.ScanDirect},
		"clob":              {"string", "", ir.KindString, ir.ScanDirect},
		"blob":              {"[]byte", "", ir.KindColumn, ir.ScanDirect},
		"longblob":          {"[]byte", "", ir.KindColumn, ir.ScanDirect},
		"double precision":  {"float64", "", ir.KindNumber, ir.ScanDirect},
		"float8":            {"float64", "", ir.KindNumber, ir.ScanDirect},
		"floating point":    {"int64", "", ir.KindNumber, ir.ScanDirect}, // SQLite: "int" wins
		"real":              {"float64", "", ir.KindNumber, ir.ScanDirect},
		"date":              {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
		"timestamp":         {"time.Time", "time", ir.KindColumn, ir.ScanDirect},
		"uuid":              {"string", "", ir.KindColumn, ir.ScanDirect},
		"json":              {"json.RawMessage", "encoding/json", ir.KindColumn, ir.ScanDirect},
		"decimal(5,1)":      {"string", "", ir.KindColumn, ir.ScanDirect},
		"bool":              {"bool", "", ir.KindColumn, ir.ScanDirect},
	}
	for raw, w := range cases {
		s := &ir.Schema{Dialect: "sqlite", Tables: []ir.Table{{DBName: "t", Columns: []ir.Column{{DBName: "c", RawType: raw}}}}}
		if warnings := mustApply(t, s, Options{Dialect: "sqlite", Nullable: NullPointer}); len(warnings) != 0 {
			t.Errorf("%q: warnings = %q, want none", raw, warnings)
		}
		checkColumn(t, s.Tables[0].Columns[0], w)
	}
}

func TestNullability(t *testing.T) {
	schema := func() *ir.Schema {
		return &ir.Schema{Dialect: "postgres", Tables: []ir.Table{{DBName: "t", Columns: []ir.Column{
			{DBName: "i", RawType: "integer", Nullable: true},
			{DBName: "s", RawType: "text", Nullable: true},
			{DBName: "b", RawType: "bigint", Nullable: true},
			{DBName: "ts", RawType: "timestamptz", Nullable: true},
		}}}}
	}

	s := schema()
	mustApply(t, s, Options{Dialect: "postgres", Nullable: NullPointer})
	checkColumn(t, column(t, s, "t", "i"), want{"int32", "", ir.KindNullNumber, ir.ScanNullWiden})
	checkColumn(t, column(t, s, "t", "s"), want{"string", "", ir.KindNullString, ir.ScanNullPtr})
	checkColumn(t, column(t, s, "t", "b"), want{"int64", "", ir.KindNullNumber, ir.ScanNullPtr})
	checkColumn(t, column(t, s, "t", "ts"), want{"time.Time", "time", ir.KindNullColumn, ir.ScanNullPtr})

	s = schema()
	mustApply(t, s, Options{Dialect: "postgres", Nullable: NullSQLNull})
	checkColumn(t, column(t, s, "t", "i"), want{"int32", "", ir.KindNullNumber, ir.ScanNullWiden})
	checkColumn(t, column(t, s, "t", "s"), want{"string", "", ir.KindNullString, ir.ScanDirect})
	checkColumn(t, column(t, s, "t", "b"), want{"int64", "", ir.KindNullNumber, ir.ScanDirect})
	checkColumn(t, column(t, s, "t", "ts"), want{"time.Time", "time", ir.KindNullColumn, ir.ScanDirect})

	// The fixture's nullable integer gets the same treatment.
	f := fixture(t, "pg_types.hcl", "postgres")
	mustApply(t, f, Options{
		Dialect: "postgres", Nullable: NullPointer, EnumImportPath: enumPkg,
		Columns: []ColumnOverride{{Column: "t.tags", GoType: "github.com/lib/pq.StringArray"}},
	})
	checkColumn(t, column(t, f, "t", "nullable_int"), want{"int32", "", ir.KindNullNumber, ir.ScanNullWiden})

	// An empty style means the default, pointer.
	s = schema()
	mustApply(t, s, Options{Dialect: "postgres"})
	checkColumn(t, column(t, s, "t", "s"), want{"string", "", ir.KindNullString, ir.ScanNullPtr})
}

func TestOverrides(t *testing.T) {
	base := Options{Dialect: "postgres", Nullable: NullPointer}
	with := func(types []TypeOverride, cols []ColumnOverride) Options {
		o := base
		o.Types, o.Columns = types, cols
		return o
	}

	t.Run("type override", func(t *testing.T) {
		s := users()
		w := mustApply(t, s, with([]TypeOverride{
			{DBType: "uuid", GoType: "github.com/google/uuid.UUID"},
			{DBType: "numeric", GoType: "github.com/shopspring/decimal.Decimal"},
			{DBType: "interval", GoType: "time.Duration"},
		}, nil))
		if len(w) != 0 {
			t.Errorf("warnings = %q, want none", w)
		}
		checkColumn(t, column(t, s, "users", "ext_id"), want{"uuid.UUID", "github.com/google/uuid", ir.KindColumn, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "price"), want{"decimal.Decimal", "github.com/shopspring/decimal", ir.KindColumn, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "ttl"), want{"time.Duration", "time", ir.KindColumn, ir.ScanDirect})
	})

	t.Run("column override beats type override", func(t *testing.T) {
		s := users()
		mustApply(t, s, with(
			[]TypeOverride{{DBType: "uuid", GoType: "github.com/google/uuid.UUID"}},
			[]ColumnOverride{{Column: "users.ext_id", GoType: "github.com/gofrs/uuid/v5.UUID"}},
		))
		checkColumn(t, column(t, s, "users", "ext_id"), want{"uuid.UUID", "github.com/gofrs/uuid/v5", ir.KindColumn, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "other_id"), want{"uuid.UUID", "github.com/google/uuid", ir.KindColumn, ir.ScanDirect})
	})

	t.Run("builtin column overrides", func(t *testing.T) {
		s := users()
		mustApply(t, s, with(nil, []ColumnOverride{
			{Column: "users.age", GoType: "int64"},
			{Column: "users.id", GoType: "int32"},
			{Column: "users.ext_id", GoType: "string"},
			{Column: "users.other_id", GoType: "[]byte"},
			{Column: "users.price", GoType: "float32"},
		}))
		checkColumn(t, column(t, s, "users", "age"), want{"int64", "", ir.KindNumber, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "id"), want{"int32", "", ir.KindNumber, ir.ScanWiden})
		checkColumn(t, column(t, s, "users", "ext_id"), want{"string", "", ir.KindString, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "other_id"), want{"[]byte", "", ir.KindColumn, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "price"), want{"float32", "", ir.KindNumber, ir.ScanWiden})
	})

	t.Run("override on a nullable column", func(t *testing.T) {
		s := users()
		s.Tables[0].Columns[1].Nullable = true // age
		s.Tables[0].Columns[2].Nullable = true // ext_id
		mustApply(t, s, with(
			[]TypeOverride{{DBType: "uuid", GoType: "github.com/google/uuid.UUID"}},
			[]ColumnOverride{{Column: "users.age", GoType: "int16"}},
		))
		checkColumn(t, column(t, s, "users", "age"), want{"int16", "", ir.KindNullNumber, ir.ScanNullWiden})
		checkColumn(t, column(t, s, "users", "ext_id"), want{"uuid.UUID", "github.com/google/uuid", ir.KindNullColumn, ir.ScanNullPtr})
	})

	t.Run("override on an enum column", func(t *testing.T) {
		s := fixture(t, "pg_types.hcl", "postgres")
		mustApply(t, s, Options{
			Dialect: "postgres", Nullable: NullPointer, EnumImportPath: enumPkg,
			Columns: []ColumnOverride{
				{Column: "t.tags", GoType: "github.com/lib/pq.StringArray"},
				{Column: "t.role", GoType: "string"},
			},
		})
		checkColumn(t, column(t, s, "t", "role"), want{"string", "", ir.KindString, ir.ScanDirect})
	})

	t.Run("unused type override warns", func(t *testing.T) {
		s := users()
		w := mustApply(t, s, with([]TypeOverride{
			{DBType: "macaddr8", GoType: "string"},
			{DBType: "jsonb", GoType: "encoding/json.RawMessage"},
		}, nil))
		if len(w) != 2 {
			t.Fatalf("warnings = %q, want 2", w)
		}
		// Sorted.
		wantContains(t, w[0], "types:", `"jsonb"`)
		wantContains(t, w[1], "types:", `"macaddr8"`)
	})

	t.Run("unknown column override errors", func(t *testing.T) {
		msg := applyErr(t, users(), with(nil, []ColumnOverride{{Column: "users.nope", GoType: "string"}}))
		wantContains(t, msg, "columns:", `"users.nope"`)
		msg = applyErr(t, users(), with(nil, []ColumnOverride{{Column: "ghosts.id", GoType: "string"}}))
		wantContains(t, msg, "columns:", `"ghosts.id"`)
		msg = applyErr(t, users(), with(nil, []ColumnOverride{{Column: "age", GoType: "string"}}))
		wantContains(t, msg, "columns:", `"age"`, "table.column")
	})

	t.Run("db type matching normalizes both sides", func(t *testing.T) {
		s := &ir.Schema{Dialect: "postgres", Tables: []ir.Table{{DBName: "t", Columns: []ir.Column{
			{DBName: "v", RawType: "varchar"},
			{DBName: "ts", RawType: "timestamp with time zone"},
		}}}}
		w := mustApply(t, s, with([]TypeOverride{
			{DBType: "VARCHAR(10)", GoType: "example.com/x.Name"},
			{DBType: "TIMESTAMPTZ", GoType: "example.com/x.Instant"},
		}, nil))
		if len(w) != 0 {
			t.Errorf("warnings = %q, want none", w)
		}
		checkColumn(t, column(t, s, "t", "v"), want{"x.Name", "example.com/x", ir.KindColumn, ir.ScanDirect})
		checkColumn(t, column(t, s, "t", "ts"), want{"x.Instant", "example.com/x", ir.KindColumn, ir.ScanDirect})
	})

	t.Run("postgres aliases match", func(t *testing.T) {
		// Atlas prints character varying for varchar columns.
		s := users()
		w := mustApply(t, s, with([]TypeOverride{
			{DBType: "varchar", GoType: "example.com/x.Email"},
			{DBType: "int4", GoType: "int64"},
		}, nil))
		if len(w) != 0 {
			t.Errorf("warnings = %q, want none", w)
		}
		checkColumn(t, column(t, s, "users", "email"), want{"x.Email", "example.com/x", ir.KindColumn, ir.ScanDirect})
		checkColumn(t, column(t, s, "users", "id"), want{"int64", "", ir.KindNumber, ir.ScanDirect})
	})

	t.Run("sqlite matching is on the declared type", func(t *testing.T) {
		s := fixture(t, "sqlite_types.hcl", "sqlite")
		w := mustApply(t, s, Options{Dialect: "sqlite", Nullable: NullPointer, Types: []TypeOverride{
			{DBType: "foo", GoType: "example.com/x.Foo"},
			{DBType: "NUMERIC", GoType: "github.com/shopspring/decimal.Decimal"},
		}})
		if len(w) != 0 {
			t.Errorf("warnings = %q, want none (overridden FOO must not warn)", w)
		}
		checkColumn(t, column(t, s, "things", "extra"), want{"x.Foo", "example.com/x", ir.KindNullColumn, ir.ScanNullPtr})
		checkColumn(t, column(t, s, "things", "price"), want{"decimal.Decimal", "github.com/shopspring/decimal", ir.KindColumn, ir.ScanDirect})
	})

	t.Run("invalid overrides", func(t *testing.T) {
		cases := []struct {
			name string
			o    Options
			subs []string
		}{
			{"bad go_type", with([]TypeOverride{{DBType: "uuid", GoType: "UUID"}}, nil), []string{"types:", `"uuid"`, `go_type "UUID"`}},
			{"empty go_type", with(nil, []ColumnOverride{{Column: "users.age", GoType: ""}}), []string{"columns:", `"users.age"`, "go_type"}},
			{"trailing dot", with([]TypeOverride{{DBType: "uuid", GoType: "github.com/google/uuid."}}, nil), []string{"types:", "go_type"}},
			{"empty db_type", with([]TypeOverride{{DBType: " ", GoType: "string"}}, nil), []string{"types:", "empty db_type"}},
			{"duplicate type", with([]TypeOverride{
				{DBType: "uuid", GoType: "string"},
				{DBType: "UUID", GoType: "github.com/google/uuid.UUID"},
			}, nil), []string{"types:", "duplicate", `"UUID"`}},
			{"duplicate column", with(nil, []ColumnOverride{
				{Column: "users.age", GoType: "int64"},
				{Column: "users.age", GoType: "int32"},
			}), []string{"columns:", "duplicate", `"users.age"`}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				wantContains(t, applyErr(t, users(), tc.o), tc.subs...)
			})
		}
	})
}

func TestParseGoType(t *testing.T) {
	cases := []struct {
		in   string
		want ir.GoType
	}{
		{"github.com/google/uuid.UUID", ir.GoType{Name: "uuid.UUID", ImportPath: "github.com/google/uuid"}},
		{"time.Duration", ir.GoType{Name: "time.Duration", ImportPath: "time"}},
		{"encoding/json.RawMessage", ir.GoType{Name: "json.RawMessage", ImportPath: "encoding/json"}},
		{"github.com/gofrs/uuid/v5.UUID", ir.GoType{Name: "uuid.UUID", ImportPath: "github.com/gofrs/uuid/v5"}},
		{"int64", ir.GoType{Name: "int64"}},
		{"[]byte", ir.GoType{Name: "[]byte"}},
	}
	for _, tc := range cases {
		got, err := parseGoType(tc.in)
		if err != nil {
			t.Errorf("parseGoType(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseGoType(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "UUID", "uuid.", ".UUID", "github.com/google/uuid", "a b.C", "example.com/x.*T"} {
		if _, err := parseGoType(bad); err == nil {
			t.Errorf("parseGoType(%q) succeeded, want error", bad)
		}
	}
}

func TestApplyBadOptions(t *testing.T) {
	wantContains(t, applyErr(t, users(), Options{Dialect: "mysql", Nullable: NullPointer}), `dialect "mysql"`)
	wantContains(t, applyErr(t, users(), Options{Dialect: "postgres", Nullable: "ptr"}), `nullable style "ptr"`)
}

func TestUnknownEnum(t *testing.T) {
	s := &ir.Schema{Dialect: "postgres", Tables: []ir.Table{{DBName: "t", Columns: []ir.Column{
		{DBName: "m", RawType: "mood", Enum: "mood"},
	}}}}
	wantContains(t, applyErr(t, s, Options{Dialect: "postgres", Nullable: NullPointer}), `table "t": column "m"`, `enum "mood"`)
}
