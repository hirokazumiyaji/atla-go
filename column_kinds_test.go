package atla

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

// Compile-time checks: column kinds satisfy the expected Expr types.
var (
	_ Expr[int32]               = NumberColumn[int32]{}
	_ Expr[string]              = StringColumn[string]{}
	_ Expr[sql.Null[string]]    = NullColumn[string]{}
	_ Expr[sql.Null[int64]]     = NullNumberColumn[int64]{}
	_ Expr[sql.Null[string]]    = NullStringColumn[string]{}
	_ Expr[string]              = NullColumn[string]{}.NotNull()
	_ Expr[int64]               = Count()
	_ Expr[sql.Null[float64]]   = Avg[int32](NumberColumn[int32]{})
	_ Expr[sql.Null[int64]]     = NullNumberColumn[int64]{}.Add(1)
	_ Expr[string]              = Coalesce[string](NullColumn[string]{}, "")
	_ Expr[bool]                = Raw[bool]("")
	_ interface{ predicate() }  = like{}
	_ interface{ exprOf(bool) } = like{}
)

func TestArithmetic(t *testing.T) {
	age := NewNumberColumn[int32](`"users"`, `"age"`)
	other := NewColumn[int32](`"posts"`, `"n"`)
	runGolden(t, dialect.Postgres, []golden{
		{"Add", age.Add(1), `("users"."age" + $1)`, []any{int32(1)}},
		{"Sub", age.Sub(1), `("users"."age" - $1)`, []any{int32(1)}},
		{"Mul", age.Mul(2), `("users"."age" * $1)`, []any{int32(2)}},
		{"Div", age.Div(2), `("users"."age" / $1)`, []any{int32(2)}},
		{"Nested", Add(age.Add(1), 2), `(("users"."age" + $1) + $2)`, []any{int32(1), int32(2)}},
		{"MulExpr", age.MulExpr(age), `("users"."age" * "users"."age")`, nil},
		{"AddExpr", age.AddExpr(other), `("users"."age" + "posts"."n")`, nil},
		{"SubExpr", age.SubExpr(other), `("users"."age" - "posts"."n")`, nil},
		{"DivExpr", age.DivExpr(other), `("users"."age" / "posts"."n")`, nil},
		{"GenericSub", Sub[int32](other, 3), `("posts"."n" - $1)`, []any{int32(3)}},
		{"GenericMulExpr", MulExpr[int32](other, age), `("posts"."n" * "users"."age")`, nil},
		{"InComparison", GT(age.Add(1), 5), `("users"."age" + $1) > $2`, []any{int32(1), int32(5)}},
		{"PromotedEQ", age.EQ(3), `"users"."age" = $1`, []any{int32(3)}},
	})
}

func TestGenericOps(t *testing.T) {
	age, _ := userCols()
	userID := NewColumn[int64](`"posts"`, `"user_id"`)
	runGolden(t, dialect.Postgres, []golden{
		{"EQ", EQ[int64](age, 1), `"users"."age" = $1`, []any{int64(1)}},
		{"NE", NE[int64](age, 1), `"users"."age" <> $1`, []any{int64(1)}},
		{"LT", LT[int64](age, 1), `"users"."age" < $1`, []any{int64(1)}},
		{"LTE", LTE[int64](age, 1), `"users"."age" <= $1`, []any{int64(1)}},
		{"GT", GT[int64](age, 1), `"users"."age" > $1`, []any{int64(1)}},
		{"GTE", GTE[int64](age, 1), `"users"."age" >= $1`, []any{int64(1)}},
		{"EQExpr", EQExpr[int64](age, userID), `"users"."age" = "posts"."user_id"`, nil},
		{"NEExpr", NEExpr[int64](age, userID), `"users"."age" <> "posts"."user_id"`, nil},
		{"LTExpr", LTExpr[int64](age, userID), `"users"."age" < "posts"."user_id"`, nil},
		{"LTEExpr", LTEExpr[int64](age, userID), `"users"."age" <= "posts"."user_id"`, nil},
		{"GTExpr", GTExpr[int64](age, userID), `"users"."age" > "posts"."user_id"`, nil},
		{"GTEExpr", GTEExpr[int64](age, userID), `"users"."age" >= "posts"."user_id"`, nil},
		{"IsNull", IsNull[int64](age), `"users"."age" IS NULL`, nil},
		{"IsNotNull", IsNotNull[int64](age), `"users"."age" IS NOT NULL`, nil},
		{"In", In[int64](age, 1, 2), `"users"."age" IN ($1, $2)`, []any{int64(1), int64(2)}},
		{"NotIn", NotIn[int64](age, 1), `"users"."age" NOT IN ($1)`, []any{int64(1)}},
		{"InEmpty", In[int64](age), `1 = 0`, nil},
		{"Asc", Asc[int64](age), `"users"."age" ASC`, nil},
		{"Desc", Desc[int64](age), `"users"."age" DESC`, nil},
	})
}

