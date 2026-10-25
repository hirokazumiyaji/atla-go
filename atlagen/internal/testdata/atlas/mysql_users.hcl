table "categories" {
  schema = schema.app
  column "id" {
    null           = false
    type           = int
    auto_increment = true
  }
  column "name" {
    null = false
    type = varchar(100)
  }
  primary_key {
    columns = [column.id]
  }
}
table "users" {
  schema = schema.app
  column "id" {
    null           = false
    type           = bigint
    auto_increment = true
  }
  column "email" {
    null = false
    type = varchar(255)
  }
  column "status" {
    null    = false
    type    = enum("active","inactive","banned")
    default = "active"
  }
  column "class" {
    null = true
    type = varchar(64)
  }
  primary_key {
    columns = [column.id]
  }
  index "idx_users_email" {
    unique  = true
    columns = [column.email]
  }
}
schema "app" {
  charset = "utf8mb4"
  collate = "utf8mb4_0900_ai_ci"
}
