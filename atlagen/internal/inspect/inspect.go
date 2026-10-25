// Package inspect loads the Atlas schema text that the parser reads: from
// standard input, from a file, or by running `atlas schema inspect`.
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
	"strings"
	"time"
)

// maxStderr caps how much of atlas's stderr is carried in an error.
const maxStderr = 4096

// Source describes where the schema text comes from.
type Source struct {
	// Schema is "" or "-" (read Stdin), an Atlas URL (anything containing
	// "://", inspected with the atlas CLI), or a file path.
	Schema string
	// DevURL is passed to atlas as --dev-url when Schema is an Atlas URL.
	// It is ignored for the other sources.
	DevURL string
	// Dir is the working directory of the atlas subprocess and the base
	// for a relative file path. Empty means the current directory.
	Dir string
	// Stdin is read when Schema is "" or "-".
	Stdin io.Reader
}

// IsURL reports whether schema is an Atlas URL rather than a file path.
func IsURL(schema string) bool {
	return strings.Contains(schema, "://")
}

// Load returns the raw schema text for src. It fails with "empty schema
// input" when the source yields nothing but whitespace.
func Load(ctx context.Context, src Source) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	switch {
	case src.Schema == "" || src.Schema == "-":
		data, err = readStdin(src.Stdin)
	case IsURL(src.Schema):
		data, err = runAtlas(ctx, src)
	default:
		data, err = readFile(src)
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty schema input")
	}
	return data, nil
}

func readStdin(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("reading schema from stdin: no input stream")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading schema from stdin: %w", err)
	}
	return data, nil
}

func readFile(src Source) ([]byte, error) {
	path := src.Schema
	if src.Dir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(src.Dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file %q: %w", path, err)
	}
	return data, nil
}

func runAtlas(ctx context.Context, src Source) ([]byte, error) {
	path, err := exec.LookPath("atlas")
	if err != nil {
		return nil, errors.New("atlas CLI not found on PATH; install it from https://atlasgo.io or pass a schema file or '-' for stdin")
	}
	args := []string{"schema", "inspect", "-u", src.Schema}
	if src.DevURL != "" {
		args = append(args, "--dev-url", src.DevURL)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = src.Dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A killed atlas may leave grandchildren holding the pipes; do not wait
	// for them forever.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("atlas schema inspect canceled: %w", ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > maxStderr {
			msg = strings.ToValidUTF8(msg[:maxStderr], "")
		}
		if msg == "" {
			return nil, fmt.Errorf("atlas schema inspect failed: %w", err)
		}
		return nil, fmt.Errorf("atlas schema inspect failed: %w: %s", err, msg)
	}
	return stdout.Bytes(), nil
}