func TestStringPredicates(t *testing.T) {
	name := NewStringColumn[string](`"users"`, `"name"`)
	runGolden(t, dialect.Postgres, []golden{
		{"Like", name.Like("a%"), `"users"."name" LIKE $1`, []any{"a%"}},
		{"ILike", name.ILike("a%"), `"users"."name" ILIKE $1`, []any{"a%"}},
		{"PromotedEQ", name.EQ("a"), `"users"."name" = $1`, []any{"a"}},
		{"AsOperand", NewColumn[bool](`"users"`, `"x"`).EQExpr(name.Like("a")), `"users"."x" = ("users"."name" LIKE $1)`, []any{"a"}},
	})
	runGolden(t, dialect.SQLite, []golden{
		{"Like", name.Like("a%"), `"users"."name" LIKE ?`, []any{"a%"}},
		{"ILike", name.ILike("a%"), `LOWER("users"."name") LIKE LOWER(?)`, []any{"a%"}},
	})
}

type userStatus string

func TestStringColumnEnumType(t *testing.T) {
	status := NewStringColumn[userStatus](`"users"`, `"status"`)
	runGolden(t, dialect.Postgres, []golden{
		{"EQ", status.EQ("active"), `"users"."status" = $1`, []any{userStatus("active")}},
		{"Like", status.Like("a%"), `"users"."status" LIKE $1`, []any{"a%"}},
	})
}

