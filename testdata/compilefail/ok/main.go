package main

import "github.com/hirokazumiyaji/atla-go"

func main() {
	userID := atla.NewNumberColumn[int64]("\"users\"", "\"id\"")
	age := atla.NewNumberColumn[int64]("\"users\"", "\"age\"")
	name := atla.NewStringColumn[string]("\"users\"", "\"name\"")
	plain := atla.NewColumn[bool]("\"users\"", "\"active\"")
	editorID := atla.NewNullNumberColumn[int64]("\"users\"", "\"editor_id\"")
	nick := atla.NewNullStringColumn[string]("\"users\"", "\"nick\"")
	note := atla.NewNullColumn[string]("\"users\"", "\"note\"")

	var _ atla.Pred = atla.And(
		userID.EQ(1),
		age.EQExpr(age.Add(1)),
		name.Like("a%"),
		plain.EQ(true),
		editorID.IsNull(),
		nick.Contains("x"),
		note.IsNotNull(),
		atla.Not(age.EQ(2)),
	)
	var _ atla.Opt[int64] = atla.Some[int64](1)
	var _ = atla.GenVersion1
}
