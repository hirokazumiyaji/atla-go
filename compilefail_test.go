package atla_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compileFailCases maps each testdata/compilefail directory to the substring
// its build output must contain. An empty string means the build must succeed.
var compileFailCases = []struct {
	dir  string
	want string
}{
	{"ok", ""},
	{"eq_wrong_type", `cannot use "20"`},
	{"expr_type_mismatch", "does not implement atla.Expr[int64]"},
	{"opt_wrong_type", `cannot use atla.Some("x")`},
	{"expr_as_pred", "does not implement atla.Expr[bool]"},
	{"null_vs_nonnull", "does not implement atla.Expr[int64]"},
	{"sealed", "missing method"},
}

func goBinary(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	p := filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("go binary not found: %v", err)
	}
	return p
}

// TestCompileFail checks the type-safety claims: each case is built in a
// throwaway module that depends on this repository, and must fail with the
// expected compiler diagnostic.
func TestCompileFail(t *testing.T) {
	if testing.Short() {
		t.Skip("compile-fail suite spawns the go tool; skipped in -short mode")
	}
	goBin := goBinary(t)
	// The test's working directory is the package directory, i.e. the repo root.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range compileFailCases {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(filepath.Join(root, "testdata", "compilefail", tc.dir, "main.go"))
			if err != nil {
				t.Fatal(err)
			}
			tmp := t.TempDir()
			gomod := "module cf\n\ngo 1.24.0\n\n" +
				"require github.com/hirokazumiyaji/atla-go v0.0.0\n\n" +
				"replace github.com/hirokazumiyaji/atla-go => " + root + "\n"
			if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tmp, "main.go"), src, 0o600); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(goBin, "build", "./...")
			cmd.Dir = tmp
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			out, err := cmd.CombinedOutput()

			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected build to succeed: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected build to fail with %q, but it succeeded", tc.want)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("build output does not contain %q:\n%s", tc.want, out)
			}
		})
	}
}
