package atla_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	atla "github.com/hirokazumiyaji/atla-go"
	"github.com/hirokazumiyaji/atla-go/internal/fakedb"
)

const selectUsers = `SELECT ` + userCols + ` FROM "users"`

var userColNames = []string{"id", "name", "age", "bio"}

// userRow returns a users result row; a nil bio is SQL NULL.
func userRow(id int64, name string, age int64, bio any) []driver.Value {
	return []driver.Value{id, name, age, bio}
}

// pgUniqueErr is a Postgres-shaped driver error with SQLSTATE 23505.
type pgUniqueErr struct{}

func (pgUniqueErr) Error() string    { return "duplicate key value violates unique constraint" }
func (pgUniqueErr) SQLState() string { return "23505" }

// checkClosed asserts that every result set was closed and that the script
// was fully consumed.
func checkClosed(t *testing.T, s *fakedb.Script) {
	t.Helper()
	if n := s.OpenRows(); n != 0 {
		t.Errorf("OpenRows() = %d, want 0", n)
	}
	s.Done(t)
}

func TestAllScansRows(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectQuery(selectUsers+` WHERE "users"."age" >= $1`, 20).Rows(userColNames,
		userRow(1, "alice", 30, "hi"),
		userRow(2, "bob", 40, nil),
	)

	users, err := atla.NewSelect(db, userTable).Where(uAge.GTE(20)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("len(users) = %d, want 2", len(users))
	}
	if u := users[0]; u.ID != 1 || u.Name != "alice" || u.Age != int32(30) || u.Bio == nil || *u.Bio != "hi" {
		t.Errorf("users[0] = %+v", u)
	}
	if u := users[1]; u.ID != 2 || u.Name != "bob" || u.Age != int32(40) || u.Bio != nil {
		t.Errorf("users[1] = %+v", u)
	}
	checkClosed(t, s)
}

func TestAllNoRows(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectQuery(selectUsers).Rows(userColNames)

	users, err := atla.NewSelect(db, userTable).All(ctx)
	if err != nil || len(users) != 0 {
		t.Fatalf("All() = %v, %v; want no rows, nil", users, err)
	}
	checkClosed(t, s)
}

func TestFirstAndOne(t *testing.T) {
	ctx := context.Background()
	// Any earlier limit is replaced; the offset and order are kept.
	base := func(db *sql.DB) atla.Select[User] {
		return atla.NewSelect(db, userTable).Where(uAge.GTE(20)).OrderBy(uID.Asc()).Limit(10).Offset(5)
	}
	prefix := selectUsers + ` WHERE "users"."age" >= $1 ORDER BY "users"."id" ASC`
	first := prefix + ` LIMIT 1 OFFSET 5`
	one := prefix + ` LIMIT 2 OFFSET 5`

	t.Run("FirstFound", func(t *testing.T) {
		db, s := fakedb.Open(t)
		// Two rows are scripted although LIMIT 1 would return one: First
		// must stop after one row and still close the result set.
		s.ExpectQuery(first, 20).Rows(userColNames, userRow(1, "alice", 30, nil), userRow(2, "bob", 40, nil))
		u, err := base(db).First(ctx)
		if err != nil || u == nil || u.ID != 1 {
			t.Fatalf("First() = %+v, %v; want user 1", u, err)
		}
		checkClosed(t, s)
	})
	t.Run("FirstNotFound", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(first, 20).Rows(userColNames)
		u, err := base(db).First(ctx)
		if u != nil || !errors.Is(err, atla.ErrNotFound) {
			t.Fatalf("First() = %+v, %v; want ErrNotFound", u, err)
		}
		checkClosed(t, s)
	})
	t.Run("OneFound", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(one, 20).Rows(userColNames, userRow(1, "alice", 30, nil))
		u, err := base(db).One(ctx)
		if err != nil || u == nil || u.ID != 1 {
			t.Fatalf("One() = %+v, %v; want user 1", u, err)
		}
		checkClosed(t, s)
	})
	t.Run("OneNotFound", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(one, 20).Rows(userColNames)
		u, err := base(db).One(ctx)
		if u != nil || !errors.Is(err, atla.ErrNotFound) {
			t.Fatalf("One() = %+v, %v; want ErrNotFound", u, err)
		}
		checkClosed(t, s)
	})
	t.Run("OneMultiple", func(t *testing.T) {
		db, s := fakedb.Open(t)
		// A third row is scripted so that One stops before EOF and must
		// close the result set itself.
		s.ExpectQuery(one, 20).Rows(userColNames,
			userRow(1, "alice", 30, nil), userRow(2, "bob", 40, nil), userRow(3, "carol", 50, nil))
		u, err := base(db).One(ctx)
		if u != nil || !errors.Is(err, atla.ErrMultipleResults) {
			t.Fatalf("One() = %+v, %v; want ErrMultipleResults", u, err)
		}
		checkClosed(t, s)
	})
}

