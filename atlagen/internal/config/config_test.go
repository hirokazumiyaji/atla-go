package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/mapper"
)

// specExample is the generator-spec §1.4 example.
const specExample = `version: 1
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

columns:                   # per column (wins over ` + "`types`" + `)
  - column: users.settings
    go_type: example.com/app/settings.Settings   # must implement sql.Scanner + driver.Valuer

names:                     # identifier overrides
  tables:
    people: Person         # model name when singularization guesses wrong
  edges:
    - fk: posts_author_id_fkey   # FK constraint symbol from Atlas
      name: Author                # edge on the child (M2O)
      inverse: AuthoredPosts      # edge on the parent (O2M / O2O)
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moduleDir returns a temp directory holding go.mod for module example.com/app.
func moduleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.24\n")
	return dir
}

func str(s string) *string { return &s }

func boolp(b bool) *bool { return &b }

func wantErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want one containing %q", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error = %q, want it to contain %q", err, substr)
	}
}

func TestLoadFile(t *testing.T) {
	t.Run("spec example", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "atlagen.yaml")
		writeFile(t, path, specExample)
		got, err := LoadFile(path)
		if err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
		want := &File{
			Version:    1,
			Schema:     "file://db/schema.hcl",
			DevURL:     "docker://postgres/16/dev",
			Dialect:    "postgres",
			SchemaName: "public",
			Out:        "internal/dbgen",
			Package:    "dbgen",
			Nullable:   "pointer",
			JSONTags:   boolp(true),
			Types: []TypeOverride{
				{DBType: "uuid", GoType: "github.com/google/uuid.UUID"},
				{DBType: "numeric", GoType: "github.com/shopspring/decimal.Decimal"},
			},
			Columns: []ColumnOverride{
				{Column: "users.settings", GoType: "example.com/app/settings.Settings"},
			},
			Names: Names{
				Tables: map[string]string{"people": "Person"},
				Edges: []EdgeName{
					{FK: "posts_author_id_fkey", Name: "Author", Inverse: "AuthoredPosts"},
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("LoadFile =\n%+v\nwant\n%+v", got, want)
		}
	})

	tests := []struct {
		name, yaml, want string
	}{
		{"unknown key", "version: 1\ndialekt: postgres\n", "dialekt"},
		{"unknown nested key", "version: 1\ntypes:\n  - db_type: uuid\n    gotype: x.Y\n", "gotype"},
		{"version missing", "dialect: postgres\n", "version: 1"},
		{"version 2", "version: 2\n", "version: 1"},
		{"empty file", "", "version: 1"},
		{"edge missing fk", "version: 1\nnames:\n  edges:\n    - name: Author\n", "fk"},
		{"edge missing name", "version: 1\nnames:\n  edges:\n    - fk: posts_author_id_fkey\n", "name"},
		{"type missing db_type", "version: 1\ntypes:\n  - go_type: x.Y\n", "db_type"},
		{"type missing go_type", "version: 1\ntypes:\n  - db_type: uuid\n", "go_type"},
		{"column missing column", "version: 1\ncolumns:\n  - go_type: x.Y\n", "column"},
		{"column missing go_type", "version: 1\ncolumns:\n  - column: users.id\n", "go_type"},
		{"wrong shape", "version: 1\ntypes: uuid\n", "line 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "atlagen.yaml")
			writeFile(t, path, tt.yaml)
			got, err := LoadFile(path)
			if err == nil {
				t.Fatalf("LoadFile = %+v, want error containing %q", got, tt.want)
			}
			wantErr(t, err, tt.want)
			wantErr(t, err, path)
		})
	}

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nope.yaml")
		_, err := LoadFile(path)
		wantErr(t, err, "nope.yaml")
	})
}

func TestResolveDefaults(t *testing.T) {
	cwd := moduleDir(t)
	got, err := Resolve(cwd, Flags{Dialect: str("postgres"), Out: str("internal/dbgen")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := &Resolved{
		Schema:     "-",
		BaseDir:    cwd,
		Dialect:    "postgres",
		OutDir:     filepath.Join(cwd, "internal", "dbgen"),
		Package:    "dbgen",
		ImportPath: "example.com/app/internal/dbgen",
		Nullable:   mapper.NullPointer,
		JSONTags:   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve =\n%+v\nwant\n%+v", got, want)
	}
}

func TestResolveFullConfig(t *testing.T) {
	root := moduleDir(t)
	writeFile(t, filepath.Join(root, "atlagen.yaml"), specExample)
	got, err := Resolve(root, Flags{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := &Resolved{
		Schema:     "file://db/schema.hcl",
		DevURL:     "docker://postgres/16/dev",
		BaseDir:    root,
		Dialect:    "postgres",
		SchemaName: "public",
		OutDir:     filepath.Join(root, "internal", "dbgen"),
		Package:    "dbgen",
		ImportPath: "example.com/app/internal/dbgen",
		Nullable:   mapper.NullPointer,
		JSONTags:   true,
		Types: []mapper.TypeOverride{
			{DBType: "uuid", GoType: "github.com/google/uuid.UUID"},
			{DBType: "numeric", GoType: "github.com/shopspring/decimal.Decimal"},
		},
		Columns: []mapper.ColumnOverride{
			{Column: "users.settings", GoType: "example.com/app/settings.Settings"},
		},
		TableNames: map[string]string{"people": "Person"},
		Edges: []EdgeName{
			{FK: "posts_author_id_fkey", Name: "Author", Inverse: "AuthoredPosts"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve =\n%+v\nwant\n%+v", got, want)
	}
}

func TestResolveFlagsOverrideConfig(t *testing.T) {
	root := moduleDir(t)
	writeFile(t, filepath.Join(root, "atlagen.yaml"),
		"version: 1\ndialect: sqlite\nout: gen\njson_tags: false\nnullable: sqlnull\nschema_name: main\n")

	got, err := Resolve(root, Flags{Dialect: str("postgres")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Dialect != "postgres" {
		t.Errorf("Dialect = %q, want postgres (flag wins)", got.Dialect)
	}
	if got.JSONTags {
		t.Error("JSONTags = true, want false (config value, no flag)")
	}
	if got.Nullable != mapper.NullSQLNull {
		t.Errorf("Nullable = %q, want sqlnull (config value)", got.Nullable)
	}
	if got.SchemaName != "main" {
		t.Errorf("SchemaName = %q, want main (config value)", got.SchemaName)
	}

	got, err = Resolve(root, Flags{
		JSONTags:   boolp(true),
		Nullable:   str("pointer"),
		SchemaName: str("public"),
		Package:    str("db"),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.JSONTags {
		t.Error("JSONTags = false, want true (flag wins)")
	}
	if got.Nullable != mapper.NullPointer {
		t.Errorf("Nullable = %q, want pointer (flag wins)", got.Nullable)
	}
	if got.SchemaName != "public" {
		t.Errorf("SchemaName = %q, want public (flag wins)", got.SchemaName)
	}
	if got.Package != "db" {
		t.Errorf("Package = %q, want db (flag)", got.Package)
	}
	if got.Dialect != "sqlite" {
		t.Errorf("Dialect = %q, want sqlite (config value)", got.Dialect)
	}
}

func TestResolveDefaultConfigDiscovery(t *testing.T) {
	t.Run("atlagen.yaml in cwd is used", func(t *testing.T) {
		root := moduleDir(t)
		writeFile(t, filepath.Join(root, "atlagen.yaml"), "version: 1\ndialect: sqlite\nout: gen\n")
		got, err := Resolve(root, Flags{})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Dialect != "sqlite" || got.OutDir != filepath.Join(root, "gen") {
			t.Errorf("Resolve = %+v, want dialect sqlite and out %s from atlagen.yaml", got, filepath.Join(root, "gen"))
		}
	})
	t.Run("absent: flags only", func(t *testing.T) {
		root := moduleDir(t)
		got, err := Resolve(root, Flags{Dialect: str("sqlite"), Out: str("gen")})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Dialect != "sqlite" || got.OutDir != filepath.Join(root, "gen") {
			t.Errorf("Resolve = %+v", got)
		}
		_, err = Resolve(root, Flags{})
		wantErr(t, err, "dialect is required")
	})
	t.Run("explicit config missing", func(t *testing.T) {
		root := moduleDir(t)
		_, err := Resolve(root, Flags{Config: str("other.yaml"), Dialect: str("sqlite"), Out: str("gen")})
		wantErr(t, err, "other.yaml")
	})
	t.Run("invalid default config is an error", func(t *testing.T) {
		root := moduleDir(t)
		writeFile(t, filepath.Join(root, "atlagen.yaml"), "version: 2\n")
		_, err := Resolve(root, Flags{Dialect: str("sqlite"), Out: str("gen")})
		wantErr(t, err, "version: 1")
	})
}

func TestResolveRelativeToConfigDir(t *testing.T) {
	setup := func(t *testing.T, yaml string) (repo, cwd string) {
		t.Helper()
		repo = moduleDir(t)
		writeFile(t, filepath.Join(repo, "atlagen.yaml"), yaml)
		cwd = filepath.Join(repo, "internal", "dbgen")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		return repo, cwd
	}

	t.Run("schema file and out", func(t *testing.T) {
		repo, cwd := setup(t, "version: 1\ndialect: postgres\nout: internal/dbgen\nschema: db/schema.json\n")
		got, err := Resolve(cwd, Flags{Config: str("../../atlagen.yaml")})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if want := filepath.Join(repo, "internal", "dbgen"); got.OutDir != want {
			t.Errorf("OutDir = %q, want %q", got.OutDir, want)
		}
		if want := filepath.Join(repo, "db", "schema.json"); got.Schema != want {
			t.Errorf("Schema = %q, want %q", got.Schema, want)
		}
		if got.BaseDir != repo {
			t.Errorf("BaseDir = %q, want %q", got.BaseDir, repo)
		}
		if got.ImportPath != "example.com/app/internal/dbgen" {
			t.Errorf("ImportPath = %q", got.ImportPath)
		}
	})

	t.Run("schema URL", func(t *testing.T) {
		repo, cwd := setup(t, "version: 1\ndialect: postgres\nout: internal/dbgen\nschema: file://db/schema.hcl\n")
		got, err := Resolve(cwd, Flags{Config: str("../../atlagen.yaml")})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Schema != "file://db/schema.hcl" {
			t.Errorf("Schema = %q, want it unchanged", got.Schema)
		}
		if got.BaseDir != repo {
			t.Errorf("BaseDir = %q, want %q", got.BaseDir, repo)
		}
	})

	t.Run("flags resolve against cwd", func(t *testing.T) {
		_, cwd := setup(t, "version: 1\ndialect: postgres\nout: internal/dbgen\nschema: db/schema.json\n")
		got, err := Resolve(cwd, Flags{
			Config: str("../../atlagen.yaml"),
			Out:    str("./x"),
			Schema: str("file://local.hcl"),
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if want := filepath.Join(cwd, "x"); got.OutDir != want {
			t.Errorf("OutDir = %q, want %q", got.OutDir, want)
		}
		if got.Schema != "file://local.hcl" {
			t.Errorf("Schema = %q, want it unchanged", got.Schema)
		}
		if got.BaseDir != cwd {
			t.Errorf("BaseDir = %q, want %q (schema came from a flag)", got.BaseDir, cwd)
		}
		if got.ImportPath != "example.com/app/internal/dbgen/x" {
			t.Errorf("ImportPath = %q", got.ImportPath)
		}

		got, err = Resolve(cwd, Flags{Config: str("../../atlagen.yaml"), Schema: str("s.hcl")})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if want := filepath.Join(cwd, "s.hcl"); got.Schema != want {
			t.Errorf("Schema = %q, want %q", got.Schema, want)
		}
	})

	t.Run("absolute paths are kept", func(t *testing.T) {
		repo, cwd := setup(t, "version: 1\ndialect: postgres\n")
		abs := filepath.Join(repo, "abs")
		got, err := Resolve(cwd, Flags{Config: str("../../atlagen.yaml"), Out: str(abs), Schema: str("-")})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.OutDir != abs || got.Schema != "-" {
			t.Errorf("OutDir = %q, Schema = %q, want %q and -", got.OutDir, got.Schema, abs)
		}
	})
}

func TestResolveValidation(t *testing.T) {
	tests := []struct {
		name  string
		flags Flags
		want  string
	}{
		{"missing dialect", Flags{Out: str("gen")}, "dialect is required"},
		{"mysql", Flags{Dialect: str("mysql"), Out: str("gen")}, `dialect "mysql" is not supported yet`},
		{"tidb", Flags{Dialect: str("tidb"), Out: str("gen")}, `dialect "tidb" is not supported yet`},
		{"unknown dialect", Flags{Dialect: str("oracle"), Out: str("gen")}, "unknown dialect"},
		{"missing out", Flags{Dialect: str("postgres")}, "out is required"},
		{"bad nullable", Flags{Dialect: str("postgres"), Out: str("gen"), Nullable: str("ptr")}, "pointer|sqlnull"},
		{"underivable package", Flags{Dialect: str("postgres"), Out: str("db-gen")}, "set --package"},
		{"uppercase package", Flags{Dialect: str("postgres"), Out: str("gen"), Package: str("Bad")}, `"Bad"`},
		{"keyword package", Flags{Dialect: str("postgres"), Out: str("gen"), Package: str("type")}, `"type"`},
		{"dev-url with stdin", Flags{Dialect: str("postgres"), Out: str("gen"), DevURL: str("docker://postgres/16/dev")}, "--dev-url requires"},
		{"dev-url with file", Flags{Dialect: str("postgres"), Out: str("gen"), Schema: str("schema.hcl"), DevURL: str("docker://postgres/16/dev")}, "--dev-url requires"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(moduleDir(t), tt.flags)
			if err == nil {
				t.Fatalf("Resolve = %+v, want error containing %q", got, tt.want)
			}
			wantErr(t, err, tt.want)
		})
	}

	t.Run("dev-url with URL schema is fine", func(t *testing.T) {
		got, err := Resolve(moduleDir(t), Flags{
			Dialect: str("postgres"), Out: str("gen"),
			Schema: str("file://schema.hcl"), DevURL: str("docker://postgres/16/dev"),
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.DevURL != "docker://postgres/16/dev" {
			t.Errorf("DevURL = %q", got.DevURL)
		}
	})

	t.Run("no go.mod above out", func(t *testing.T) {
		_, err := Resolve(t.TempDir(), Flags{Dialect: str("postgres"), Out: str("gen")})
		wantErr(t, err, "go.mod")
	})
}

func TestModuleImportPath(t *testing.T) {
	root := moduleDir(t)
	nested := filepath.Join(root, "internal", "dbgen")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	innerMod := filepath.Join(root, "tools")
	writeFile(t, filepath.Join(innerMod, "go.mod"), "module example.com/tools\n")

	tests := []struct {
		name, dir, want string
	}{
		{"nested existing dir", nested, "example.com/app/internal/dbgen"},
		{"not yet existing dir", filepath.Join(root, "gen", "db"), "example.com/app/gen/db"},
		{"module root", root, "example.com/app"},
		{"nearest go.mod wins", filepath.Join(innerMod, "gen"), "example.com/tools/gen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ModuleImportPath(tt.dir)
			if err != nil {
				t.Fatalf("ModuleImportPath: %v", err)
			}
			if got != tt.want {
				t.Errorf("ModuleImportPath = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("no go.mod", func(t *testing.T) {
		_, err := ModuleImportPath(filepath.Join(t.TempDir(), "gen"))
		wantErr(t, err, "go.mod")
	})
	t.Run("go.mod without module", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "go 1.24\n")
		_, err := ModuleImportPath(dir)
		wantErr(t, err, "module")
	})
	t.Run("unreadable go.mod is an error", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("file permissions are not enforced on windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root can read a 0o000 file")
		}
		outer := moduleDir(t)
		inner := filepath.Join(outer, "sub")
		gomod := filepath.Join(inner, "go.mod")
		writeFile(t, gomod, "module example.com/sub\n")
		if err := os.Chmod(gomod, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(gomod, 0o644) })
		got, err := ModuleImportPath(filepath.Join(inner, "gen"))
		if err == nil {
			t.Fatalf("ModuleImportPath = %q, want an error for the unreadable go.mod", got)
		}
		wantErr(t, err, "reading "+gomod)
	})
}
