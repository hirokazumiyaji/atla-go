package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// moduleDir returns a temp directory holding go.mod for module example.com/app.
func moduleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "internal", "testdata", "atlas", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, cwd string, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr, cwd)
	return result{code, stdout.String(), stderr.String()}
}

func TestCLIVersion(t *testing.T) {
	got := runCLI(t, t.TempDir(), "", "version")
	if got.code != 0 || got.stdout != "atlagen devel\n" || got.stderr != "" {
		t.Errorf("version = %+v, want exit 0 and %q", got, "atlagen devel\n")
	}
}

func TestVersionString(t *testing.T) {
	for in, want := range map[string]string{"": "devel", "(devel)": "devel", "v0.2.0": "v0.2.0"} {
		if got := versionString(in); got != want {
			t.Errorf("versionString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCLIIRGolden(t *testing.T) {
	for _, name := range []string{"postgres_users", "composite"} {
		t.Run(name, func(t *testing.T) {
			got := runCLI(t, moduleDir(t), "", "ir", "-s", fixture(t, name+".hcl"), "--dialect", "postgres", "-o", "internal/dbgen")
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
			}
			golden := filepath.Join("..", "..", "internal", "testdata", "golden", name+".ir.json")
			if *update {
				if err := os.WriteFile(golden, []byte(got.stdout), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got.stdout != string(want) {
				t.Errorf("stdout differs from %s (run with -update to rewrite):\n%s", golden, got.stdout)
			}
		})
	}
}

func TestCLIIRStdin(t *testing.T) {
	path := fixture(t, "composite.hcl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := moduleDir(t)
	fromFile := runCLI(t, dir, "", "ir", "--schema", path, "--dialect", "postgres", "--out", "internal/dbgen")
	fromStdin := runCLI(t, dir, string(data), "ir", "--schema", "-", "--dialect", "postgres", "--out", "internal/dbgen")
	if fromFile.code != 0 || fromStdin.code != 0 {
		t.Fatalf("exit codes %d/%d, stderr %q / %q", fromFile.code, fromStdin.code, fromFile.stderr, fromStdin.stderr)
	}
	if fromStdin.stdout != fromFile.stdout {
		t.Errorf("stdin output differs from file output")
	}
	// --schema defaults to stdin.
	if def := runCLI(t, dir, string(data), "ir", "--dialect", "postgres", "-o", "internal/dbgen"); def.stdout != fromFile.stdout {
		t.Errorf("default --schema output differs: exit %d, stderr %q", def.code, def.stderr)
	}
}

func TestCLIAliasLastWins(t *testing.T) {
	dir := moduleDir(t)
	users, composite := fixture(t, "postgres_users.hcl"), fixture(t, "composite.hcl")
	want := runCLI(t, dir, "", "ir", "-s", composite, "--dialect", "postgres", "-o", "internal/dbgen")
	got := runCLI(t, dir, "", "ir", "--schema", users, "-s", composite, "--dialect", "postgres", "--out", "other", "-o", "internal/dbgen")
	if got.code != 0 || got.stdout != want.stdout {
		t.Errorf("last alias did not win: exit %d, stderr %q", got.code, got.stderr)
	}
}

func TestCLIFlagsOverrideConfig(t *testing.T) {
	dir := moduleDir(t)
	cfg := "version: 1\nschema: " + fixture(t, "composite.hcl") + "\ndialect: postgres\nout: internal/dbgen\nnullable: pointer\n"
	if err := os.WriteFile(filepath.Join(dir, "atlagen.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, dir, "", "ir", "--nullable", "sqlnull", "--json-tags=false", "--package", "db")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	// deleted_at is nullable: with sqlnull its scan plan is direct, not nullptr.
	if !strings.Contains(got.stdout, `"db_name": "deleted_at"`) || strings.Contains(got.stdout, `"nullptr"`) {
		t.Errorf("--nullable sqlnull not applied:\n%s", got.stdout)
	}

	// --config names another file; a bad one is a runtime error.
	got = runCLI(t, dir, "", "ir", "--config", "nope.yaml")
	if got.code != 1 || !strings.HasPrefix(got.stderr, "atlagen: reading config") {
		t.Errorf("--config nope.yaml = %+v, want exit 1 reading config error", got)
	}
}

func TestCLIWarnings(t *testing.T) {
	got := runCLI(t, moduleDir(t), "", "ir", "-s", fixture(t, "sqlite_types.hcl"), "--dialect", "sqlite", "-o", "internal/dbgen")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "atlagen: warning: ") || !strings.Contains(got.stderr, "FOO") {
		t.Errorf("stderr = %q, want a warning about FOO", got.stderr)
	}
	if !strings.HasPrefix(got.stdout, "{\n") {
		t.Errorf("stdout = %q, want the IR", got.stdout)
	}
}

func TestCLIUsageErrors(t *testing.T) {
	dir := moduleDir(t)
	for _, args := range [][]string{
		nil,
		{"-h"},
		{"--help"},
		{"bogus"},
		{"ir", "--nope"},
		{"ir", "-h"},
		{"ir", "extra"},
		{"ir", "--json-tags=maybe"},
		{"version", "extra"},
	} {
		got := runCLI(t, dir, "", args...)
		if got.code != 2 {
			t.Errorf("%q: exit %d, want 2", args, got.code)
		}
		if got.stdout != "" {
			t.Errorf("%q: stdout %q, want empty", args, got.stdout)
		}
		if !strings.Contains(got.stderr, "Usage: atlagen") {
			t.Errorf("%q: stderr %q, want usage", args, got.stderr)
		}
	}
	top := runCLI(t, dir, "")
	for _, cmd := range []string{"ir", "version"} {
		if !strings.Contains(top.stderr, "  "+cmd) {
			t.Errorf("usage %q does not list %q", top.stderr, cmd)
		}
	}
}

func TestCLIRuntimeErrors(t *testing.T) {
	dir := moduleDir(t)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"ir", "-o", "internal/dbgen"}, "atlagen: dialect is required"},
		{[]string{"ir", "--dialect", "postgres"}, "atlagen: out is required"},
		{[]string{"ir", "--dialect", "postgres", "-o", "x", "-s", "schema.hcl", "--dev-url", "docker://postgres/16/dev"}, "atlagen: --dev-url requires"},
		{[]string{"ir", "--dialect", "postgres", "-o", "x", "-s", "missing.hcl"}, "atlagen: "},
		{[]string{"ir", "--dialect", "postgres", "-o", "x"}, "atlagen: empty schema input"},
	}
	for _, tt := range tests {
		got := runCLI(t, dir, "", tt.args...)
		if got.code != 1 || !strings.HasPrefix(got.stderr, tt.want) || got.stdout != "" {
			t.Errorf("%q = %+v, want exit 1 and stderr starting %q", tt.args, got, tt.want)
		}
		if strings.Contains(got.stderr, "Usage:") {
			t.Errorf("%q: runtime error printed usage", tt.args)
		}
	}
}

func TestCLIDeterministic(t *testing.T) {
	dir := moduleDir(t)
	args := []string{"ir", "-s", fixture(t, "composite.hcl"), "--dialect", "postgres", "-o", "internal/dbgen"}
	a, b := runCLI(t, dir, "", args...), runCLI(t, dir, "", args...)
	if a.code != 0 || a.stdout != b.stdout {
		t.Errorf("outputs differ (exit %d)", a.code)
	}
}

func TestCLIJoinedErrors(t *testing.T) {
	table := func(name string) string {
		return "table \"" + name + "\" {\n  schema = schema.main\n  column \"id\" {\n    null = false\n    type = integer\n  }\n}\n"
	}
	// Two naming clashes: user/users share a model, client is reserved.
	hcl := table("client") + table("user") + table("users") + "schema \"main\" {\n}\n"
	got := runCLI(t, moduleDir(t), hcl, "ir", "--dialect", "sqlite", "-o", "internal/dbgen")
	if got.code != 1 || got.stdout != "" {
		t.Fatalf("exit %d, stdout %q, want exit 1 and no output", got.code, got.stdout)
	}
	lines := strings.Split(strings.TrimSuffix(got.stderr, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stderr has %d lines, want 2:\n%s", len(lines), got.stderr)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "atlagen: ") {
			t.Errorf("line %q does not start with %q", l, "atlagen: ")
		}
	}
	if !strings.Contains(lines[0], `"User"`) || !strings.Contains(lines[1], `"Client"`) {
		t.Errorf("stderr = %q, want the User then the Client clash", got.stderr)
	}
}
