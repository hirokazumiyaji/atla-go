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
		data, err = readStdin(ctx, src.Stdin)
	case IsURL(src.Schema):
		data, err = runAtlas(ctx, src)
	default:
		data, err = readFile(ctx, src)
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty schema input")
	}
	return data, nil
}

// readAll runs read in a goroutine so that cancellation can interrupt a read
// blocked on a terminal, a pipe or a named pipe. If read does not return, its
// goroutine stays blocked and the CLI exits after reporting the cancellation.
func readAll(ctx context.Context, what string, read func() ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := read()
		done <- result{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: %w", what, ctx.Err())
	case res := <-done:
		if res.err != nil {
			return nil, fmt.Errorf("%s: %w", what, res.err)
		}
		return res.data, nil
	}
}

func readStdin(ctx context.Context, r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("reading schema from stdin: no input stream")
	}
	return readAll(ctx, "reading schema from stdin", func() ([]byte, error) {
		return io.ReadAll(r)
	})
}

func readFile(ctx context.Context, src Source) ([]byte, error) {
	path := src.Schema
	if src.Dir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(src.Dir, path)
	}
	return readAll(ctx, fmt.Sprintf("reading schema file %q", path), func() ([]byte, error) {
		return os.ReadFile(path)
	})
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
	var stdout bytes.Buffer
	stderr := &cappedBuffer{max: maxStderr}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = src.Dir
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	// A killed atlas may leave grandchildren holding the pipes; do not wait
	// for them forever.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("atlas schema inspect canceled: %w", ctxErr)
		}
		msg := redactStderr(strings.TrimSpace(stderr.buf.String()), src.Schema, src.DevURL)
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

// cappedBuffer keeps the first max bytes written and discards the rest. It
// bounds the memory a chatty atlas can use; the error prints at most maxStderr
// bytes anyway.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		w.buf.Write(p)
	}
	return n, nil
}

// redactStderr masks the password of every database URL passed to atlas.
// Atlas may echo those URLs in connection errors, including URLs that net/url
// rejects, so the credentials are split out by hand.
func redactStderr(msg string, urls ...string) string {
	for _, raw := range urls {
		userinfo, masked, ok := credentialUserinfo(raw)
		if !ok {
			continue
		}
		msg = strings.ReplaceAll(msg, userinfo, masked)
		// The stderr cap may cut the password off before its closing '@'.
		// Mask the rest of that userinfo span too.
		username, _, _ := strings.Cut(userinfo, ":")
		marker := "://" + username + ":"
		i := strings.Index(msg, marker)
		if i < 0 {
			continue
		}
		start := i + len(marker)
		end := strings.IndexByte(msg[start:], '@')
		if end < 0 {
			end = strings.IndexAny(msg[start:], " \t\r\n")
		}
		if end < 0 {
			end = len(msg) - start
		}
		msg = msg[:start] + "xxxxx" + msg[start+end:]
	}
	return msg
}

// credentialUserinfo returns the "user:password" part of a URL and the same
// part with the password masked. It works without net/url so that a URL Atlas
// rejects is still redacted if Atlas echoes it.
func credentialUserinfo(raw string) (userinfo, masked string, ok bool) {
	i := strings.Index(raw, "://")
	if i < 0 {
		return "", "", false
	}
	authority := raw[i+3:]
	if j := strings.IndexAny(authority, "/?#"); j >= 0 {
		authority = authority[:j]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return "", "", false
	}
	userinfo = authority[:at]
	colon := strings.IndexByte(userinfo, ':')
	if colon < 0 || colon == len(userinfo)-1 {
		return "", "", false
	}
	return userinfo, userinfo[:colon] + ":xxxxx", true
}