func TestCountAndExist(t *testing.T) {
	ctx := context.Background()
	where := ` FROM "users" WHERE "users"."age" >= $1`

	t.Run("Count", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT COUNT(*)`+where, 20).Rows([]string{"count"}, []driver.Value{int64(3)})
		n, err := atla.NewSelect(db, userTable).Where(uAge.GTE(20)).OrderBy(uID.Desc()).Count(ctx)
		if err != nil || n != 3 {
			t.Fatalf("Count() = %d, %v; want 3", n, err)
		}
		checkClosed(t, s)
	})
	t.Run("CountLimit", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT COUNT(*) FROM (SELECT 1`+where+` LIMIT 10) AS "atla_count"`, 20).
			Rows([]string{"count"}, []driver.Value{int64(7)})
		n, err := atla.NewSelect(db, userTable).Where(uAge.GTE(20)).OrderBy(uID.Desc()).Limit(10).Count(ctx)
		if err != nil || n != 7 {
			t.Fatalf("Count() = %d, %v; want 7", n, err)
		}
		checkClosed(t, s)
	})
	t.Run("CountOffset", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT COUNT(*) FROM (SELECT 1`+where+` OFFSET 5) AS "atla_count"`, 20).
			Rows([]string{"count"}, []driver.Value{int64(2)})
		n, err := atla.NewSelect(db, userTable).Where(uAge.GTE(20)).Offset(5).Count(ctx)
		if err != nil || n != 2 {
			t.Fatalf("Count() = %d, %v; want 2", n, err)
		}
		checkClosed(t, s)
	})
	t.Run("CountLockWraps", func(t *testing.T) {
		// Postgres rejects FOR UPDATE next to an aggregate, so a locked
		// count locks the rows in a subquery.
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT COUNT(*) FROM (SELECT 1`+where+` FOR UPDATE) AS "atla_count"`, 20).
			Rows([]string{"count"}, []driver.Value{int64(1)})
		n, err := atla.NewSelect(db, userTable).Where(uAge.GTE(20)).ForUpdate().Count(ctx)
		if err != nil || n != 1 {
			t.Fatalf("Count() = %d, %v; want 1", n, err)
		}
		checkClosed(t, s)
	})
	t.Run("Exist", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT EXISTS (SELECT 1`+where+`)`, 20).Rows([]string{"exists"}, []driver.Value{true})
		ok, err := atla.NewSelect(db, userTable).Where(uAge.GTE(20)).OrderBy(uID.Desc()).Exist(ctx)
		if err != nil || !ok {
			t.Fatalf("Exist() = %v, %v; want true", ok, err)
		}
		checkClosed(t, s)
	})
	t.Run("ExistFalse", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT EXISTS (SELECT 1 FROM "users")`).Rows([]string{"exists"}, []driver.Value{false})
		ok, err := atla.NewSelect(db, userTable).Exist(ctx)
		if err != nil || ok {
			t.Fatalf("Exist() = %v, %v; want false", ok, err)
		}
		checkClosed(t, s)
	})
	t.Run("ExistSQLite", func(t *testing.T) {
		// SQLite returns EXISTS as an integer.
		db, s := fakedb.Open(t)
		s.ExpectQuery(`SELECT EXISTS (SELECT 1 FROM "users" WHERE "users"."age" >= ? LIMIT 3)`, 20).
			Rows([]string{"exists"}, []driver.Value{int64(1)})
		ok, err := atla.NewSelect(db, sqliteUserTable()).Where(uAge.GTE(20)).Limit(3).Exist(ctx)
		if err != nil || !ok {
			t.Fatalf("Exist() = %v, %v; want true", ok, err)
		}
		checkClosed(t, s)
	})
}

func TestIterBreakClosesRows(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectQuery(selectUsers).Rows(userColNames,
		userRow(1, "alice", 30, nil), userRow(2, "bob", 40, nil), userRow(3, "carol", 50, nil))

	var got []int64
	for u, err := range atla.NewSelect(db, userTable).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, u.ID)
		break
	}
	if !slices.Equal(got, []int64{1}) {
		t.Fatalf("iterated %v, want [1]", got)
	}
	if !slices.Contains(s.Log(), "ROWS CLOSED") {
		t.Errorf("log %q has no ROWS CLOSED", s.Log())
	}
	checkClosed(t, s)
}

