table "logs" {
  schema = schema.audit
  column "id" {
    null = false
    type = bigserial
  }
  column "message" {
    null = false
    type = text
  }
  primary_key {
    columns = [column.id]
  }
}
table "events" {
  schema = schema.public
  column "id" {
    null = false
    type = bigserial
  }
  column "log_id" {
    null = false
    type = bigint
  }
  column "name" {
    null = false
    type = text
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "events_log_id_fkey" {
    columns     = [column.log_id]
    ref_columns = [table.logs.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}
schema "audit" {
}
schema "public" {
  comment = "standard public schema"
}
