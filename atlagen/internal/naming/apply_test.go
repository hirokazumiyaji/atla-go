package naming

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/parser"
)

func tables(names ...string) []ir.Table {
	out := make([]ir.Table, 0, len(names))
	for _, n := range names {
		out = append(out, ir.Table{DBName: n, Columns: []ir.Column{{DBName: "id"}}})
	}
	return out
}

func findTable(t *testing.T, s *ir.Schema, name string) ir.Table {
	t.Helper()
	for _, tbl := range s.Tables {
		if tbl.DBName == name {
			return tbl
		}
	}
	t.Fatalf("table %q not found", name)
	return ir.Table{}
}

func findColumn(t *testing.T, tbl ir.Table, name string) ir.Column {
	t.Helper()
	c, ok := tbl.Column(name)
	if !ok {
		t.Fatalf("table %q: column %q not found", tbl.DBName, name)
	}
	return c
}

func applyErr(t *testing.T, s *ir.Schema, o Overrides) string {
	t.Helper()
	err := Apply(s, o)
	if err == nil {
		t.Fatal("Apply succeeded, want error")
	}
	return err.Error()
}

func wantContains(t *testing.T, msg string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(msg, sub) {
			t.Errorf("error %q does not contain %q", msg, sub)
		}
	}
}

func TestApplyFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "atlas", "postgres_users.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := parser.Parse(data, "postgres", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(s, Overrides{RootPackage: "dbgen"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	users := findTable(t, s, "users")
	if users.Model != "User" || users.Package != "user" {
		t.Errorf("users: Model/Package = %q/%q, want User/user", users.Model, users.Package)
	}
	posts := findTable(t, s, "posts")
	if posts.Model != "Post" || posts.Package != "post" {
		t.Errorf("posts: Model/Package = %q/%q, want Post/post", posts.Model, posts.Package)
	}
	for col, want := range map[string]string{"id": "ID", "created_at": "CreatedAt", "from": "From", "role": "Role"} {
		if got := findColumn(t, users, col).GoName; got != want {
			t.Errorf("users.%s GoName = %q, want %q", col, got, want)
		}
	}
	if got := findColumn(t, posts, "user_id").GoName; got != "UserID" {
		t.Errorf("posts.user_id GoName = %q, want UserID", got)
	}
	if len(s.Enums) != 1 {
		t.Fatalf("enums = %d, want 1", len(s.Enums))
	}
	e := s.Enums[0]
	if e.GoName != "UserRole" {
		t.Errorf("enum GoName = %q, want UserRole", e.GoName)
	}
	wantValues := []ir.EnumValue{
		{DB: "admin", Go: "UserRoleAdmin"},
		{DB: "member", Go: "UserRoleMember"},
		{DB: "guest", Go: "UserRoleGuest"},
	}
	if len(e.Values) != len(wantValues) {
		t.Fatalf("enum values = %#v, want %#v", e.Values, wantValues)
	}
	for i, v := range wantValues {
		if e.Values[i] != v {
			t.Errorf("enum value %d = %#v, want %#v", i, e.Values[i], v)
		}
	}
}