func TestIterAllRows(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectQuery(selectUsers).Rows(userColNames, userRow(1, "alice", 30, nil), userRow(2, "bob", 40, nil))

	var got []int64
	for u, err := range atla.NewSelect(db, userTable).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, u.ID)
	}
	if !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("iterated %v, want [1 2]", got)
	}
	checkClosed(t, s)
}

// collectIter drains seq and returns the IDs and errors it yielded.
func collectIter(seq func(func(*User, error) bool)) (ids []int64, errs []error) {
	for u, err := range seq {
		if err != nil {
			if u != nil {
				panic("Iter yielded a row together with an error")
			}
			errs = append(errs, err)
			continue
		}
		ids = append(ids, u.ID)
	}
	return ids, errs
}

func TestIterYieldsErrorOnce(t *testing.T) {
	ctx := context.Background()

	t.Run("QueryError", func(t *testing.T) {
		db, s := fakedb.Open(t)
		boom := errors.New("boom")
		s.ExpectQuery(selectUsers).Err(boom)
		ids, errs := collectIter(atla.NewSelect(db, userTable).Iter(ctx))
		if len(ids) != 0 || len(errs) != 1 || !errors.Is(errs[0], boom) {
			t.Fatalf("Iter yielded ids %v, errs %v; want exactly one boom", ids, errs)
		}
		checkClosed(t, s)
	})
	t.Run("ScanErrorMidway", func(t *testing.T) {
		db, s := fakedb.Open(t)
		// The second row's id is not an integer; the third row is never
		// read, so the iterator must close the result set itself.
		s.ExpectQuery(selectUsers).Rows(userColNames,
			userRow(1, "alice", 30, nil),
			[]driver.Value{"not-an-id", "bob", int64(40), nil},
			userRow(3, "carol", 50, nil))
		ids, errs := collectIter(atla.NewSelect(db, userTable).Iter(ctx))
		if !slices.Equal(ids, []int64{1}) || len(errs) != 1 {
			t.Fatalf("Iter yielded ids %v, errs %v; want [1] and one error", ids, errs)
		}
		checkClosed(t, s)
	})
	t.Run("CompileError", func(t *testing.T) {
		db, s := fakedb.Open(t)
		q := atla.NewSelect(db, userTable).Where(atla.Exists(atla.AnySubquery{}))
		ids, errs := collectIter(q.Iter(ctx))
		if len(ids) != 0 || len(errs) != 1 || !strings.HasPrefix(errs[0].Error(), "atla: ") {
			t.Fatalf("Iter yielded ids %v, errs %v; want one atla: error", ids, errs)
		}
		if log := s.Log(); len(log) != 0 {
			t.Fatalf("log = %q, want no statement sent", log)
		}
	})
}

func TestAllScanErrorClosesRows(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	s.ExpectQuery(selectUsers).Rows(userColNames,
		userRow(1, "alice", 30, nil),
		[]driver.Value{"not-an-id", "bob", int64(40), nil},
		userRow(3, "carol", 50, nil))

	users, err := atla.NewSelect(db, userTable).All(ctx)
	if err == nil || users != nil {
		t.Fatalf("All() = %v, %v; want nil and a scan error", users, err)
	}
	checkClosed(t, s)
}

func TestCompileErrorSendsNothing(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	q := atla.NewSelect(db, userTable).Where(atla.Raw[bool]("a = ?"))

	check := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), "atla: Raw: ") {
			t.Errorf("%s error = %v, want the Raw compile error", name, err)
		}
	}
	_, err := q.All(ctx)
	check("All", err)
	_, err = q.First(ctx)
	check("First", err)
	_, err = q.One(ctx)
	check("One", err)
	_, err = q.Count(ctx)
	check("Count", err)
	_, err = q.Limit(1).Count(ctx)
	check("Count(wrapped)", err)
	_, err = q.Exist(ctx)
	check("Exist", err)
	if log := s.Log(); len(log) != 0 {
		t.Fatalf("log = %q, want no statement sent", log)
	}
}

// infoRecorder is an Executor wrapper that records the QueryInfo of every
// QueryContext and ExecContext call.
type infoRecorder struct {
	*sql.DB
	infos []atla.QueryInfo
}

func (r *infoRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	qi, ok := atla.QueryInfoFrom(ctx)
	if !ok {
		qi = atla.QueryInfo{Op: "<missing>"}
	}
	r.infos = append(r.infos, qi)
	return r.DB.QueryContext(ctx, query, args...)
}

func (r *infoRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	qi, ok := atla.QueryInfoFrom(ctx)
	if !ok {
		qi = atla.QueryInfo{Op: "<missing>"}
	}
	r.infos = append(r.infos, qi)
	return r.DB.ExecContext(ctx, query, args...)
}

