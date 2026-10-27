table "posts" {
  schema = schema.public
  column "id" {
    null = false
    type = bigint
    identity {
      generated = BY_DEFAULT
    }
  }
  column "user_id" {
    null = false
    type = bigint
  }
  column "title" {
    null = false
    type = text
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "posts_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}
table "users" {
  schema = schema.public
  column "id" {
    null = false
    type = bigserial
  }
  column "email" {
    null = false
    type = character_varying(255)
  }
  column "role" {
    null    = false
    type    = enum.user_role
    default = "member"
  }
  column "from" {
    null = true
    type = text
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  unique "users_email_key" {
    columns = [column.email]
  }
}
enum "user_role" {
  schema = schema.public
  values = ["admin", "member", "guest"]
}
schema "public" {
  comment = "standard public schema"
}
