package atla

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

// Compile-time checks: columns and nodes satisfy the expected Expr types.
var (
	_ Expr[int64]                       = Column[int64]{}
	_ Expr[sql.Null[int64]]             = Column[int64]{}.Null()
	_ Pred                              = And()
	_ Expr[string]                      = param[string]{}
	_ Pred                              = binary[bool]{}
	_ Pred                              = unary[bool]{}
	_ Pred                              = inList{}
	_ Pred                              = between{}
	_ Pred                              = exists{}
	_ Pred                              = inQuery{}
	_ interface{ predicate() }          = exists{}
	_ interface{ predicate() }          = inQuery{}
	_ interface{ appendSQL(*compiler) } = Order{}
)

func sqlOf(t *testing.T, d dialect.Dialect, n node) (string, []any) {
	t.Helper()
	s, args, err := compile(d, "", n)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s, args
}

type golden struct {
	name string
	n    node
	sql  string
	args []any
}

func runGolden(t *testing.T, d dialect.Dialect, cases []golden) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, args := sqlOf(t, d, tc.n)
			if s != tc.sql {
				t.Errorf("sql:\n got  %s\n want %s", s, tc.sql)
			}
			if !reflect.DeepEqual(args, tc.args) {
				t.Errorf("args: got %#v, want %#v", args, tc.args)
			}
		})
	}
}

func userCols() (age Column[int64], name Column[string]) {
	return NewColumn[int64](`"users"`, `"age"`), NewColumn[string](`"users"`, `"name"`)
}

func TestComparisonsPostgres(t *testing.T) {
	age, name := userCols()
	userID := NewColumn[int64](`"posts"`, `"user_id"`)
	runGolden(t, dialect.Postgres, []golden{
		{"EQ", age.EQ(20), `"users"."age" = $1`, []any{int64(20)}},
		{"NE", age.NE(1), `"users"."age" <> $1`, []any{int64(1)}},
		{"LT", age.LT(1), `"users"."age" < $1`, []any{int64(1)}},
		{"LTE", age.LTE(1), `"users"."age" <= $1`, []any{int64(1)}},
		{"GT", age.GT(1), `"users"."age" > $1`, []any{int64(1)}},
		{"GTE", age.GTE(1), `"users"."age" >= $1`, []any{int64(1)}},
		{"EQExpr", age.EQExpr(userID), `"users"."age" = "posts"."user_id"`, nil},
		{"NEExpr", age.NEExpr(userID), `"users"."age" <> "posts"."user_id"`, nil},
		{"LTExpr", age.LTExpr(userID), `"users"."age" < "posts"."user_id"`, nil},
		{"LTEExpr", age.LTEExpr(userID), `"users"."age" <= "posts"."user_id"`, nil},
		{"GTExpr", age.GTExpr(userID), `"users"."age" > "posts"."user_id"`, nil},
		{"GTEExpr", age.GTEExpr(userID), `"users"."age" >= "posts"."user_id"`, nil},
		{"string", name.EQ("bob"), `"users"."name" = $1`, []any{"bob"}},
	})
}

func TestPredicateOperandsParenthesized(t *testing.T) {
	age, name := userCols()
	active := NewColumn[bool](`"users"`, `"active"`)
	runGolden(t, dialect.Postgres, []golden{
		{"Comparison", active.EQExpr(age.EQ(1)), `"users"."active" = ("users"."age" = $1)`, []any{int64(1)}},
		{"SingleAnd", active.EQExpr(And(age.EQ(1))), `"users"."active" = ("users"."age" = $1)`, []any{int64(1)}},
		{"IsNull", active.EQExpr(name.IsNull()), `"users"."active" = ("users"."name" IS NULL)`, nil},
		{"In", active.NEExpr(age.In(1, 2)), `"users"."active" <> ("users"."age" IN ($1, $2))`, []any{int64(1), int64(2)}},
		{"Between", active.EQExpr(age.Between(1, 2)), `"users"."active" = ("users"."age" BETWEEN $1 AND $2)`, []any{int64(1), int64(2)}},
		{"EmptyOr", active.EQExpr(Or()), `"users"."active" = (1 = 0)`, nil},
		{"Not", active.EQExpr(Not(age.EQ(1))), `"users"."active" = (NOT ("users"."age" = $1))`, []any{int64(1)}},
		{"MultiAnd", active.EQExpr(And(age.EQ(1), name.EQ("b"))), `"users"."active" = ("users"."age" = $1 AND "users"."name" = $2)`, []any{int64(1), "b"}},
		{"Atoms", active.EQExpr(active), `"users"."active" = "users"."active"`, nil},
	})
}

