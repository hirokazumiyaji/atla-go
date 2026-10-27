package inspect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fixtureHCL = "table \"users\" {\n  schema = schema.public\n}\n"

// installFakeAtlas writes an executable `atlas` shell script into a fresh
// directory, puts that directory first on PATH, and returns it. The script
// appends its arguments to args.txt, records its working directory in
// pwd.txt, prints out.txt to stdout and exits with $FAKE_ATLAS_EXIT. When
// extra is non-empty it is run before the output is printed.
func installFakeAtlas(t *testing.T, extra string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake atlas is a sh script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + bin + "/args.txt'\n" +
		"pwd -P > '" + bin + "/pwd.txt'\n" +
		extra +
		"cat '" + bin + "/out.txt'\n" +
		"exit \"${FAKE_ATLAS_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "atlas"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "out.txt"), []byte(fixtureHCL), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func slurp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIsURL(t *testing.T) {
	for in, want := range map[string]bool{
		"file://schema.hcl":             true,
		"postgres://u@h/db":             true,
		"docker://postgres/16/dev":      true,
		"schema.hcl":                    false,
		"/abs/path/schema.hcl":          false,
		"":                              false,
		"-":                             false,
		"rel/dir/with:colon/schema.hcl": false,
		"C:\\dir\\schema.hcl":           false,
	} {
		if got := IsURL(in); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoadStdin(t *testing.T) {
	for _, schema := range []string{"-", ""} {
		got, err := Load(context.Background(), Source{Schema: schema, Stdin: strings.NewReader(fixtureHCL)})
		if err != nil {
			t.Fatalf("Schema %q: %v", schema, err)
		}
		if string(got) != fixtureHCL {
			t.Errorf("Schema %q: got %q, want %q", schema, got, fixtureHCL)
		}
	}
}

func TestLoadStdinNil(t *testing.T) {
	if _, err := Load(context.Background(), Source{Schema: "-"}); err == nil {
		t.Fatal("expected error for nil Stdin")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "schema.hcl")
	if err := os.WriteFile(abs, []byte(fixtureHCL), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("absolute", func(t *testing.T) {
		got, err := Load(context.Background(), Source{Schema: abs, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != fixtureHCL {
			t.Errorf("got %q", got)
		}
	})

	t.Run("relative with Dir", func(t *testing.T) {
		got, err := Load(context.Background(), Source{Schema: "schema.hcl", Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != fixtureHCL {
			t.Errorf("got %q", got)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := Load(context.Background(), Source{Schema: "nope.hcl", Dir: dir})
		if err == nil {
			t.Fatal("expected error")
		}
		if want := filepath.Join(dir, "nope.hcl"); !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	})
}

func TestLoadAtlas(t *testing.T) {
	bin := installFakeAtlas(t, "")
	d := t.TempDir()

	got, err := Load(context.Background(), Source{
		Schema: "file://schema.hcl",
		DevURL: "docker://postgres/16/dev",
		Dir:    d,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fixtureHCL {
		t.Errorf("got %q, want %q", got, fixtureHCL)
	}
	wantArgs := "schema inspect -u file://schema.hcl --dev-url docker://postgres/16/dev\n"
	if a := slurp(t, filepath.Join(bin, "args.txt")); a != wantArgs {
		t.Errorf("args = %q, want %q", a, wantArgs)
	}
	wantDir, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.TrimSpace(slurp(t, filepath.Join(bin, "pwd.txt"))); p != wantDir {
		t.Errorf("pwd = %q, want %q", p, wantDir)
	}
}

func TestLoadAtlasWithoutDevURL(t *testing.T) {
	bin := installFakeAtlas(t, "")
	if _, err := Load(context.Background(), Source{Schema: "postgres://u:p@h/db", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	want := "schema inspect -u postgres://u:p@h/db\n"
	if a := slurp(t, filepath.Join(bin, "args.txt")); a != want {
		t.Errorf("args = %q, want %q", a, want)
	}
}

func TestLoadAtlasMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Load(context.Background(), Source{Schema: "file://schema.hcl"})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"atlas CLI not found on PATH", "https://atlasgo.io"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestLoadAtlasFails(t *testing.T) {
	installFakeAtlas(t, "echo boom >&2\n")
	t.Setenv("FAKE_ATLAS_EXIT", "1")
	_, err := Load(context.Background(), Source{Schema: "file://schema.hcl", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"atlas schema inspect failed", "boom"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestLoadAtlasStderrTruncated(t *testing.T) {
	installFakeAtlas(t, "head -c 10000 /dev/zero | tr '\\0' 'x' >&2\n")
	t.Setenv("FAKE_ATLAS_EXIT", "1")
	_, err := Load(context.Background(), Source{Schema: "file://schema.hcl", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
	run := strings.Repeat("x", maxStderr)
	if !strings.Contains(err.Error(), run) {
		t.Errorf("error does not carry %d bytes of stderr", maxStderr)
	}
	if strings.Contains(err.Error(), run+"x") {
		t.Errorf("stderr in error exceeds %d bytes", maxStderr)
	}
}

func TestLoadEmpty(t *testing.T) {
	t.Run("stdin", func(t *testing.T) {
		_, err := Load(context.Background(), Source{Schema: "-", Stdin: bytes.NewReader(nil)})
		if err == nil || !strings.Contains(err.Error(), "empty schema input") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "empty.hcl"), []byte("\n  \n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(context.Background(), Source{Schema: "empty.hcl", Dir: dir})
		if err == nil || !strings.Contains(err.Error(), "empty schema input") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("atlas", func(t *testing.T) {
		bin := installFakeAtlas(t, "")
		if err := os.WriteFile(filepath.Join(bin, "out.txt"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(context.Background(), Source{Schema: "file://schema.hcl", Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "empty schema input") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestLoadContextCancel(t *testing.T) {
	installFakeAtlas(t, "exec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Load(ctx, Source{Schema: "file://schema.hcl", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Load took %v after cancellation", elapsed)
	}
}

// blockingReader blocks in Read until release is closed.
type blockingReader struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingReader) Read([]byte) (int, error) {
	close(r.started)
	<-r.release
	return 0, io.EOF
}

func TestLoadStdinContextCancel(t *testing.T) {
	r := &blockingReader{started: make(chan struct{}), release: make(chan struct{})}
	defer close(r.release)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Load(ctx, Source{Stdin: r})
		done <- err
	}()
	<-r.started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load did not return after cancellation")
	}
}

func TestLoadFileContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mkfifo is not available on windows")
	}
	// Opening a FIFO for reading blocks until a writer connects, so this
	// mimics a schema file on a hung network mount.
	path := filepath.Join(t.TempDir(), "schema.hcl")
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Skipf("mkfifo: %v: %s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Load(ctx, Source{Schema: path})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Load took %v after cancellation", elapsed)
	}
}

func TestRedactStderr(t *testing.T) {
	const dsn = "postgres://user:s3cret@db:5432/app"
	tests := []struct{ name, in, want string }{
		{"whole URL", "dial " + dsn + ": refused", "dial postgres://user:xxxxx@db:5432/app: refused"},
		{"rewritten URL", `dial failed for "postgres://user:s3cret@db:9999/app"`, `dial failed for "postgres://user:xxxxx@db:9999/app"`},
		{"no password", "dial postgres://user@db:5432/app: refused", "dial postgres://user@db:5432/app: refused"},
	}
	for _, tt := range tests {
		// A URL without a password and a non-URL must be ignored.
		got := redactStderr(tt.in, dsn, "sqlite://dev.db", "", "not a url")
		if got != tt.want {
			t.Errorf("%s: redactStderr = %q, want %q", tt.name, got, tt.want)
		}
	}

	// A short password must not rewrite unrelated occurrences.
	got := redactStderr("error: encoding failed", "postgres://u:e@db/app")
	if want := "error: encoding failed"; got != want {
		t.Errorf("short password: redactStderr = %q, want %q", got, want)
	}

	// net/url rejects this URL, but atlas may still echo it.
	malformed := "postgres://user:review%ss@db/app"
	if got, want := redactStderr("bad "+malformed, malformed), "bad postgres://user:xxxxx@db/app"; got != want {
		t.Errorf("malformed: redactStderr = %q, want %q", got, want)
	}

	// net/url may echo only a fragment of a malformed URL.
	if got, want := redactStderr(`invalid port ":dummysecret" after host`, "postgres://user:dummysecret/@db/app"), `invalid port ":xxxxx" after host`; got != want {
		t.Errorf("bad port: redactStderr = %q, want %q", got, want)
	}
	if got, want := redactStderr(`invalid URL escape "%ss"`, "postgres://review:review%ss@localhost/app"), `invalid URL escape "xxxxx"`; got != want {
		t.Errorf("bad escape: redactStderr = %q, want %q", got, want)
	}

	// The stderr cap can cut the password before its closing '@'.
	truncated := "bad postgres://user:s3c"
	if got, want := redactStderr(truncated, dsn), "bad postgres://user:xxxxx"; got != want {
		t.Errorf("truncated: redactStderr = %q, want %q", got, want)
	}

	// Every occurrence must be masked, not just the first one.
	in := "a postgres://user:s3cret@db/app b postgres://user:s3c"
	if got, want := redactStderr(in, dsn), "a postgres://user:xxxxx@db/app b postgres://user:xxxxx"; got != want {
		t.Errorf("repeated: redactStderr = %q, want %q", got, want)
	}
}

func TestCappedBuffer(t *testing.T) {
	w := &cappedBuffer{max: 5}
	if n, err := w.Write([]byte("abcdefgh")); n != 8 || err != nil {
		t.Fatalf("Write = %d, %v; want 8, nil", n, err)
	}
	if _, err := w.Write([]byte("ij")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := w.buf.String(), "abcde"; got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestLoadRedactsAtlasStderr(t *testing.T) {
	const schema = "postgres://user:s3cret@db:5432/app"
	installFakeAtlas(t, "echo \"connect $* failed\" >&2\n")
	t.Setenv("FAKE_ATLAS_EXIT", "1")

	_, err := Load(context.Background(), Source{Schema: schema, DevURL: "docker://postgres/16/dev"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "s3cret") {
		t.Errorf("error leaks the password: %s", msg)
	}
	if !strings.Contains(msg, "xxxxx") {
		t.Errorf("error = %q, want the password masked", msg)
	}
}

func TestLoadRedactsStderrBeforeTruncation(t *testing.T) {
	const schema = "postgres://user:s3cret@db/app"
	// Put the length cap three bytes into the password: the partial userinfo
	// must still be masked when the buffer ends there.
	padding := maxStderr - len("postgres://user:") - 3
	installFakeAtlas(t, fmt.Sprintf(
		"printf '%%%ds' '' | tr ' ' x >&2\nprintf '%%s\\n' %q >&2\n", padding, schema))
	t.Setenv("FAKE_ATLAS_EXIT", "1")

	_, err := Load(context.Background(), Source{Schema: schema})
	if err == nil {
		t.Fatal("expected error")
	}
	if msg := err.Error(); strings.Contains(msg, "s3c") || strings.Contains(msg, "s3cret") {
		t.Errorf("error leaks part of the password: %q", msg)
	}
}
