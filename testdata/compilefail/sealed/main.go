package main

import "github.com/hirokazumiyaji/atla-go"

func main() {
	userID := atla.NewNumberColumn[int64]("\"users\"", "\"id\"")
	age := atla.NewNumberColumn[int64]("\"users\"", "\"age\"")
	name := atla.NewStringColumn[string]("\"users\"", "\"name\"")
	editorID := atla.NewNullNumberColumn[int64]("\"users\"", "\"editor_id\"")
	_, _, _, _ = userID, age, name, editorID
	type my struct{}
	var _ atla.Pred = my{}
}