func TestComparisonsSQLite(t *testing.T) {
	age, name := userCols()
	runGolden(t, dialect.SQLite, []golden{
		{"EQ", age.EQ(20), `"users"."age" = ?`, []any{int64(20)}},
		{"And", And(age.EQ(1), name.EQ("a")), `("users"."age" = ? AND "users"."name" = ?)`, []any{int64(1), "a"}},
	})
}

func TestInBetweenNull(t *testing.T) {
	age, _ := userCols()
	runGolden(t, dialect.Postgres, []golden{
		{"In", age.In(1, 2, 3), `"users"."age" IN ($1, $2, $3)`, []any{int64(1), int64(2), int64(3)}},
		{"InEmpty", age.In(), `1 = 0`, nil},
		{"NotInEmpty", age.NotIn(), `1 = 1`, nil},
		{"NotIn", age.NotIn(4), `"users"."age" NOT IN ($1)`, []any{int64(4)}},
		{"Between", age.Between(1, 9), `"users"."age" BETWEEN $1 AND $2`, []any{int64(1), int64(9)}},
		{"IsNull", age.IsNull(), `"users"."age" IS NULL`, nil},
		{"IsNotNull", age.IsNotNull(), `"users"."age" IS NOT NULL`, nil},
	})
}

func TestInCopiesValues(t *testing.T) {
	age, _ := userCols()
	vs := []int64{1, 2}
	p := age.In(vs...)
	vs[0] = 99
	_, args := sqlOf(t, dialect.Postgres, p)
	if !reflect.DeepEqual(args, []any{int64(1), int64(2)}) {
		t.Errorf("args: got %#v, want [1 2]", args)
	}
}

func TestLogical(t *testing.T) {
	age, name := userCols()
	a, b, c := age.EQ(1), name.EQ("b"), age.GT(3)
	runGolden(t, dialect.Postgres, []golden{
		{"And2", And(a, b), `("users"."age" = $1 AND "users"."name" = $2)`, []any{int64(1), "b"}},
		{"And1", And(a), `"users"."age" = $1`, []any{int64(1)}},
		{"AndFlatten", And(And(a, b), c), `("users"."age" = $1 AND "users"."name" = $2 AND "users"."age" > $3)`, []any{int64(1), "b", int64(3)}},
		{"OrNested", Or(a, And(b, c)), `("users"."age" = $1 OR ("users"."name" = $2 AND "users"."age" > $3))`, []any{int64(1), "b", int64(3)}},
		{"OrFlatten", Or(Or(a, b), c), `("users"."age" = $1 OR "users"."name" = $2 OR "users"."age" > $3)`, []any{int64(1), "b", int64(3)}},
		{"AndEmpty", And(), `1 = 1`, nil},
		{"OrEmpty", Or(), `1 = 0`, nil},
		{"Not", Not(a), `NOT ("users"."age" = $1)`, []any{int64(1)}},
	})
}

func TestLogicalDoesNotAliasInput(t *testing.T) {
	age, name := userCols()
	a, b, c := age.EQ(1), name.EQ("b"), age.GT(3)
	ps := []Pred{a, b}
	p := And(ps...)
	ps[0] = c
	s, _ := sqlOf(t, dialect.Postgres, p)
	if want := `("users"."age" = $1 AND "users"."name" = $2)`; s != want {
		t.Errorf("got %s, want %s", s, want)
	}
}

func TestOrder(t *testing.T) {
	age, _ := userCols()
	runGolden(t, dialect.Postgres, []golden{
		{"Asc", age.Asc(), `"users"."age" ASC`, nil},
		{"Desc", age.Desc(), `"users"."age" DESC`, nil},
		{"DescNullsLast", age.Desc().NullsLast(), `"users"."age" DESC NULLS LAST`, nil},
		{"AscNullsFirst", age.Asc().NullsFirst(), `"users"."age" ASC NULLS FIRST`, nil},
	})
}

