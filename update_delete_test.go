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

const updateUser = `UPDATE "users" SET "name" = $1, "age" = ("users"."age" + $2) WHERE "users"."id" = $3`

// bobUpdate returns the assignments name = 'bob', age = age + 1.
func bobUpdate() ([]string, []any) {
	cols, vals := atla.Some("bob").AppendTo(nil, nil, `"name"`)
	return atla.SetExpr(uAge.Add(1)).AppendTo(cols, vals, `"age"`)
}

// nameUpdate returns the single assignment name = 'bob'.
func nameUpdate() ([]string, []any) {
	return atla.Some("bob").AppendTo(nil, nil, `"name"`)
}

// checkNothingSent asserts that no statement reached the database.
func checkNothingSent(t *testing.T, s *fakedb.Script) {
	t.Helper()
	if log := s.Log(); len(log) != 0 {
		t.Fatalf("log = %q, want no statement sent", log)
	}
}

// checkNilPredicate asserts that run fails, without panicking, with either
// the nil-predicate compile error or the unsafe-mutation guard error.
func checkNilPredicate(t *testing.T, name string, run func() error) {
	t.Helper()
	err := run()
	ok := err != nil && (strings.Contains(err.Error(), "atla: nil predicate") || errors.Is(err, atla.ErrUnsafeMutation))
	if !ok {
		t.Errorf("%s error = %v; want atla: nil predicate or ErrUnsafeMutation", name, err)
	}
}