func TestContainsEscapesWildcards(t *testing.T) {
	name := NewStringColumn[string](`"users"`, `"name"`)
	runGolden(t, dialect.Postgres, []golden{
		{"Contains", name.Contains(`50%_off\`), `"users"."name" LIKE $1 ESCAPE '\'`, []any{`%50\%\_off\\%`}},
		{"HasPrefix", name.HasPrefix("a_"), `"users"."name" LIKE $1 ESCAPE '\'`, []any{`a\_%`}},
		{"HasSuffix", name.HasSuffix("%"), `"users"."name" LIKE $1 ESCAPE '\'`, []any{`%\%`}},
		{"Plain", name.Contains("abc"), `"users"."name" LIKE $1 ESCAPE '\'`, []any{`%abc%`}},
	})
	runGolden(t, dialect.SQLite, []golden{
		{"Contains", name.Contains("a"), `"users"."name" LIKE ? ESCAPE '\'`, []any{`%a%`}},
	})
}

func TestNullColumns(t *testing.T) {
	bio := NewNullStringColumn[string](`"users"`, `"bio"`)
	other := NewColumn[string](`"posts"`, `"title"`)
	score := NewNullNumberColumn[int64](`"users"`, `"score"`)
	deleted := NewNullColumn[int64](`"users"`, `"deleted_at"`)
	runGolden(t, dialect.Postgres, []golden{
		{"Self", bio, `"users"."bio"`, nil},
		{"EQ", bio.EQ("x"), `"users"."bio" = $1`, []any{"x"}},
		{"NE", bio.NE("x"), `"users"."bio" <> $1`, []any{"x"}},
		{"LT", deleted.LT(5), `"users"."deleted_at" < $1`, []any{int64(5)}},
		{"LTE", deleted.LTE(5), `"users"."deleted_at" <= $1`, []any{int64(5)}},
		{"GT", deleted.GT(5), `"users"."deleted_at" > $1`, []any{int64(5)}},
		{"GTE", deleted.GTE(5), `"users"."deleted_at" >= $1`, []any{int64(5)}},
		{"EQExpr", bio.EQExpr(other), `"users"."bio" = "posts"."title"`, nil},
		{"NEExpr", bio.NEExpr(other), `"users"."bio" <> "posts"."title"`, nil},
		{"LTExpr", bio.LTExpr(other), `"users"."bio" < "posts"."title"`, nil},
		{"LTEExpr", bio.LTEExpr(other), `"users"."bio" <= "posts"."title"`, nil},
		{"GTExpr", bio.GTExpr(other), `"users"."bio" > "posts"."title"`, nil},
		{"GTEExpr", bio.GTEExpr(other), `"users"."bio" >= "posts"."title"`, nil},
		{"In", bio.In("a", "b"), `"users"."bio" IN ($1, $2)`, []any{"a", "b"}},
		{"NotIn", bio.NotIn("a"), `"users"."bio" NOT IN ($1)`, []any{"a"}},
		{"Between", deleted.Between(1, 2), `"users"."deleted_at" BETWEEN $1 AND $2`, []any{int64(1), int64(2)}},
		{"IsNull", bio.IsNull(), `"users"."bio" IS NULL`, nil},
		{"IsNotNull", bio.IsNotNull(), `"users"."bio" IS NOT NULL`, nil},
		{"Asc", bio.Asc(), `"users"."bio" ASC`, nil},
		{"Desc", bio.Desc().NullsLast(), `"users"."bio" DESC NULLS LAST`, nil},
		{"NotNull", bio.NotNull(), `"users"."bio"`, nil},
		{"Coalesce", Coalesce[string](bio, ""), `COALESCE("users"."bio", $1)`, []any{""}},
		{"Like", bio.Like("a%"), `"users"."bio" LIKE $1`, []any{"a%"}},
		{"ILike", bio.ILike("a%"), `"users"."bio" ILIKE $1`, []any{"a%"}},
		{"Contains", bio.Contains("_"), `"users"."bio" LIKE $1 ESCAPE '\'`, []any{`%\_%`}},
		{"HasPrefix", bio.HasPrefix("a"), `"users"."bio" LIKE $1 ESCAPE '\'`, []any{`a%`}},
		{"HasSuffix", bio.HasSuffix("a"), `"users"."bio" LIKE $1 ESCAPE '\'`, []any{`%a`}},
		{"Add", score.Add(1), `("users"."score" + $1)`, []any{int64(1)}},
		{"Sub", score.Sub(1), `("users"."score" - $1)`, []any{int64(1)}},
		{"Mul", score.Mul(2), `("users"."score" * $1)`, []any{int64(2)}},
		{"Div", score.Div(2), `("users"."score" / $1)`, []any{int64(2)}},
		{"PromotedEQ", score.EQ(3), `"users"."score" = $1`, []any{int64(3)}},
		{"GenericOnNull", IsNull[sql.Null[string]](bio), `"users"."bio" IS NULL`, nil},
	})
	runGolden(t, dialect.SQLite, []golden{
		{"ILike", bio.ILike("a%"), `LOWER("users"."bio") LIKE LOWER(?)`, []any{"a%"}},
	})
}

func TestNullColumnIsComparable(t *testing.T) {
	a := NewNullColumn[int64](`"users"`, `"a"`)
	if a != NewNullColumn[int64](`"users"`, `"a"`) {
		t.Error("equal columns compare unequal")
	}
	if a == NewNullColumn[int64](`"users"`, `"b"`) {
		t.Error("different columns compare equal")
	}
}

func TestAggregates(t *testing.T) {
	age := NewNumberColumn[int32](`"users"`, `"age"`)
	name := NewColumn[string](`"users"`, `"name"`)
	runGolden(t, dialect.Postgres, []golden{
		{"Count", Count(), `COUNT(*)`, nil},
		{"CountOf", CountOf[int32](age), `COUNT("users"."age")`, nil},
		{"CountDistinct", CountDistinct[int32](age), `COUNT(DISTINCT "users"."age")`, nil},
		{"Sum", Sum[int32](age), `SUM("users"."age")`, nil},
		{"Avg", Avg[int32](age), `AVG("users"."age")`, nil},
		{"Min", Min[string](name), `MIN("users"."name")`, nil},
		{"Max", Max[int32](age), `MAX("users"."age")`, nil},
		{"SumOfArith", Sum(age.Add(1)), `SUM(("users"."age" + $1))`, []any{int32(1)}},
		{"HavingCount", GT(Count(), 5), `COUNT(*) > $1`, []any{int64(5)}},
		{"OrderByMax", Desc(Max[int32](age)), `MAX("users"."age") DESC`, nil},
		{"CoalesceMax", Coalesce(Max[int32](age), 0), `COALESCE(MAX("users"."age"), $1)`, []any{int32(0)}},
	})
}

func TestRaw(t *testing.T) {
	runGolden(t, dialect.Postgres, []golden{
		{"Params", EQ(Raw[int64]("length(?) + ?", "ab", 1), 3), `length($1) + $2 = $3`, []any{"ab", 1, int64(3)}},
		{"Escaped", Raw[bool](`"data" ?? 'k'`), `"data" ? 'k'`, nil},
		{"EscapedThenParam", Raw[bool](`"data" ??? 'k'`, "x"), `"data" ?$1 'k'`, []any{"x"}},
		{"NoMarkers", Raw[int64]("now()"), `now()`, nil},
	})
	runGolden(t, dialect.SQLite, []golden{
		{"Params", Raw[int64]("? + ?", 1, 2), `? + ?`, []any{1, 2}},
		{"Escaped", Raw[bool](`a ?? b`), `a ? b`, nil},
	})
}

func TestRawCopiesArgs(t *testing.T) {
	args := []any{1}
	r := Raw[int64]("?", args...)
	args[0] = 99
	_, got := sqlOf(t, dialect.Postgres, r)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("args: got %#v, want [1]", got)
	}
}

func TestRawPlaceholderMismatch(t *testing.T) {
	cases := []struct {
		name string
		n    node
		want string
	}{
		{"TooFewArgs", Raw[bool]("? = ?", 1), "atla: Raw: 2 placeholders, 1 args"},
		{"TooManyArgs", Raw[bool]("?", 1, 2), "atla: Raw: 1 placeholders, 2 args"},
		{"EscapedNotCounted", Raw[bool]("??", 1), "atla: Raw: 0 placeholders, 1 args"},
		{"Nested", And(Raw[bool]("? = ?", 1)), "atla: Raw: 2 placeholders, 1 args"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, args, err := compile(dialect.Postgres, "", tc.n)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err: got %v, want containing %q", err, tc.want)
			}
			if s != "" || args != nil {
				t.Errorf("on error got (%q, %v), want empty", s, args)
			}
		})
	}
}

