// Command fxgen marshals hand-built schema.Realm values (shaped the way
// atlas's Postgres/MySQL inspectors build them) with atlas's own HCL
// marshallers, to produce faithful `atlas schema inspect` HCL fixtures.
package main

import (
	"fmt"
	"os"

	"ariga.io/atlas/sql/mysql"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
)

func p6() *int { v := 6; return &v }

func col(name string, t schema.Type) *schema.Column { return schema.NewColumn(name).SetType(t) }
func text() schema.Type                             { return &schema.StringType{T: "text"} }
func bigint() schema.Type                           { return &schema.IntegerType{T: "bigint"} }
func bigserial() schema.Type                        { return &postgres.SerialType{T: "bigserial"} }
func tstz() schema.Type                             { return &schema.TimeType{T: "timestamp with time zone", Precision: p6()} }

func publicSchema() *schema.Schema { return schema.New("public").SetComment("standard public schema") }

func fk(sym string, t *schema.Table, cols []*schema.Column, rt *schema.Table, rcols []*schema.Column) *schema.ForeignKey {
	return schema.NewForeignKey(sym).SetTable(t).AddColumns(cols...).SetRefTable(rt).AddRefColumns(rcols...).
		SetOnUpdate(schema.NoAction).SetOnDelete(schema.NoAction)
}

func pgUsers() *schema.Realm {
	s := publicSchema()
	role := &schema.EnumType{T: "user_role", Values: []string{"admin", "member", "guest"}, Schema: s}
	uid := col("id", bigserial())
	email := col("email", &schema.StringType{T: "character varying", Size: 255})
	users := schema.NewTable("users").AddColumns(
		uid, email,
		col("role", role).SetDefault(&schema.Literal{V: "'member'"}),
		col("from", text()).SetNull(true),
		col("created_at", tstz()).SetDefault(&schema.RawExpr{X: "now()"}),
	)
	users.SetPrimaryKey(schema.NewPrimaryKey(uid))
	users.AddIndexes(schema.NewUniqueIndex("users_email_key").AddColumns(email).AddAttrs(postgres.UniqueConstraint("users_email_key")))
	pid := col("id", bigint()).AddAttrs(&postgres.Identity{Generation: "BY DEFAULT", Sequence: &postgres.Sequence{Start: 1, Increment: 1}})
	puid := col("user_id", bigint())
	posts := schema.NewTable("posts").AddColumns(pid, puid, col("title", text()))
	posts.SetPrimaryKey(schema.NewPrimaryKey(pid))
	posts.AddForeignKeys(fk("posts_user_id_fkey", posts, []*schema.Column{puid}, users, []*schema.Column{uid}))
	s.AddTables(posts, users) // inspect orders tables by name
	s.AddObjects(role)
	return schema.NewRealm(s)
}

func pgTypecheck() *schema.Realm {
	s := publicSchema()
	id := col("id", bigserial())
	email := col("email", text())
	users := schema.NewTable("users").AddColumns(id, col("name", text()), email, col("age", &schema.IntegerType{T: "integer"}))
	users.SetPrimaryKey(schema.NewPrimaryKey(id))
	users.AddIndexes(schema.NewUniqueIndex("users_email_key").AddColumns(email))
	s.AddTables(users)
	return schema.NewRealm(s)
}

