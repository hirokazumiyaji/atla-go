package atla_test

import (
	"reflect"
	"strings"
	"testing"

	atla "github.com/hirokazumiyaji/atla-go"
)

const (
	userCols = `"users"."id", "users"."name", "users"."age", "users"."bio"`
	postCols = `"posts"."id", "posts"."author_id", "posts"."title"`
)

type sqler interface {
	ToSQL() (string, []any, error)
}

// checkSQL asserts that q compiles to wantSQL with wantArgs.
func checkSQL(t *testing.T, q sqler, wantSQL string, wantArgs ...any) {
	t.Helper()
	got, args, err := q.ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if got != wantSQL {
		t.Errorf("sql:\n got  %s\n want %s", got, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args: got %#v, want %#v", args, wantArgs)
	}
}

func TestSelectToSQL(t *testing.T) {
	sel := atla.NewSelect[User](nil, userTable)
	from := `SELECT ` + userCols + ` FROM "users"`

	t.Run("Bare", func(t *testing.T) {
		checkSQL(t, sel, from)
	})
	t.Run("AllClauses", func(t *testing.T) {
		q := sel.Where(uAge.GTE(20)).OrderBy(uID.Desc()).Limit(10).Offset(5)
		checkSQL(t, q, from+` WHERE "users"."age" >= $1 ORDER BY "users"."id" DESC LIMIT 10 OFFSET 5`, int32(20))
	})
	t.Run("WhereTwiceANDs", func(t *testing.T) {
		q := sel.Where(uAge.GTE(20)).Where(uName.EQ("bob"))
		checkSQL(t, q, from+` WHERE ("users"."age" >= $1 AND "users"."name" = $2)`, int32(20), "bob")
	})
	t.Run("WhereSeveralInOneCall", func(t *testing.T) {
		q := sel.Where(uAge.GTE(20), uName.EQ("bob"))
		checkSQL(t, q, from+` WHERE ("users"."age" >= $1 AND "users"."name" = $2)`, int32(20), "bob")
	})
	t.Run("WhereNoPredicates", func(t *testing.T) {
		checkSQL(t, sel.Where(), from)
	})
	t.Run("WhereOrIsGrouped", func(t *testing.T) {
		q := sel.Where(atla.Or(uAge.LT(18), uAge.GT(65)), uName.EQ("bob"))
		checkSQL(t, q, from+` WHERE (("users"."age" < $1 OR "users"."age" > $2) AND "users"."name" = $3)`,
			int32(18), int32(65), "bob")
	})
	t.Run("OrderByCommaJoined", func(t *testing.T) {
		q := sel.OrderBy(uAge.Asc().NullsLast()).OrderBy(uID.Desc())
		checkSQL(t, q, from+` ORDER BY "users"."age" ASC NULLS LAST, "users"."id" DESC`)
	})
	t.Run("LimitOnly", func(t *testing.T) {
		checkSQL(t, sel.Limit(0), from+` LIMIT 0`)
	})
	t.Run("ForUpdateSkipLocked", func(t *testing.T) {
		q := sel.Where(uAge.GTE(20)).Limit(1).ForUpdate(atla.SkipLocked())
		checkSQL(t, q, from+` WHERE "users"."age" >= $1 LIMIT 1 FOR UPDATE SKIP LOCKED`, int32(20))
	})
	t.Run("ForUpdate", func(t *testing.T) {
		checkSQL(t, sel.ForUpdate(), from+` FOR UPDATE`)
	})
	t.Run("ForShareOfNoWait", func(t *testing.T) {
		q := sel.ForShare(atla.Of(`"users"`), atla.NoWait())
		checkSQL(t, q, from+` FOR SHARE OF "users" NOWAIT`)
	})
	t.Run("LastLockWins", func(t *testing.T) {
		q := sel.ForUpdate(atla.NoWait()).ForShare()
		checkSQL(t, q, from+` FOR SHARE`)
	})
}

func TestSelectBranchingDoesNotAlias(t *testing.T) {
	// Three single-predicate Where calls leave the where slice at length 3
	// with capacity 4 (append grows 1 -> 2 -> 4), so an append that does not
	// clone first would write both branches into the same backing array.
	base := atla.NewSelect[User](nil, userTable).
		Where(uAge.GTE(1)).Where(uAge.LTE(99)).Where(uAge.NE(50)).
		OrderBy(uAge.Asc()).OrderBy(uName.Asc()).OrderBy(uBio.Asc())
	a := base.Where(uName.EQ("a")).OrderBy(uID.Asc())
	b := base.Where(uName.EQ("b")).OrderBy(uID.Desc())

	baseWhere := ` WHERE ("users"."age" >= $1 AND "users"."age" <= $2 AND "users"."age" <> $3`
	baseOrder := ` ORDER BY "users"."age" ASC, "users"."name" ASC, "users"."bio" ASC`
	from := `SELECT ` + userCols + ` FROM "users"`

	checkSQL(t, base, from+baseWhere+`)`+baseOrder, int32(1), int32(99), int32(50))
	checkSQL(t, a, from+baseWhere+` AND "users"."name" = $4)`+baseOrder+`, "users"."id" ASC`,
		int32(1), int32(99), int32(50), "a")
	checkSQL(t, b, from+baseWhere+` AND "users"."name" = $4)`+baseOrder+`, "users"."id" DESC`,
		int32(1), int32(99), int32(50), "b")
}

func TestLockOfDoesNotAliasCaller(t *testing.T) {
	tables := []string{`"users"`}
	of := atla.Of(tables...)
	tables[0] = `"posts"` // after Of
	q := atla.NewSelect[User](nil, userTable).ForUpdate(of)
	tables[0] = `"comments"` // after ForUpdate
	checkSQL(t, q, `SELECT `+userCols+` FROM "users" FOR UPDATE OF "users"`)
}

func TestSelectSubqueryPlaceholderNumbering(t *testing.T) {
	sub := atla.NewSelect[Post](nil, postTable).Where(pAuthorID.EQExpr(uID), pTitle.EQ("x")).Subquery()
	q := atla.NewSelect[User](nil, userTable).Where(uAge.GT(1), atla.Exists(sub), uName.EQ("n"))
	checkSQL(t, q, `SELECT `+userCols+` FROM "users" WHERE ("users"."age" > $1 AND EXISTS (SELECT `+postCols+
		` FROM "posts" WHERE ("posts"."author_id" = "users"."id" AND "posts"."title" = $2)) AND "users"."name" = $3)`,
		int32(1), "x", "n")
}

func TestSelectSubqueryClauses(t *testing.T) {
	sub := atla.NewSelect[Post](nil, postTable).Where(pAuthorID.EQExpr(uID)).
		OrderBy(pID.Desc()).Limit(1).Subquery()
	q := atla.NewSelect[User](nil, userTable).Where(atla.Not(atla.Exists(sub)))
	checkSQL(t, q, `SELECT `+userCols+` FROM "users" WHERE NOT (EXISTS (SELECT `+postCols+
		` FROM "posts" WHERE "posts"."author_id" = "users"."id" ORDER BY "posts"."id" DESC LIMIT 1))`)
}

func TestInQuery(t *testing.T) {
	authorIDs := postTable
	authorIDs.SelectList = `"posts"."author_id"`
	titles := postTable
	titles.SelectList = `"posts"."title"`

	t.Run("Column", func(t *testing.T) {
		sub := atla.NewSubqueryForTest[int64](atla.NewSelect[Post](nil, authorIDs).Where(pTitle.EQ("x")).Subquery())
		q := atla.NewSelect[User](nil, userTable).Where(uAge.GT(1), uID.InQuery(sub), uName.EQ("n"))
		checkSQL(t, q, `SELECT `+userCols+` FROM "users" WHERE ("users"."age" > $1 AND "users"."id" IN `+
			`(SELECT "posts"."author_id" FROM "posts" WHERE "posts"."title" = $2) AND "users"."name" = $3)`,
			int32(1), "x", "n")
	})
	t.Run("NullColumn", func(t *testing.T) {
		sub := atla.NewSubqueryForTest[string](atla.NewSelect[Post](nil, titles).Subquery())
		q := atla.NewSelect[User](nil, userTable).Where(uBio.InQuery(sub))
		checkSQL(t, q, `SELECT `+userCols+` FROM "users" WHERE "users"."bio" IN (SELECT "posts"."title" FROM "posts")`)
	})
}

func TestEmptySubqueryFails(t *testing.T) {
	q := atla.NewSelect[User](nil, userTable).Where(atla.Exists(atla.AnySubquery{}))
	if _, _, err := q.ToSQL(); err == nil || !strings.HasPrefix(err.Error(), "atla: ") {
		t.Fatalf("ToSQL error = %v, want an atla: error", err)
	}
	q = atla.NewSelect[User](nil, userTable).Where(uID.InQuery(atla.Subquery[int64]{}))
	if _, _, err := q.ToSQL(); err == nil {
		t.Fatal("ToSQL with a zero Subquery succeeded")
	}
}

func TestSelectAliasFromItem(t *testing.T) {
	q := atla.NewSelect[User](nil, userTable.Alias("m"))
	checkSQL(t, q, `SELECT "m"."id", "m"."name", "m"."age", "m"."bio" FROM "users" AS "m"`)
}

func TestSelectSQLiteOffsetOnly(t *testing.T) {
	sel := atla.NewSelect[User](nil, sqliteUserTable())
	from := `SELECT ` + userCols + ` FROM "users"`
	checkSQL(t, sel.Where(uAge.GTE(20)).Offset(5), from+` WHERE "users"."age" >= ? LIMIT -1 OFFSET 5`, int32(20))
	// SQLite has no row locks, so the lock clause renders nothing.
	checkSQL(t, sel.Limit(3).ForUpdate(atla.SkipLocked()), from+` LIMIT 3`)
}