func TestUpdateSQL(t *testing.T) {
	ctx := context.Background()

	t.Run("Where", func(t *testing.T) {
		cols, vals := bobUpdate()
		checkSQL(t, atla.NewUpdate(nil, userTable, cols, vals).Where(uID.EQ(7)), updateUser, "bob", int32(1), int64(7))
	})
	t.Run("WhereAccumulates", func(t *testing.T) {
		cols, vals := nameUpdate()
		q := atla.NewUpdate(nil, userTable, cols, vals).Where(uID.EQ(7)).Where(uAge.GT(20))
		checkSQL(t, q, `UPDATE "users" SET "name" = $1 WHERE ("users"."id" = $2 AND "users"."age" > $3)`,
			"bob", int64(7), int32(20))
	})
	t.Run("SQLite", func(t *testing.T) {
		cols, vals := bobUpdate()
		checkSQL(t, atla.NewUpdate(nil, sqliteUserTable(), cols, vals).Where(uID.EQ(7)),
			`UPDATE "users" SET "name" = ?, "age" = ("users"."age" + ?) WHERE "users"."id" = ?`, "bob", int32(1), int64(7))
	})
	t.Run("WhereClones", func(t *testing.T) {
		cols, vals := nameUpdate()
		base := atla.NewUpdate(nil, userTable, cols, vals).Where(uID.EQ(1), uID.EQ(2), uID.EQ(3)).Where(uID.EQ(4))
		a := base.Where(uAge.EQ(10))
		b := base.Where(uAge.EQ(20))
		checkSQL(t, a, `UPDATE "users" SET "name" = $1 WHERE ("users"."id" = $2 AND "users"."id" = $3 AND "users"."id" = $4 AND "users"."id" = $5 AND "users"."age" = $6)`,
			"bob", int64(1), int64(2), int64(3), int64(4), int32(10))
		checkSQL(t, b, `UPDATE "users" SET "name" = $1 WHERE ("users"."id" = $2 AND "users"."id" = $3 AND "users"."id" = $4 AND "users"."id" = $5 AND "users"."age" = $6)`,
			"bob", int64(1), int64(2), int64(3), int64(4), int32(20))
	})
	t.Run("CopiesInputs", func(t *testing.T) {
		cols, vals := make([]string, 1, 4), make([]any, 1, 4)
		cols[0], vals[0] = `"name"`, "bob"
		q := atla.NewUpdate(nil, userTable, cols, vals).Where(uID.EQ(7))
		cols[0], vals[0] = `"bio"`, "mutated"
		_, _ = append(cols, `"age"`), append(vals, 1)
		checkSQL(t, q, `UPDATE "users" SET "name" = $1 WHERE "users"."id" = $2`, "bob", int64(7))
	})
	t.Run("LengthMismatch", func(t *testing.T) {
		_, _, err := atla.NewUpdate[User](nil, userTable, []string{`"name"`, `"age"`}, []any{"bob"}).Where(uID.EQ(7)).ToSQL()
		if err == nil || !strings.Contains(err.Error(), "2 columns, 1 values") {
			t.Fatalf("ToSQL error = %v; want a column/value count mismatch", err)
		}
	})
	t.Run("Exec", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(updateUser, "bob", int32(1), int64(7)).Result(3)
		cols, vals := bobUpdate()
		n, err := atla.NewUpdate(db, userTable, cols, vals).Where(uID.EQ(7)).Exec(ctx)
		if n != 3 || err != nil {
			t.Fatalf("Exec() = %d, %v; want 3, nil", n, err)
		}
		s.Done(t)
	})
	t.Run("Save", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(updateUser+returningAll, "bob", int32(1), int64(7)).
			Rows(userColNames, userRow(7, "bob", 31, nil), userRow(8, "bob", 41, "hi"))
		cols, vals := bobUpdate()
		us, err := atla.NewUpdate(db, userTable, cols, vals).Where(uID.EQ(7)).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(us) != 2 || us[0].ID != 7 || us[0].Age != 31 || us[1].ID != 8 || us[1].Bio == nil || *us[1].Bio != "hi" {
			t.Errorf("Save() = %+v", us)
		}
		checkClosed(t, s)
	})
	t.Run("SaveNoRows", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectQuery(updateUser+returningAll, "bob", int32(1), int64(7)).Rows(userColNames)
		cols, vals := bobUpdate()
		us, err := atla.NewUpdate(db, userTable, cols, vals).Where(uID.EQ(7)).Save(ctx)
		if len(us) != 0 || err != nil {
			t.Fatalf("Save() = %+v, %v; want no rows, nil", us, err)
		}
		checkClosed(t, s)
	})
	t.Run("SaveReturningUnsupported", func(t *testing.T) {
		db, s := fakedb.Open(t)
		tbl := userTable
		tbl.Dialect = noReturning{dialect.Postgres}
		cols, vals := nameUpdate()
		us, err := atla.NewUpdate(db, tbl, cols, vals).Where(uID.EQ(7)).Save(ctx)
		if us != nil || !errors.Is(err, atla.ErrReturningUnsupported) {
			t.Fatalf("Save() = %+v, %v; want nil, ErrReturningUnsupported", us, err)
		}
		checkNothingSent(t, s)
	})
	t.Run("LengthMismatchSendsNothing", func(t *testing.T) {
		db, s := fakedb.Open(t)
		q := atla.NewUpdate[User](db, userTable, []string{`"name"`, `"age"`}, []any{"bob"}).Where(uID.EQ(7))
		if _, err := q.Exec(ctx); err == nil {
			t.Error("Exec: want error")
		}
		if _, err := q.Save(ctx); err == nil {
			t.Error("Save: want error")
		}
		checkNothingSent(t, s)
	})
	t.Run("ErrorClassified", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`UPDATE "users" SET "name" = $1`, "bob").Err(pgUniqueErr{})
		s.ExpectQuery(`UPDATE "users" SET "name" = $1`+returningAll, "bob").Err(pgUniqueErr{})
		cols, vals := nameUpdate()
		q := atla.NewUpdate(db, userTable, cols, vals).AllowFullTable()
		_, execErr := q.Exec(ctx)
		_, saveErr := q.Save(ctx)
		for name, err := range map[string]error{"Exec": execErr, "Save": saveErr} {
			var dbErr *atla.DBError
			if !errors.Is(err, atla.ErrUniqueViolation) || !errors.As(err, &dbErr) {
				t.Fatalf("%s error = %v; want a DBError with ErrUniqueViolation", name, err)
			}
			if dbErr.Op != "update" || dbErr.Table != "users" {
				t.Errorf("%s DBError = %+v; want Op update, Table users", name, dbErr)
			}
		}
		checkClosed(t, s)
	})
}

