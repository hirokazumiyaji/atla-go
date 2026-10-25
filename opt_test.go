package atla_test

import (
	"reflect"
	"testing"

	atla "github.com/hirokazumiyaji/atla-go"
)

func TestOpt(t *testing.T) {
	t.Run("Zero", func(t *testing.T) {
		var o atla.Opt[int64]
		if o.IsSet() {
			t.Error("zero Opt: IsSet() = true")
		}
		if v, ok := o.Get(); v != 0 || ok {
			t.Errorf("zero Opt: Get() = %v, %v; want 0, false", v, ok)
		}
		cols, vals := o.AppendTo([]string{`"name"`}, []any{"alice"}, `"age"`)
		if !reflect.DeepEqual(cols, []string{`"name"`}) || !reflect.DeepEqual(vals, []any{"alice"}) {
			t.Errorf("zero Opt: AppendTo = %q, %#v; want unchanged", cols, vals)
		}
	})
	t.Run("Some", func(t *testing.T) {
		o := atla.Some(int64(3))
		if !o.IsSet() {
			t.Error("IsSet() = false")
		}
		if v, ok := o.Get(); v != 3 || !ok {
			t.Errorf("Get() = %v, %v; want 3, true", v, ok)
		}
		cols, vals := o.AppendTo(nil, nil, `"age"`)
		if !reflect.DeepEqual(cols, []string{`"age"`}) || !reflect.DeepEqual(vals, []any{int64(3)}) {
			t.Errorf("AppendTo = %q, %#v", cols, vals)
		}
	})
	t.Run("SomePtr", func(t *testing.T) {
		o := atla.SomePtr("x")
		v, ok := o.Get()
		if !ok || v == nil || *v != "x" {
			t.Fatalf("Get() = %v, %v; want pointer to \"x\", true", v, ok)
		}
		_, vals := o.AppendTo(nil, nil, `"bio"`)
		if p, ok := vals[0].(*string); !ok || p == nil || *p != "x" {
			t.Errorf("AppendTo value = %#v; want *string to \"x\"", vals[0])
		}
	})
	t.Run("Null", func(t *testing.T) {
		o := atla.Null[string]()
		if !o.IsSet() {
			t.Error("IsSet() = false")
		}
		v, ok := o.Get()
		if v != nil || !ok {
			t.Errorf("Get() = %v, %v; want nil, true", v, ok)
		}
		cols, vals := o.AppendTo(nil, nil, `"bio"`)
		if len(cols) != 1 || len(vals) != 1 {
			t.Fatalf("AppendTo = %q, %#v; want one column", cols, vals)
		}
		if p, ok := vals[0].(*string); !ok || p != nil {
			t.Errorf("AppendTo value = %#v; want (*string)(nil)", vals[0])
		}
	})
	t.Run("SetExpr", func(t *testing.T) {
		o := atla.SetExpr(uAge.Add(1))
		if !o.IsSet() {
			t.Error("IsSet() = false")
		}
		if v, ok := o.Get(); v != 0 || ok {
			t.Errorf("Get() = %v, %v; want 0, false", v, ok)
		}
		cols, vals := o.AppendTo(nil, nil, `"age"`)
		if !reflect.DeepEqual(cols, []string{`"age"`}) || len(vals) != 1 {
			t.Fatalf("AppendTo = %q, %#v", cols, vals)
		}
		if _, ok := vals[0].(atla.Expr[int32]); !ok {
			t.Errorf("AppendTo value = %#v; want an atla.Expr[int32]", vals[0])
		}
	})
	t.Run("SetExprPtr", func(t *testing.T) {
		o := atla.SetExprPtr(atla.Raw[string]("upper(?)", "x"))
		if !o.IsSet() {
			t.Error("IsSet() = false")
		}
		if v, ok := o.Get(); v != nil || ok {
			t.Errorf("Get() = %v, %v; want nil, false", v, ok)
		}
		_, vals := o.AppendTo(nil, nil, `"bio"`)
		if _, ok := vals[0].(atla.Expr[*string]); !ok {
			t.Errorf("AppendTo value = %#v; want an atla.Expr[*string]", vals[0])
		}
		// The adapter renders the wrapped expression unchanged.
		cols, vals := o.AppendTo(nil, nil, `"bio"`)
		checkSQL(t, atla.NewInsert[User](nil, userTable, cols, vals),
			`INSERT INTO "users" ("bio") VALUES (upper($1))`, "x")
	})
	t.Run("NilExpr", func(t *testing.T) {
		// A nil expression is bound as NULL instead of panicking.
		cols, vals := atla.SetExpr[int32](nil).AppendTo(nil, nil, `"age"`)
		cols, vals = atla.SetExprPtr[string](nil).AppendTo(cols, vals, `"bio"`)
		checkSQL(t, atla.NewInsert[User](nil, userTable, cols, vals),
			`INSERT INTO "users" ("age", "bio") VALUES ($1, $2)`, nil, nil)
	})
}
