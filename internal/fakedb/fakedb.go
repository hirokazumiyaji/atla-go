// Package fakedb is a scripted database/sql driver for tests of the atla
// runtime. Tests declare the statements they expect, in order, with canned
// responses, and afterwards inspect the log of what the runtime sent.
//
// It must not import the root atla package, so that atla's internal tests can
// use it.
package fakedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Script holds the expected statements and the log of everything executed.
// It is safe for concurrent use by the connections of one *sql.DB.
type Script struct {
	mu       sync.Mutex
	expected []*Response
	log      []string
	openRows int
}

// Response is the canned outcome of one expected statement.
type Response struct {
	query bool
	sql   string
	args  []driver.Value

	cols         []string
	rows         [][]driver.Value
	rowsAffected int64
	err          error
}

// Open returns a *sql.DB backed by a fresh Script. The DB is closed when the
// test ends.
func Open(t testing.TB) (*sql.DB, *Script) {
	t.Helper()
	s := &Script{}
	db := sql.OpenDB(connector{s: s})
	t.Cleanup(func() { _ = db.Close() })
	return db, s
}

// ExpectQuery appends an expected QueryContext call with the given SQL and
// arguments. Arguments are written as Go values; they are converted the same
// way database/sql converts the actual arguments before comparison.
func (s *Script) ExpectQuery(query string, args ...any) *Response {
	return s.expect(true, query, args)
}

// ExpectExec appends an expected ExecContext call with the given SQL and
// arguments.
func (s *Script) ExpectExec(query string, args ...any) *Response {
	return s.expect(false, query, args)
}

func (s *Script) expect(query bool, sqlText string, args []any) *Response {
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		v, err := driver.DefaultParameterConverter.ConvertValue(a)
		if err != nil {
			v = a
		}
		vals[i] = v
	}
	r := &Response{query: query, sql: sqlText, args: vals}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expected = append(s.expected, r)
	return r
}

// Rows sets the result set returned by an expected query.
func (r *Response) Rows(cols []string, rows ...[]driver.Value) {
	r.cols = cols
	r.rows = rows
}

// Result sets the rows-affected count returned by an expected exec.
func (r *Response) Result(rowsAffected int64) { r.rowsAffected = rowsAffected }

// Err makes the expected statement fail with err.
func (r *Response) Err(err error) { r.err = err }

func (r *Response) String() string {
	kind := "EXEC"
	if r.query {
		kind = "QUERY"
	}
	return fmt.Sprintf("%s %s %v", kind, r.sql, r.args)
}

// Log returns the statements and transaction events seen so far.
func (s *Script) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

// OpenRows returns the number of result sets not yet closed.
func (s *Script) OpenRows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openRows
}

// Done reports every expectation that was not consumed.
func (s *Script) Done(t testing.TB) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.expected {
		t.Errorf("fakedb: unmet expectation: %v", r)
	}
}

func (s *Script) logf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, fmt.Sprintf(format, args...))
}

// next logs the statement and pops the head expectation if it matches.
func (s *Script) next(query bool, sqlText string, nv []driver.NamedValue) (*Response, error) {
	kind := "EXEC"
	if query {
		kind = "QUERY"
	}
	args := make([]driver.Value, len(nv))
	for i, v := range nv {
		args[i] = v.Value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, kind+" "+sqlText)
	if !query && isSavepoint(sqlText) {
		return &Response{}, nil
	}
	if len(s.expected) == 0 {
		return nil, fmt.Errorf("fakedb: unexpected %s %s %v: no expectations left", kind, sqlText, args)
	}
	head := s.expected[0]
	if head.query != query || head.sql != sqlText || !reflect.DeepEqual(head.args, args) {
		return nil, fmt.Errorf("fakedb: unexpected %s %s %v: want %v", kind, sqlText, args, head)
	}
	s.expected = s.expected[1:]
	return head, nil
}

func isSavepoint(sqlText string) bool {
	return strings.HasPrefix(sqlText, "SAVEPOINT") ||
		strings.HasPrefix(sqlText, "RELEASE SAVEPOINT") ||
		strings.HasPrefix(sqlText, "ROLLBACK TO SAVEPOINT")
}

type connector struct{ s *Script }

func (c connector) Connect(context.Context) (driver.Conn, error) { return &conn{s: c.s}, nil }
func (c connector) Driver() driver.Driver                        { return fakeDriver(c) }

type fakeDriver struct{ s *Script }

func (d fakeDriver) Open(string) (driver.Conn, error) { return &conn{s: d.s}, nil }

// conn deliberately does not implement driver.NamedValueChecker, so
// database/sql applies its default argument conversion.
type conn struct{ s *Script }

var (
	_ driver.QueryerContext = (*conn)(nil)
	_ driver.ExecerContext  = (*conn)(nil)
	_ driver.ConnBeginTx    = (*conn)(nil)
)

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return nil, fmt.Errorf("fakedb: Prepare is not supported: %s", query)
}

func (c *conn) Close() error { return nil }

func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *conn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if opts.Isolation == driver.IsolationLevel(sql.LevelDefault) && !opts.ReadOnly {
		c.s.logf("BEGIN")
	} else {
		c.s.logf("BEGIN iso=%d ro=%t", int(opts.Isolation), opts.ReadOnly)
	}
	return tx{s: c.s}, nil
}

func (c *conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r, err := c.s.next(true, query, args)
	if err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	c.s.mu.Lock()
	c.s.openRows++
	c.s.mu.Unlock()
	return &rows{s: c.s, cols: r.cols, data: r.rows}, nil
}

func (c *conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	r, err := c.s.next(false, query, args)
	if err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	return result(r.rowsAffected), nil
}

type tx struct{ s *Script }

func (t tx) Commit() error {
	t.s.logf("COMMIT")
	return nil
}

func (t tx) Rollback() error {
	t.s.logf("ROLLBACK")
	return nil
}

type result int64

func (r result) LastInsertId() (int64, error) {
	return 0, errors.New("fakedb: LastInsertId is not supported")
}

func (r result) RowsAffected() (int64, error) { return int64(r), nil }

type rows struct {
	s      *Script
	cols   []string
	data   [][]driver.Value
	closed bool
}

func (r *rows) Columns() []string { return r.cols }

func (r *rows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.openRows--
	r.s.log = append(r.s.log, "ROWS CLOSED")
	return nil
}

func (r *rows) Next(dest []driver.Value) error {
	if len(r.data) == 0 {
		return io.EOF
	}
	row := r.data[0]
	r.data = r.data[1:]
	if len(row) != len(dest) {
		return fmt.Errorf("fakedb: row has %d values, want %d", len(row), len(dest))
	}
	copy(dest, row)
	return nil
}
