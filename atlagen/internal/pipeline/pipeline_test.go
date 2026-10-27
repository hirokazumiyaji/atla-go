package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/config"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resolve returns the options for schema (a path relative to the temp
// module, or an absolute path) with dialect postgres and out internal/dbgen.
func resolve(t *testing.T, schema string) *config.Resolved {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.24\n")
	dialect, out := "postgres", "internal/dbgen"
	r, err := config.Resolve(dir, config.Flags{Schema: &schema, Dialect: &dialect, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "testdata", "atlas", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func findTable(t *testing.T, s *ir.Schema, name string) ir.Table {
	t.Helper()
	for _, tb := range s.Tables {
		if tb.DBName == name {
			return tb
		}
	}
	t.Fatalf("table %q not found", name)
	return ir.Table{}
}

func TestPipelineRun(t *testing.T) {
	s, warnings, err := Run(context.Background(), resolve(t, fixture(t, "composite.hcl")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	if s.Dialect != "postgres" || s.Name != "public" {
		t.Errorf("schema = %q/%q, want public/postgres", s.Name, s.Dialect)
	}
	m := findTable(t, s, "memberships")
	if m.Model != "Membership" || m.Package != "membership" {
		t.Errorf("memberships model/package = %q/%q, want Membership/membership", m.Model, m.Package)
	}
	for db, goName := range map[string]string{"org_id": "OrgID", "user_id": "UserID"} {
		c, ok := m.Column(db)
		if !ok {
			t.Fatalf("column %q missing", db)
		}
		if c.GoName != goName || c.Kind != ir.KindNumber || c.Scan != ir.ScanDirect || c.GoType.Name != "int64" {
			t.Errorf("column %q = %q kind %q scan %q type %q, want %q NumberColumn direct int64",
				db, c.GoName, c.Kind, c.Scan, c.GoType.Name, goName)
		}
	}
}

func TestPipelineEnumImportPath(t *testing.T) {
	s, _, err := Run(context.Background(), resolve(t, fixture(t, "postgres_users.hcl")), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := findTable(t, s, "users").Column("role")
	want := ir.GoType{Name: "enum.UserRole", ImportPath: "example.com/app/internal/dbgen/enum"}
	if c.GoType != want {
		t.Errorf("role go type = %+v, want %+v", c.GoType, want)
	}
}

func TestPipelineStdin(t *testing.T) {
	data, err := os.ReadFile(fixture(t, "composite.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := Run(context.Background(), resolve(t, "-"), strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tables) != 4 {
		t.Errorf("got %d tables, want 4", len(s.Tables))
	}
}

func TestPipelineRelativeSchema(t *testing.T) {
	r := resolve(t, "db/schema.hcl")
	data, err := os.ReadFile(fixture(t, "composite.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(r.BaseDir, "db", "schema.hcl"), string(data))
	if _, _, err := Run(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineErrorsCarryStage(t *testing.T) {
	tests := []struct {
		name, hcl string
		want      []string
	}{
		{
			name: "mapper",
			hcl: `table "gadgets" {
  schema = schema.public
  column "id" {
    null = false
    type = bigint
  }
  column "blob" {
    null = false
    type = sql("FOO")
  }
}
schema "public" {
}
`,
			want: []string{`table "gadgets": column "blob": unsupported type`},
		},
		{
			name: "parser",
			hcl:  "table \"x\" {\n",
			want: []string{"invalid atlas hcl"},
		},
		{
			name: "naming",
			hcl: `table "clients" {
  schema = schema.public
  column "id" {
    null = false
    type = bigint
  }
}
schema "public" {
}
`,
			want: []string{`table "clients"`, "Client"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := resolve(t, "schema.hcl")
			writeFile(t, r.Schema, tt.hcl)
			_, _, err := Run(context.Background(), r, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}

	t.Run("inspect", func(t *testing.T) {
		r := resolve(t, "missing.hcl")
		if _, _, err := Run(context.Background(), r, nil); err == nil || !strings.Contains(err.Error(), "missing.hcl") {
			t.Errorf("error = %v, want one naming missing.hcl", err)
		}
	})
}
