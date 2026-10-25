package atla_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	atla "github.com/hirokazumiyaji/atla-go"
	"github.com/hirokazumiyaji/atla-go/dialect"
	"github.com/hirokazumiyaji/atla-go/internal/fakedb"
)

const (
	insertUser   = `INSERT INTO "users" ("name", "age", "bio") VALUES ($1, $2, $3)`
	returningAll = ` RETURNING "id", "name", "age", "bio"`
)

// aliceInsert returns the column list and values of an insert of alice with
// bio "hi".
func aliceInsert() ([]string, []any) {
	cols, vals := []string{`"name"`, `"age"`}, []any{"alice", int32(30)}
	return atla.SomePtr("hi").AppendTo(cols, vals, `"bio"`)
}

func ptr[T any](v T) *T { return &v }

func TestInsertSQL(t *testing.T) {
	t.Run("Columns", func(t *testing.T) {
		cols, vals := aliceInsert()
		checkSQL(t, atla.NewInsert(nil, userTable, cols, vals), insertUser, "alice", int32(30), ptr("hi"))
	})
	t.Run("DefaultValues", func(t *testing.T) {
		checkSQL(t, atla.NewInsert[User](nil, userTable, nil, nil), `INSERT INTO "users" DEFAULT VALUES`)
	})
	t.Run("ExprInline", func(t *testing.T) {
		cols, vals := atla.SetExpr(atla.Raw[int32]("1 + ?", 1)).AppendTo(nil, nil, `"age"`)
		checkSQL(t, atla.NewInsert(nil, userTable, cols, vals), `INSERT INTO "users" ("age") VALUES (1 + $1)`, 1)
	})
	t.Run("ExprBetweenParams", func(t *testing.T) {
		cols, vals := atla.Some("alice").AppendTo(nil, nil, `"name"`)
		cols, vals = atla.SetExpr(atla.Raw[int32]("1 + ?", 1)).AppendTo(cols, vals, `"age"`)
		cols, vals = atla.Null[string]().AppendTo(cols, vals, `"bio"`)
		checkSQL(t, atla.NewInsert(nil, userTable, cols, vals),
			`INSERT INTO "users" ("name", "age", "bio") VALUES ($1, 1 + $2, $3)`, "alice", 1, (*string)(nil))
	})
	t.Run("UnsetOmitted", func(t *testing.T) {
		cols, vals := []string{`"name"`}, []any{"alice"}
		var age atla.Opt[int32]
		cols, vals = age.AppendTo(cols, vals, `"age"`)
		checkSQL(t, atla.NewInsert(nil, userTable, cols, vals), `INSERT INTO "users" ("name") VALUES ($1)`, "alice")
	})
	t.Run("SQLite", func(t *testing.T) {
		cols, vals := aliceInsert()
		checkSQL(t, atla.NewInsert(nil, sqliteUserTable(), cols, vals),
			`INSERT INTO "users" ("name", "age", "bio") VALUES (?, ?, ?)`, "alice", int32(30), ptr("hi"))
	})
	t.Run("CopiesInputs", func(t *testing.T) {
		cols, vals := make([]string, 1, 4), make([]any, 1, 4)
		cols[0], vals[0] = `"name"`, "alice"
		q := atla.NewInsert(nil, userTable, cols, vals)
		cols[0], vals[0] = `"bio"`, "mutated"
		_, _ = append(cols, `"age"`), append(vals, 1)
		checkSQL(t, q, `INSERT INTO "users" ("name") VALUES ($1)`, "alice")
	})
	t.Run("LengthMismatch", func(t *testing.T) {
		_, _, err := atla.NewInsert(nil, userTable, []string{`"name"`, `"age"`}, []any{"alice"}).ToSQL()
		if err == nil || !strings.Contains(err.Error(), "2 columns, 1 values") {
			t.Fatalf("ToSQL error = %v; want a column/value count mismatch", err)
		}
	})
	t.Run("ExprError", func(t *testing.T) {
		cols, vals := atla.SetExpr(atla.Raw[int32]("?")).AppendTo(nil, nil, `"age"`)
		_, _, err := atla.NewInsert(nil, userTable, cols, vals).ToSQL()
		if err == nil || err.Error() != "atla: Raw: 1 placeholders, 0 args" {
			t.Fatalf("ToSQL error = %v; want the Raw placeholder error", err)
		}
	})
}

// noReturning is a Postgres dialect that reports no RETURNING support.
type noReturning struct{ dialect.Dialect }

func (noReturning) Returning() bool { return false }

