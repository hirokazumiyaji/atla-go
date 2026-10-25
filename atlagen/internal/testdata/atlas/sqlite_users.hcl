table "users" {
  schema = schema.main
  column "id" {
    null           = true
    type           = integer
    auto_increment = true
  }
  column "email" {
    null = false
    type = text
  }
  column "type" {
    null = true
    type = text
  }
  column "created_at" {
    null    = false
    type    = datetime
    default = sql("CURRENT_TIMESTAMP")
  }
  primary_key {
    columns = [column.id]
  }
  index "sqlite_autoindex_users_1" {
    unique  = true
    columns = [column.email]
  }
}
schema "main" {
}
