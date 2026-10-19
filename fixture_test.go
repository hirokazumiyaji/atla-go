package atla_test

import (
	"database/sql"

	atla "github.com/hirokazumiyaji/atla-go"
	"github.com/hirokazumiyaji/atla-go/dialect"
)

// This file is a hand-written stand-in for generated code: it describes the
// users and posts tables using only the exported API, the way atlagen output
// does. Tests in package atla_test share it.

// User is the model of the users table.
type User struct {
	ID   int64
	Name string
	Age  int32   // widen plan: scanned through an int64 local
	Bio  *string // nullptr plan: scanned through sql.Null[string]
}

// Post is the model of the posts table.
type Post struct {
	ID       int64
	AuthorID int64
	Title    string
}

// userTable and postTable are the Postgres tables.
var (
	userTable = newUserTable(dialect.Postgres, "")
	postTable = newPostTable(dialect.Postgres)
)

// sqliteUserTable is the users table for SQLite.
func sqliteUserTable() atla.Table[User] { return newUserTable(dialect.SQLite, "") }

// Columns of users and posts, qualified by the base table name.
var (
	uID   = atla.NewNumberColumn[int64](`"users"`, `"id"`)
	uName = atla.NewStringColumn[string](`"users"`, `"name"`)
	uAge  = atla.NewNumberColumn[int32](`"users"`, `"age"`)
	uBio  = atla.NewNullStringColumn[string](`"users"`, `"bio"`)

	pID       = atla.NewNumberColumn[int64](`"posts"`, `"id"`)
	pAuthorID = atla.NewNumberColumn[int64](`"posts"`, `"author_id"`)
	pTitle    = atla.NewStringColumn[string](`"posts"`, `"title"`)
)

// newUserTable returns the users table for d. A non-empty alias names the
// FROM item and qualifies the select list.
func newUserTable(d dialect.Dialect, alias string) atla.Table[User] {
	ident := string(d.AppendIdent(nil, "users"))
	ref, aliasName := ident, ""
	if alias != "" {
		aliasName = string(d.AppendIdent(nil, alias))
		ref = aliasName
	}
	return atla.Table[User]{
		Dialect:       d,
		Ident:         ident,
		RawName:       "users",
		AliasName:     aliasName,
		SelectList:    ref + `."id", ` + ref + `."name", ` + ref + `."age", ` + ref + `."bio"`,
		ReturningList: `"id", "name", "age", "bio"`,
		NumCols:       4,
		Scan:          scanUser,
		Alias:         func(alias string) atla.Table[User] { return newUserTable(d, alias) },
	}
}

// scanUser reads one users row with the generator's scan plans: age is
// widened through an int64 local, and bio is read through sql.Null[string]
// and only allocates when it is not NULL.
func scanUser(rows *sql.Rows) (*User, error) {
	var (
		u   User
		age int64
		bio sql.Null[string]
	)
	if err := rows.Scan(&u.ID, &u.Name, &age, &bio); err != nil {
		return nil, err
	}
	u.Age = int32(age)
	if bio.Valid {
		v := bio.V
		u.Bio = &v
	}
	return &u, nil
}

// newPostTable returns the posts table for d.
func newPostTable(d dialect.Dialect) atla.Table[Post] {
	return atla.Table[Post]{
		Dialect:       d,
		Ident:         string(d.AppendIdent(nil, "posts")),
		RawName:       "posts",
		SelectList:    `"posts"."id", "posts"."author_id", "posts"."title"`,
		ReturningList: `"id", "author_id", "title"`,
		NumCols:       3,
		Scan:          scanPost,
	}
}

func scanPost(rows *sql.Rows) (*Post, error) {
	var p Post
	if err := rows.Scan(&p.ID, &p.AuthorID, &p.Title); err != nil {
		return nil, err
	}
	return &p, nil
}