func pgComposite() *schema.Realm {
	s := publicSchema()
	oid := col("id", bigserial())
	orgs := schema.NewTable("orgs").AddColumns(oid, col("name", text()))
	orgs.SetPrimaryKey(schema.NewPrimaryKey(oid))
	uid := col("id", bigserial())
	users := schema.NewTable("users").AddColumns(uid, col("email", text()))
	users.SetPrimaryKey(schema.NewPrimaryKey(uid))

	mOrg, mUser, slug := col("org_id", bigint()), col("user_id", bigint()), col("slug", text())
	role := col("role", text()).SetDefault(&schema.Literal{V: "'member'"})
	m := schema.NewTable("memberships").AddColumns(mOrg, mUser, slug, role, col("deleted_at", tstz()).SetNull(true))
	m.SetPrimaryKey(schema.NewPrimaryKey(mOrg, mUser))
	m.AddIndexes(
		schema.NewUniqueIndex("memberships_org_id_slug_key").AddColumns(mOrg, slug),
		schema.NewUniqueIndex("memberships_org_id_lower_slug_key").AddParts(schema.NewColumnPart(mOrg), schema.NewIndexPart().SetExpr(&schema.RawExpr{X: "lower(slug)"})),
		schema.NewUniqueIndex("memberships_user_id_active_key").AddColumns(mUser).AddAttrs(&postgres.IndexPredicate{P: "(deleted_at IS NULL)"}),
		schema.NewIndex("memberships_role_idx").AddColumns(role),
	)
	m.AddForeignKeys(
		fk("memberships_org_id_fkey", m, []*schema.Column{mOrg}, orgs, []*schema.Column{oid}),
		fk("memberships_user_id_fkey", m, []*schema.Column{mUser}, users, []*schema.Column{uid}),
	)
	nid, nOrg, nUser := col("id", bigserial()), col("org_id", bigint()), col("user_id", bigint())
	notes := schema.NewTable("membership_notes").AddColumns(nid, nOrg, nUser, col("body", text()))
	notes.SetPrimaryKey(schema.NewPrimaryKey(nid))
	notes.AddForeignKeys(fk("membership_notes_membership_fkey", notes, []*schema.Column{nOrg, nUser}, m, []*schema.Column{mOrg, mUser}))
	s.AddTables(notes, m, orgs, users)
	return schema.NewRealm(s)
}

func pgTypes() *schema.Realm {
	s := publicSchema()
	role := &schema.EnumType{T: "user_role", Values: []string{"admin", "member", "guest"}, Schema: s}
	pk := col("c_bigserial", bigserial())
	t := schema.NewTable("t").AddColumns(
		col("c_smallint", &schema.IntegerType{T: "smallint"}),
		col("c_int2", &schema.IntegerType{T: "int2"}),
		col("c_smallserial", &postgres.SerialType{T: "smallserial"}),
		col("c_integer", &schema.IntegerType{T: "integer"}),
		col("c_int", &schema.IntegerType{T: "int"}),
		col("c_int4", &schema.IntegerType{T: "int4"}),
		col("c_serial", &postgres.SerialType{T: "serial"}),
		col("c_bigint", bigint()),
		col("c_int8", &schema.IntegerType{T: "int8"}),
		pk,
		col("c_real", &schema.FloatType{T: "real"}),
		col("c_float4", &schema.FloatType{T: "float4"}),
		col("c_double_precision", &schema.FloatType{T: "double precision"}),
		col("c_float8", &schema.FloatType{T: "float8"}),
		col("c_numeric", &schema.DecimalType{T: "numeric"}),
		col("c_decimal", &schema.DecimalType{T: "decimal"}),
		col("c_money", &postgres.CurrencyType{T: "money"}),
		col("c_boolean", &schema.BoolType{T: "boolean"}),
		col("c_text", text()),
		col("c_varchar", &schema.StringType{T: "varchar"}),
		col("c_char", &schema.StringType{T: "char"}),
		col("c_citext", &postgres.UserDefinedType{T: "citext"}),
		col("c_inet", &postgres.NetworkType{T: "inet"}),
		col("c_cidr", &postgres.NetworkType{T: "cidr"}),
		col("c_macaddr", &postgres.NetworkType{T: "macaddr"}),
		col("c_bytea", &schema.BinaryType{T: "bytea"}),
		col("c_uuid", &schema.UUIDType{T: "uuid"}),
		col("c_date", &schema.TimeType{T: "date"}),
		col("c_timestamp", &schema.TimeType{T: "timestamp without time zone", Precision: p6()}),
		col("c_timestamptz", tstz()),
		col("c_time", &schema.TimeType{T: "time without time zone", Precision: p6()}),
		col("c_timetz", &schema.TimeType{T: "time with time zone", Precision: p6()}),
		col("c_interval", &postgres.IntervalType{T: "interval", Precision: p6()}),
		col("c_json", &schema.JSONType{T: "json"}),
		col("c_jsonb", &schema.JSONType{T: "jsonb"}),
		col("tags", &postgres.ArrayType{Type: text(), T: "text[]"}),
		col("role", role),
		col("nullable_int", &schema.IntegerType{T: "integer"}).SetNull(true),
	)
	t.SetPrimaryKey(schema.NewPrimaryKey(pk))
	s.AddTables(t)
	s.AddObjects(role)
	return schema.NewRealm(s)
}