func TestNullAdapterRendersSameSQL(t *testing.T) {
	age, _ := userCols()
	runGolden(t, dialect.Postgres, []golden{
		{"Null", age.Null(), `"users"."age"`, nil},
	})
}

func TestColumnIsComparable(t *testing.T) {
	age, _ := userCols()
	if age != NewColumn[int64](`"users"`, `"age"`) {
		t.Error("equal columns compare unequal")
	}
	if age == NewColumn[int64](`"users"`, `"id"`) {
		t.Error("different columns compare equal")
	}
}

func TestCompilePrefixAndNilNode(t *testing.T) {
	age, _ := userCols()
	s, args, err := compile(dialect.Postgres, `SELECT 1`, nil)
	if err != nil || s != `SELECT 1` || args != nil {
		t.Errorf("got (%q, %v, %v), want (%q, nil, nil)", s, args, err, `SELECT 1`)
	}
	s, args, err = compile(dialect.Postgres, `SELECT * FROM "users" WHERE `, age.EQ(5))
	if err != nil || s != `SELECT * FROM "users" WHERE "users"."age" = $1` || !reflect.DeepEqual(args, []any{int64(5)}) {
		t.Errorf("got (%q, %v, %v)", s, args, err)
	}
}

func TestCompilerValue(t *testing.T) {
	age, _ := userCols()
	c := &compiler{d: dialect.Postgres}
	c.value(int64(7))
	c.buf = append(c.buf, ", "...)
	c.value(age)
	c.buf = append(c.buf, ", "...)
	c.value("x")
	if got, want := string(c.buf), `$1, "users"."age", $2`; got != want {
		t.Errorf("sql: got %s, want %s", got, want)
	}
	if !reflect.DeepEqual(c.args, []any{int64(7), "x"}) {
		t.Errorf("args: got %#v", c.args)
	}
}

// failNode is a Pred that records err when compiled.
type failNode struct{ err error }

func (f failNode) appendSQL(c *compiler) { c.fail(f.err) }
func (failNode) exprOf(bool)             {}

func TestCompilerFailKeepsFirstError(t *testing.T) {
	age, _ := userCols()
	e1, e2 := errors.New("first"), errors.New("second")
	s, args, err := compile(dialect.Postgres, "", And(age.EQ(1), failNode{e1}, failNode{e2}))
	if !errors.Is(err, e1) {
		t.Errorf("err: got %v, want %v", err, e1)
	}
	if s != "" || args != nil {
		t.Errorf("on error got (%q, %v), want empty", s, args)
	}
}

func TestNilOperandFails(t *testing.T) {
	age, _ := userCols()
	a := age.EQ(1)
	cases := []struct {
		name string
		n    node
		want string
	}{
		{"AndNil", And(nil, a), "atla: nil predicate"},
		{"AndSingleNil", And(nil), "atla: nil predicate"},
		{"OrNil", Or(a, nil), "atla: nil predicate"},
		{"NotNil", Not(nil), "atla: nil predicate"},
		{"IsNullNil", IsNull[bool](nil), "atla: nil predicate"},
		{"BinaryNil", EQExpr[int64](age, nil), "atla: nil predicate"},
		{"ZeroOrder", Order{}, "atla: zero Order"},
		{"AscNil", Asc[int64](nil), "atla: zero Order"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, args, err := compile(dialect.Postgres, "", tc.n)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err: got %v, want %q", err, tc.want)
			}
			if s != "" || args != nil {
				t.Errorf("on error got (%q, %v), want empty", s, args)
			}
		})
	}
}

func TestAlwaysTrue(t *testing.T) {
	age, _ := userCols()
	a := age.EQ(1)
	cases := []struct {
		name string
		p    Pred
		want bool
	}{
		{"EmptyAnd", And(), true},
		{"NestedEmptyAnd", And(And()), true},
		{"AndOfEmptyAnds", And(And(), And()), true},
		{"OrOfEmptyAnd", Or(And()), true},
		{"AndOfEmptyOr", And(Or()), false},
		{"OrWithEmptyAnd", Or(a, And()), true},
		{"EmptyOr", Or(), false},
		{"Comparison", a, false},
		{"AndWithComparison", And(And(), a), false},
		{"EmptyNotIn", NotIn[int64](age), false},
		{"Nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alwaysTrue(tc.p); got != tc.want {
				t.Errorf("alwaysTrue = %v, want %v", got, tc.want)
			}
		})
	}
}