func TestInsertExecAndSave(t *testing.T) {
	ctx := context.Background()

	t.Run("Exec", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(insertUser, "alice", int32(30), "hi").Result(1)
		cols, vals := aliceInsert()
		n, err := atla.NewInsert(db, userTable, cols, vals).Exec(ctx)
		if n != 1 || err != nil {
			t.Fatalf("Exec() = %d, %v; want 1, nil", n, err)
		}
		s.Done(t)
	})
	t.Run("ExecNull", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`INSERT INTO "users" ("bio") VALUES ($1)`, nil).Result(1)
		cols, vals := atla.Null[string]().AppendTo(nil, nil, `"bio"`)
		if _, err := atla.NewInsert(db, userTable, cols, vals).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		s.Done(t)
	})
	t.Run("Save", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(insertUser+returningAll, "alice", int32(30), "hi").
			Rows(userColNames, userRow(7, "alice", 30, "hi"))
		cols, vals := aliceInsert()
		u, err := atla.NewInsert(db, userTable, cols, vals).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if u.ID != 7 || u.Name != "alice" || u.Age != 30 || u.Bio == nil || *u.Bio != "hi" {
			t.Errorf("Save() = %+v", u)
		}
		checkClosed(t, s)
	})
	t.Run("SaveDefaultValues", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`INSERT INTO "users" DEFAULT VALUES`+returningAll).
			Rows(userColNames, userRow(8, "", 0, nil))
		u, err := atla.NewInsert[User](db, userTable, nil, nil).Save(ctx)
		if err != nil || u.ID != 8 {
			t.Fatalf("Save() = %+v, %v; want ID 8", u, err)
		}
		checkClosed(t, s)
	})
	t.Run("SaveNoRow", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(`INSERT INTO "users" DEFAULT VALUES` + returningAll).Rows(userColNames)
		u, err := atla.NewInsert[User](db, userTable, nil, nil).Save(ctx)
		if u != nil || !errors.Is(err, atla.ErrNotFound) {
			t.Fatalf("Save() = %+v, %v; want nil, ErrNotFound", u, err)
		}
		checkClosed(t, s)
	})
	t.Run("SaveReturningUnsupported", func(t *testing.T) {
		db, s := fakedb.Open(t)
		tbl := userTable
		tbl.Dialect = noReturning{dialect.Postgres}
		u, err := atla.NewInsert[User](db, tbl, nil, nil).Save(ctx)
		if u != nil || !errors.Is(err, atla.ErrReturningUnsupported) {
			t.Fatalf("Save() = %+v, %v; want nil, ErrReturningUnsupported", u, err)
		}
		if log := s.Log(); len(log) != 0 {
			t.Fatalf("log = %q, want no statement sent", log)
		}
	})
	t.Run("LengthMismatchSendsNothing", func(t *testing.T) {
		db, s := fakedb.Open(t)
		q := atla.NewInsert(db, userTable, []string{`"name"`}, nil)
		if _, err := q.Exec(ctx); err == nil {
			t.Error("Exec: want error")
		}
		if _, err := q.Save(ctx); err == nil {
			t.Error("Save: want error")
		}
		if log := s.Log(); len(log) != 0 {
			t.Fatalf("log = %q, want no statement sent", log)
		}
	})
	t.Run("QueryInfo", func(t *testing.T) {
		db, s := fakedb.Open(t)
		rec := &infoRecorder{DB: db}
		s.ExpectExec(`INSERT INTO "users" DEFAULT VALUES`).Result(1)
		s.ExpectQuery(`INSERT INTO "users" DEFAULT VALUES`+returningAll).
			Rows(userColNames, userRow(1, "", 0, nil))
		q := atla.NewInsert[User](rec, userTable, nil, nil)
		if _, err := q.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
		want := atla.QueryInfo{Op: "insert", Table: "users", ReadOnly: false}
		if !slices.Equal(rec.infos, []atla.QueryInfo{want, want}) {
			t.Fatalf("QueryInfo = %+v; want %+v twice", rec.infos, want)
		}
		checkClosed(t, s)
	})
	t.Run("ErrorClassified", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`INSERT INTO "users" DEFAULT VALUES`).Err(pgUniqueErr{})
		s.ExpectQuery(`INSERT INTO "users" DEFAULT VALUES` + returningAll).Err(pgUniqueErr{})
		q := atla.NewInsert[User](db, userTable, nil, nil)
		_, execErr := q.Exec(ctx)
		_, saveErr := q.Save(ctx)
		for name, err := range map[string]error{"Exec": execErr, "Save": saveErr} {
			var dbErr *atla.DBError
			if !errors.Is(err, atla.ErrUniqueViolation) || !errors.As(err, &dbErr) {
				t.Fatalf("%s error = %v; want a DBError with ErrUniqueViolation", name, err)
			}
			if dbErr.Op != "insert" || dbErr.Table != "users" {
				t.Errorf("%s DBError = %+v; want Op insert, Table users", name, dbErr)
			}
		}
		checkClosed(t, s)
	})
}