func TestApply(t *testing.T) {
	s := &ir.Schema{
		Name: "public",
		Enums: []ir.Enum{
			{DBName: "user_role", Values: []ir.EnumValue{{DB: "admin"}, {DB: ""}, {DB: "in-progress"}}},
			{DBName: "mood"},
		},
		Tables: []ir.Table{
			{DBName: "api_keys", Columns: []ir.Column{{DBName: "id"}, {DBName: "user_id"}}},
			{DBName: "people", Columns: []ir.Column{{DBName: "id"}}},
			{DBName: "user_profiles", Columns: []ir.Column{{DBName: "id"}, {DBName: "edges"}, {DBName: "avatar_url"}}},
			{DBName: "2fa_codes", Columns: []ir.Column{{DBName: "id"}}},
			{DBName: "types", Columns: []ir.Column{{DBName: "id"}}},
		},
	}
	o := Overrides{Tables: map[string]string{"people": "Person"}, RootPackage: "dbgen"}
	if err := Apply(s, o); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantTables := map[string][2]string{
		"api_keys":      {"APIKey", "apikey"},
		"people":        {"Person", "person"},
		"user_profiles": {"UserProfile", "userprofile"},
		"2fa_codes":     {"X2faCode", "x2facode"},
		"types":         {"Type", "typetbl"},
	}
	for name, want := range wantTables {
		tbl := findTable(t, s, name)
		if tbl.Model != want[0] || tbl.Package != want[1] {
			t.Errorf("%s: Model/Package = %q/%q, want %q/%q", name, tbl.Model, tbl.Package, want[0], want[1])
		}
	}
	up := findTable(t, s, "user_profiles")
	if got := findColumn(t, up, "edges").GoName; got != "EdgesCol" {
		t.Errorf("edges GoName = %q, want EdgesCol", got)
	}
	if got := findColumn(t, up, "avatar_url").GoName; got != "AvatarURL" {
		t.Errorf("avatar_url GoName = %q, want AvatarURL", got)
	}
	if got := findColumn(t, findTable(t, s, "api_keys"), "user_id").GoName; got != "UserID" {
		t.Errorf("user_id GoName = %q, want UserID", got)
	}

	role := s.Enums[0]
	if role.GoName != "UserRole" {
		t.Errorf("enum GoName = %q, want UserRole", role.GoName)
	}
	wantValues := []string{"UserRoleAdmin", "UserRoleEmpty", "UserRoleInProgress"}
	for i, want := range wantValues {
		if got := role.Values[i].Go; got != want {
			t.Errorf("value %q Go = %q, want %q", role.Values[i].DB, got, want)
		}
	}
	mood := s.Enums[1]
	if mood.GoName != "Mood" || len(mood.Values) != 0 {
		t.Errorf("empty enum = %#v, want GoName Mood and no values", mood)
	}
}