func TestPredicateShapedOperandsOfInBetweenNull(t *testing.T) {
	a := NewColumn[int64](`"t"`, `"a"`)
	b := NewColumn[bool](`"t"`, `"b"`)
	runGolden(t, dialect.Postgres, []golden{
		{"IsNullNot", IsNull(Not(a.EQ(1))), `(NOT ("t"."a" = $1)) IS NULL`, []any{int64(1)}},
		{"IsNotNullCmp", IsNotNull(a.EQ(1)), `("t"."a" = $1) IS NOT NULL`, []any{int64(1)}},
		{"InCmp", In(a.EQ(1), true), `("t"."a" = $1) IN ($2)`, []any{int64(1), true}},
		{"NotInCmp", NotIn(a.EQ(1), true), `("t"."a" = $1) NOT IN ($2)`, []any{int64(1), true}},
		{"InMultiAnd", In(And(b, a.EQ(1)), true), `("t"."b" AND "t"."a" = $1) IN ($2)`, []any{int64(1), true}},
		{"BetweenCmp", betweenOf(a.EQ(1), false, true), `("t"."a" = $1) BETWEEN $2 AND $3`, []any{int64(1), false, true}},
		{"BetweenBoundsCmp", between{x: b, lo: a.EQ(1), hi: a.EQ(2)}, `"t"."b" BETWEEN ("t"."a" = $1) AND ("t"."a" = $2)`, []any{int64(1), int64(2)}},
		{"AtomsBare", IsNull[bool](b), `"t"."b" IS NULL`, nil},
		{"RawStaysBare", IsNull(Raw[bool]("x = 1")), `x = 1 IS NULL`, nil},
	})
}

func TestRawGroupedInLogicalAndArith(t *testing.T) {
	id := NewNumberColumn[int64](`"users"`, `"id"`)
	runGolden(t, dialect.Postgres, []golden{
		{"And", And(Raw[bool]("a = 1 OR b = 2"), id.EQ(3)), `((a = 1 OR b = 2) AND "users"."id" = $1)`, []any{int64(3)}},
		{"Or", Or(id.EQ(3), Raw[bool]("a AND b")), `("users"."id" = $1 OR (a AND b))`, []any{int64(3)}},
		{"SingleAndBare", And(Raw[bool]("a = 1 OR b = 2")), `a = 1 OR b = 2`, nil},
		{"MulLeft", Mul(Raw[int64]("a + b"), 2), `((a + b) * $1)`, []any{int64(2)}},
		{"MulExprRight", id.MulExpr(Raw[int64]("a + ?", 1)), `("users"."id" * (a + $1))`, []any{1}},
		{"ComparisonBare", EQ(Raw[int64]("a + b"), 3), `a + b = $1`, []any{int64(3)}},
	})
}

func TestRawGroupedThroughSingleOperandLogical(t *testing.T) {
	a := NewColumn[int64](`"t"`, `"a"`)
	r := Raw[bool]("x = 1 OR y = 2")
	runGolden(t, dialect.Postgres, []golden{
		{"AndOfSingleOr", And(Or(r), a.EQ(1)), `((x = 1 OR y = 2) AND "t"."a" = $1)`, []any{int64(1)}},
		{"OrOfSingleAnd", Or(And(r), a.EQ(1)), `((x = 1 OR y = 2) OR "t"."a" = $1)`, []any{int64(1)}},
		{"NestedSingles", And(Or(And(r)), a.EQ(1)), `((x = 1 OR y = 2) AND "t"."a" = $1)`, []any{int64(1)}},
		{"SingleOrBare", Or(r), `x = 1 OR y = 2`, nil},
	})
}