func pgMulti() *schema.Realm {
	audit := schema.New("audit")
	lid := col("id", bigserial())
	logs := schema.NewTable("logs").AddColumns(lid, col("message", text()))
	logs.SetPrimaryKey(schema.NewPrimaryKey(lid))
	audit.AddTables(logs)

	pub := publicSchema()
	eid, logID := col("id", bigserial()), col("log_id", bigint())
	events := schema.NewTable("events").AddColumns(eid, logID, col("name", text()))
	events.SetPrimaryKey(schema.NewPrimaryKey(eid))
	events.AddForeignKeys(fk("events_log_id_fkey", events, []*schema.Column{logID}, logs, []*schema.Column{lid}))
	pub.AddTables(events)
	return schema.NewRealm(audit, pub) // inspect orders schemas by name
}

func myUsers() *schema.Realm {
	s := schema.New("app").AddAttrs(&schema.Charset{V: "utf8mb4"}, &schema.Collation{V: "utf8mb4_0900_ai_ci"})
	cs := []schema.Attr{&schema.Charset{V: "utf8mb4"}, &schema.Collation{V: "utf8mb4_0900_ai_ci"}}
	cid := col("id", &schema.IntegerType{T: "int"}).AddAttrs(&mysql.AutoIncrement{})
	cats := schema.NewTable("categories").AddColumns(cid, col("name", &schema.StringType{T: "varchar", Size: 100}))
	cats.SetPrimaryKey(schema.NewPrimaryKey(cid))
	cats.AddAttrs(cs...)
	uid := col("id", &schema.IntegerType{T: "bigint"}).AddAttrs(&mysql.AutoIncrement{})
	email := col("email", &schema.StringType{T: "varchar", Size: 255})
	users := schema.NewTable("users").AddColumns(
		uid, email,
		col("status", &schema.EnumType{T: "enum", Values: []string{"active", "inactive", "banned"}}).SetDefault(&schema.Literal{V: `"active"`}),
		col("class", &schema.StringType{T: "varchar", Size: 64}).SetNull(true),
	)
	users.SetPrimaryKey(schema.NewPrimaryKey(uid))
	users.AddIndexes(schema.NewUniqueIndex("idx_users_email").AddColumns(email))
	users.AddAttrs(cs...)
	s.AddTables(cats, users)
	return schema.NewRealm(s)
}

func main() {
	out := os.Args[1]
	pg := map[string]*schema.Realm{
		"postgres_users":  pgUsers(),
		"typecheck_users": pgTypecheck(),
		"composite":       pgComposite(),
		"pg_types":        pgTypes(),
		"multi_schema":    pgMulti(),
	}
	for name, r := range pg {
		b, err := postgres.MarshalHCL(r)
		if err != nil {
			fmt.Fprintln(os.Stderr, name, err)
			os.Exit(1)
		}
		os.WriteFile(out+"/"+name+".hcl", b, 0o644)
	}
	b, err := mysql.MarshalHCL(myUsers())
	if err != nil {
		fmt.Fprintln(os.Stderr, "mysql_users", err)
		os.Exit(1)
	}
	os.WriteFile(out+"/mysql_users.hcl", b, 0o644)
}