func TestQueryInfoAttached(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	rec := &infoRecorder{DB: db}
	row := userRow(1, "alice", 30, nil)
	count := []driver.Value{int64(1)}
	exists := []driver.Value{true}

	s.ExpectQuery(selectUsers).Rows(userColNames, row)
	s.ExpectQuery(selectUsers+` LIMIT 1`).Rows(userColNames, row)
	s.ExpectQuery(selectUsers+` LIMIT 2`).Rows(userColNames, row)
	s.ExpectQuery(`SELECT COUNT(*) FROM "users"`).Rows([]string{"count"}, count)
	s.ExpectQuery(`SELECT EXISTS (SELECT 1 FROM "users")`).Rows([]string{"exists"}, exists)
	s.ExpectQuery(selectUsers).Rows(userColNames, row)
	s.ExpectQuery(selectUsers+` FOR UPDATE`).Rows(userColNames, row)
	s.ExpectQuery(`SELECT COUNT(*) FROM (SELECT 1 FROM "users" FOR SHARE) AS "atla_count"`).Rows([]string{"count"}, count)
	s.ExpectQuery(`SELECT EXISTS (SELECT 1 FROM "users" FOR UPDATE)`).Rows([]string{"exists"}, exists)
	s.ExpectQuery(selectUsers+` FOR UPDATE`).Rows(userColNames, row)

	q := atla.NewSelect[User](rec, userTable)
	locked := q.ForUpdate()
	runs := []func() error{
		func() error { _, err := q.All(ctx); return err },
		func() error { _, err := q.First(ctx); return err },
		func() error { _, err := q.One(ctx); return err },
		func() error { _, err := q.Count(ctx); return err },
		func() error { _, err := q.Exist(ctx); return err },
		func() error { _, errs := collectIter(q.Iter(ctx)); return errors.Join(errs...) },
		func() error { _, err := locked.All(ctx); return err },
		func() error { _, err := q.ForShare().Count(ctx); return err },
		func() error { _, err := locked.Exist(ctx); return err },
		func() error { _, errs := collectIter(locked.Iter(ctx)); return errors.Join(errs...) },
	}
	for i, run := range runs {
		if err := run(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	readOnly := atla.QueryInfo{Op: "select", Table: "users", ReadOnly: true}
	locking := atla.QueryInfo{Op: "select", Table: "users", ReadOnly: false}
	want := []atla.QueryInfo{readOnly, readOnly, readOnly, readOnly, readOnly, readOnly, locking, locking, locking, locking}
	if !slices.Equal(rec.infos, want) {
		t.Fatalf("QueryInfo:\n got  %+v\n want %+v", rec.infos, want)
	}
	checkClosed(t, s)
}

func TestSelectErrorClassified(t *testing.T) {
	ctx := context.Background()
	q := func(db *sql.DB) atla.Select[User] { return atla.NewSelect(db, userTable) }
	tests := []struct {
		name string
		sql  string
		run  func(*sql.DB) error
	}{
		{"All", selectUsers, func(db *sql.DB) error { _, err := q(db).All(ctx); return err }},
		{"First", selectUsers + ` LIMIT 1`, func(db *sql.DB) error { _, err := q(db).First(ctx); return err }},
		{"One", selectUsers + ` LIMIT 2`, func(db *sql.DB) error { _, err := q(db).One(ctx); return err }},
		{"Count", `SELECT COUNT(*) FROM "users"`, func(db *sql.DB) error { _, err := q(db).Count(ctx); return err }},
		{"Exist", `SELECT EXISTS (SELECT 1 FROM "users")`, func(db *sql.DB) error { _, err := q(db).Exist(ctx); return err }},
		{"Iter", selectUsers, func(db *sql.DB) error { _, errs := collectIter(q(db).Iter(ctx)); return errors.Join(errs...) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, s := fakedb.Open(t)
			s.ExpectQuery(tt.sql).Err(pgUniqueErr{})
			err := tt.run(db)
			if !errors.Is(err, atla.ErrUniqueViolation) {
				t.Fatalf("err = %v, want ErrUniqueViolation", err)
			}
			var dbErr *atla.DBError
			if !errors.As(err, &dbErr) || dbErr.Op != "select" || dbErr.Table != "users" {
				t.Fatalf("err = %#v, want *DBError{Op: select, Table: users}", err)
			}
			var drv pgUniqueErr
			if !errors.As(err, &drv) {
				t.Fatalf("errors.As(driver error) = false for %v", err)
			}
			checkClosed(t, s)
		})
	}
}