func TestUpdateGuards(t *testing.T) {
	ctx := context.Background()

	t.Run("EmptyUpdate", func(t *testing.T) {
		db, s := fakedb.Open(t)
		// Checked before the WHERE guard: neither a WHERE nor AllowFullTable
		// changes the error.
		for name, q := range map[string]atla.Update[User]{
			"NoWhere":        atla.NewUpdate[User](db, userTable, nil, nil),
			"Where":          atla.NewUpdate[User](db, userTable, nil, nil).Where(uID.EQ(7)),
			"AllowFullTable": atla.NewUpdate[User](db, userTable, nil, nil).AllowFullTable(),
		} {
			if _, _, err := q.ToSQL(); !errors.Is(err, atla.ErrEmptyUpdate) {
				t.Errorf("%s: ToSQL error = %v; want ErrEmptyUpdate", name, err)
			}
			if _, err := q.Exec(ctx); !errors.Is(err, atla.ErrEmptyUpdate) {
				t.Errorf("%s: Exec error = %v; want ErrEmptyUpdate", name, err)
			}
			if _, err := q.Save(ctx); !errors.Is(err, atla.ErrEmptyUpdate) {
				t.Errorf("%s: Save error = %v; want ErrEmptyUpdate", name, err)
			}
		}
		checkNothingSent(t, s)
	})
	t.Run("NoWhere", func(t *testing.T) {
		db, s := fakedb.Open(t)
		cols, vals := nameUpdate()
		for name, q := range map[string]atla.Update[User]{
			"NoWhere":        atla.NewUpdate(db, userTable, cols, vals),
			"EmptyCall":      atla.NewUpdate(db, userTable, cols, vals).Where(),
			"EmptyAnd":       atla.NewUpdate(db, userTable, cols, vals).Where(atla.And()),
			"NestedEmptyAnd": atla.NewUpdate(db, userTable, cols, vals).Where(atla.And(atla.And())),
			"OrOfEmptyAnd":   atla.NewUpdate(db, userTable, cols, vals).Where(atla.Or(atla.And())),
			"EmptyAnds":      atla.NewUpdate(db, userTable, cols, vals).Where(atla.And(), atla.And(atla.And(), atla.And())),
		} {
			if _, _, err := q.ToSQL(); !errors.Is(err, atla.ErrUnsafeMutation) {
				t.Errorf("%s: ToSQL error = %v; want ErrUnsafeMutation", name, err)
			}
			if _, err := q.Exec(ctx); !errors.Is(err, atla.ErrUnsafeMutation) {
				t.Errorf("%s: Exec error = %v; want ErrUnsafeMutation", name, err)
			}
			if us, err := q.Save(ctx); us != nil || !errors.Is(err, atla.ErrUnsafeMutation) {
				t.Errorf("%s: Save() = %+v, %v; want nil, ErrUnsafeMutation", name, us, err)
			}
		}
		checkNothingSent(t, s)
	})
	t.Run("AllowFullTable", func(t *testing.T) {
		cols, vals := nameUpdate()
		base := atla.NewUpdate(nil, userTable, cols, vals)
		checkSQL(t, base.AllowFullTable(), `UPDATE "users" SET "name" = $1`, "bob")
		// AllowFullTable returns a copy: base keeps its guard.
		if _, _, err := base.ToSQL(); !errors.Is(err, atla.ErrUnsafeMutation) {
			t.Errorf("base ToSQL error = %v; want ErrUnsafeMutation", err)
		}
	})
	t.Run("AllowFullTableEmptyWhere", func(t *testing.T) {
		cols, vals := nameUpdate()
		base := atla.NewUpdate(nil, userTable, cols, vals).AllowFullTable()
		checkSQL(t, base.Where(atla.And()), `UPDATE "users" SET "name" = $1 WHERE 1 = 1`, "bob")
		checkSQL(t, base.Where(atla.And(atla.And())), `UPDATE "users" SET "name" = $1 WHERE 1 = 1`, "bob")
		checkSQL(t, base.Where(atla.Or(atla.And())), `UPDATE "users" SET "name" = $1 WHERE 1 = 1`, "bob")
	})
	t.Run("NilPredicate", func(t *testing.T) {
		db, s := fakedb.Open(t)
		cols, vals := nameUpdate()
		for name, q := range map[string]atla.Update[User]{
			"WhereNil":      atla.NewUpdate(db, userTable, cols, vals).Where(nil),
			"AndNil":        atla.NewUpdate(db, userTable, cols, vals).Where(atla.And(nil, uID.EQ(7))),
			"WhereNilAllow": atla.NewUpdate(db, userTable, cols, vals).Where(nil).AllowFullTable(),
		} {
			checkNilPredicate(t, name+" ToSQL", func() error { _, _, err := q.ToSQL(); return err })
			checkNilPredicate(t, name+" Exec", func() error { _, err := q.Exec(ctx); return err })
			checkNilPredicate(t, name+" Save", func() error { _, err := q.Save(ctx); return err })
		}
		checkNothingSent(t, s)
	})
	t.Run("AllowFullTableExecAndSave", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`UPDATE "users" SET "name" = $1`, "bob").Result(5)
		s.ExpectQuery(`UPDATE "users" SET "name" = $1`+returningAll, "bob").
			Rows(userColNames, userRow(1, "bob", 30, nil))
		cols, vals := nameUpdate()
		q := atla.NewUpdate(db, userTable, cols, vals).AllowFullTable()
		if n, err := q.Exec(ctx); n != 5 || err != nil {
			t.Fatalf("Exec() = %d, %v; want 5, nil", n, err)
		}
		if us, err := q.Save(ctx); len(us) != 1 || err != nil {
			t.Fatalf("Save() = %+v, %v; want 1 row, nil", us, err)
		}
		checkClosed(t, s)
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("SQL", func(t *testing.T) {
		checkSQL(t, atla.NewDelete[User](nil, userTable).Where(uID.EQ(7)), `DELETE FROM "users" WHERE "users"."id" = $1`, int64(7))
	})
	t.Run("WhereAccumulates", func(t *testing.T) {
		checkSQL(t, atla.NewDelete[User](nil, userTable).Where(uID.EQ(7)).Where(uAge.LT(18)),
			`DELETE FROM "users" WHERE ("users"."id" = $1 AND "users"."age" < $2)`, int64(7), int32(18))
	})
	t.Run("SQLite", func(t *testing.T) {
		checkSQL(t, atla.NewDelete[User](nil, sqliteUserTable()).Where(uID.EQ(7)), `DELETE FROM "users" WHERE "users"."id" = ?`, int64(7))
	})
	t.Run("WhereClones", func(t *testing.T) {
		base := atla.NewDelete[User](nil, userTable).Where(uID.EQ(1), uID.EQ(2), uID.EQ(3)).Where(uID.EQ(4))
		a := base.Where(uAge.EQ(10))
		b := base.Where(uAge.EQ(20))
		const want = `DELETE FROM "users" WHERE ("users"."id" = $1 AND "users"."id" = $2 AND "users"."id" = $3 AND "users"."id" = $4 AND "users"."age" = $5)`
		checkSQL(t, a, want, int64(1), int64(2), int64(3), int64(4), int32(10))
		checkSQL(t, b, want, int64(1), int64(2), int64(3), int64(4), int32(20))
	})
	t.Run("Exec", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`DELETE FROM "users" WHERE "users"."id" = $1`, int64(7)).Result(1)
		n, err := atla.NewDelete[User](db, userTable).Where(uID.EQ(7)).Exec(ctx)
		if n != 1 || err != nil {
			t.Fatalf("Exec() = %d, %v; want 1, nil", n, err)
		}
		s.Done(t)
	})
	t.Run("NoWhere", func(t *testing.T) {
		db, s := fakedb.Open(t)
		for name, q := range map[string]atla.Delete[User]{
			"NoWhere":        atla.NewDelete[User](db, userTable),
			"EmptyCall":      atla.NewDelete[User](db, userTable).Where(),
			"EmptyAnd":       atla.NewDelete[User](db, userTable).Where(atla.And()),
			"NestedEmptyAnd": atla.NewDelete[User](db, userTable).Where(atla.And(atla.And())),
			"OrOfEmptyAnd":   atla.NewDelete[User](db, userTable).Where(atla.Or(atla.And())),
			"EmptyAnds":      atla.NewDelete[User](db, userTable).Where(atla.And(), atla.And(atla.And(), atla.And())),
		} {
			if _, _, err := q.ToSQL(); !errors.Is(err, atla.ErrUnsafeMutation) {
				t.Errorf("%s: ToSQL error = %v; want ErrUnsafeMutation", name, err)
			}
			if n, err := q.Exec(ctx); n != 0 || !errors.Is(err, atla.ErrUnsafeMutation) {
				t.Errorf("%s: Exec() = %d, %v; want 0, ErrUnsafeMutation", name, n, err)
			}
		}
		checkNothingSent(t, s)
	})
	t.Run("AllowFullTable", func(t *testing.T) {
		base := atla.NewDelete[User](nil, userTable)
		checkSQL(t, base.AllowFullTable(), `DELETE FROM "users"`)
		if _, _, err := base.ToSQL(); !errors.Is(err, atla.ErrUnsafeMutation) {
			t.Errorf("base ToSQL error = %v; want ErrUnsafeMutation", err)
		}
	})
	t.Run("AllowFullTableEmptyWhere", func(t *testing.T) {
		base := atla.NewDelete[User](nil, userTable).AllowFullTable()
		checkSQL(t, base.Where(atla.And()), `DELETE FROM "users" WHERE 1 = 1`)
		checkSQL(t, base.Where(atla.And(atla.And())), `DELETE FROM "users" WHERE 1 = 1`)
		checkSQL(t, base.Where(atla.Or(atla.And())), `DELETE FROM "users" WHERE 1 = 1`)
	})
	t.Run("NilPredicate", func(t *testing.T) {
		db, s := fakedb.Open(t)
		for name, q := range map[string]atla.Delete[User]{
			"WhereNil":      atla.NewDelete[User](db, userTable).Where(nil),
			"AndNil":        atla.NewDelete[User](db, userTable).Where(atla.And(nil, uID.EQ(7))),
			"WhereNilAllow": atla.NewDelete[User](db, userTable).Where(nil).AllowFullTable(),
		} {
			checkNilPredicate(t, name+" ToSQL", func() error { _, _, err := q.ToSQL(); return err })
			checkNilPredicate(t, name+" Exec", func() error { _, err := q.Exec(ctx); return err })
		}
		checkNothingSent(t, s)
	})
	t.Run("AllowFullTableExec", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`DELETE FROM "users"`).Result(9)
		n, err := atla.NewDelete[User](db, userTable).AllowFullTable().Exec(ctx)
		if n != 9 || err != nil {
			t.Fatalf("Exec() = %d, %v; want 9, nil", n, err)
		}
		s.Done(t)
	})
	t.Run("ErrorClassified", func(t *testing.T) {
		db, s := fakedb.Open(t)
		s.ExpectExec(`DELETE FROM "users" WHERE "users"."id" = $1`, int64(7)).Err(pgUniqueErr{})
		_, err := atla.NewDelete[User](db, userTable).Where(uID.EQ(7)).Exec(ctx)
		var dbErr *atla.DBError
		if !errors.Is(err, atla.ErrUniqueViolation) || !errors.As(err, &dbErr) {
			t.Fatalf("Exec error = %v; want a DBError with ErrUniqueViolation", err)
		}
		if dbErr.Op != "delete" || dbErr.Table != "users" {
			t.Errorf("DBError = %+v; want Op delete, Table users", dbErr)
		}
		s.Done(t)
	})
}