func TestApplyCollisions(t *testing.T) {
	t.Run("same model", func(t *testing.T) {
		msg := applyErr(t, &ir.Schema{Tables: tables("user", "users")}, Overrides{})
		wantContains(t, msg, `"user"`, `"users"`, `"User"`, "names.tables")
		if n := strings.Count(msg, "\n") + 1; n != 1 {
			t.Errorf("got %d errors, want 1 (no duplicate reports for derived names):\n%s", n, msg)
		}
	})
	t.Run("model clashes with derived type", func(t *testing.T) {
		msg := applyErr(t, &ir.Schema{Tables: tables("user", "user_insert")}, Overrides{})
		wantContains(t, msg, `"UserInsert"`, `"user_insert"`, `"user"`, "insert type", "names.tables")
	})
	t.Run("reserved root identifier", func(t *testing.T) {
		msg := applyErr(t, &ir.Schema{Tables: tables("client")}, Overrides{})
		wantContains(t, msg, `"Client"`, `"client"`, "reserved")
	})
	t.Run("column go names", func(t *testing.T) {
		s := &ir.Schema{Tables: []ir.Table{{DBName: "users", Columns: []ir.Column{{DBName: "user_id"}, {DBName: "userId"}}}}}
		msg := applyErr(t, s, Overrides{})
		wantContains(t, msg, `table "users"`, `"user_id"`, `"userId"`, `"UserID"`)
	})
	t.Run("package", func(t *testing.T) {
		msg := applyErr(t, &ir.Schema{Tables: tables("user_profiles", "userprofiles")}, Overrides{})
		wantContains(t, msg, `"user_profiles"`, `"userprofiles"`, `package "userprofile"`)
	})
	t.Run("enum identifiers", func(t *testing.T) {
		s := &ir.Schema{
			Enums: []ir.Enum{
				{DBName: "user_role", Values: []ir.EnumValue{{DB: "admin"}}},
				{DBName: "user_role_admin"},
			},
			Tables: tables("users"),
		}
		msg := applyErr(t, s, Overrides{})
		wantContains(t, msg, `"UserRoleAdmin"`, `enum "user_role"`, `enum "user_role_admin"`, `"admin"`)
	})
	t.Run("enum values within one enum", func(t *testing.T) {
		s := &ir.Schema{
			Enums:  []ir.Enum{{DBName: "status", Values: []ir.EnumValue{{DB: "in-progress"}, {DB: "in_progress"}}}},
			Tables: tables("users"),
		}
		msg := applyErr(t, s, Overrides{})
		wantContains(t, msg, `"StatusInProgress"`, `"in-progress"`, `"in_progress"`)
	})
	t.Run("enum value clashes with values function", func(t *testing.T) {
		s := &ir.Schema{
			Enums:  []ir.Enum{{DBName: "user_role", Values: []ir.EnumValue{{DB: "values"}}}},
			Tables: tables("users"),
		}
		msg := applyErr(t, s, Overrides{})
		wantContains(t, msg, `"UserRoleValues"`, "values function", `"values"`)
	})
	t.Run("two enums with one type name", func(t *testing.T) {
		s := &ir.Schema{
			Enums:  []ir.Enum{{DBName: "user_role", Values: []ir.EnumValue{{DB: "a"}}}, {DBName: "UserRole", Values: []ir.EnumValue{{DB: "a"}}}},
			Tables: tables("users"),
		}
		msg := applyErr(t, s, Overrides{})
		wantContains(t, msg, `"user_role"`, `"UserRole"`)
		if n := strings.Count(msg, "\n") + 1; n != 1 {
			t.Errorf("got %d errors, want 1:\n%s", n, msg)
		}
	})
	t.Run("unknown override key", func(t *testing.T) {
		msg := applyErr(t, &ir.Schema{Tables: tables("users")}, Overrides{Tables: map[string]string{"nope": "Nope"}})
		wantContains(t, msg, "names.tables", `"nope"`)
	})
	t.Run("unexported override value", func(t *testing.T) {
		msg := applyErr(t, &ir.Schema{Tables: tables("users")}, Overrides{Tables: map[string]string{"users": "lower"}})
		wantContains(t, msg, "names.tables", `"users"`, `"lower"`)
	})
	t.Run("override resolves clash", func(t *testing.T) {
		s := &ir.Schema{Tables: tables("user", "users")}
		if err := Apply(s, Overrides{Tables: map[string]string{"user": "LegacyUser"}}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	})
	t.Run("no go identifier", func(t *testing.T) {
		s := &ir.Schema{Tables: []ir.Table{{DBName: "--", Columns: []ir.Column{{DBName: "id"}, {DBName: "é"}}}}}
		msg := applyErr(t, s, Overrides{})
		wantContains(t, msg, `table "--"`, `column "é"`)
	})
	t.Run("all clashes reported in deterministic order", func(t *testing.T) {
		build := func() *ir.Schema {
			return &ir.Schema{
				Tables: []ir.Table{
					{DBName: "client", Columns: []ir.Column{{DBName: "id"}}},
					{DBName: "user", Columns: []ir.Column{{DBName: "a_b"}, {DBName: "aB"}}},
					{DBName: "user_insert", Columns: []ir.Column{{DBName: "id"}}},
				},
			}
		}
		first := applyErr(t, build(), Overrides{Tables: map[string]string{"zz": "Zz", "aa": "Aa"}})
		lines := strings.Split(first, "\n")
		if len(lines) != 5 {
			t.Fatalf("got %d errors, want 5:\n%s", len(lines), first)
		}
		wantContains(t, lines[0], `"aa"`)
		wantContains(t, lines[1], `"zz"`)
		for i := 0; i < 5; i++ {
			if again := applyErr(t, build(), Overrides{Tables: map[string]string{"zz": "Zz", "aa": "Aa"}}); again != first {
				t.Fatalf("error differs between runs:\n%s\n---\n%s", first, again)
			}
		}
	})
}