func TestUpdateDeleteQueryInfo(t *testing.T) {
	ctx := context.Background()
	db, s := fakedb.Open(t)
	rec := &infoRecorder{DB: db}
	s.ExpectExec(`UPDATE "users" SET "name" = $1 WHERE "users"."id" = $2`, "bob", int64(7)).Result(1)
	s.ExpectQuery(`UPDATE "users" SET "name" = $1 WHERE "users"."id" = $2`+returningAll, "bob", int64(7)).
		Rows(userColNames, userRow(7, "bob", 30, nil))
	s.ExpectExec(`DELETE FROM "users" WHERE "users"."id" = $1`, int64(7)).Result(1)

	cols, vals := nameUpdate()
	upd := atla.NewUpdate(rec, userTable, cols, vals).Where(uID.EQ(7))
	if _, err := upd.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := upd.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := atla.NewDelete[User](rec, userTable).Where(uID.EQ(7)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	update := atla.QueryInfo{Op: "update", Table: "users", ReadOnly: false}
	del := atla.QueryInfo{Op: "delete", Table: "users", ReadOnly: false}
	if want := []atla.QueryInfo{update, update, del}; !slices.Equal(rec.infos, want) {
		t.Fatalf("QueryInfo = %+v; want %+v", rec.infos, want)
	}
	checkClosed(t, s)
}
