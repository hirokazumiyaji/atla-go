table "things" {
  schema = schema.main
  column "id" {
    null = true
    type = integer
  }
  column "created_at" {
    null = false
    type = datetime
  }
  column "active" {
    null = false
    type = boolean
  }
  column "code" {
    null = false
    type = varchar(10)
  }
  column "rank" {
    null = false
    type = mediumint
  }
  column "price" {
    null = false
    type = numeric(10,2)
  }
  column "extra" {
    null = true
    type = sql("FOO")
  }
  primary_key {
    columns = [column.id]
  }
}
table "pairs" {
  schema = schema.main
  column "a" {
    null = false
    type = integer
  }
  column "b" {
    null = false
    type = integer
  }
  primary_key {
    columns = [column.a, column.b]
  }
}
table "codes" {
  schema = schema.main
  column "id" {
    null = false
    type = bigint
  }
  column "label" {
    null = false
    type = text
  }
  column "label_upper" {
    null = false
    type = text
    as {
      expr = "(upper(label))"
      type = STORED
    }
  }
  primary_key {
    columns = [column.id]
  }
}
table "kv" {
  schema = schema.main
  column "id" {
    null = false
    type = integer
  }
  column "v" {
    null = true
    type = blob
  }
  primary_key {
    columns = [column.id]
  }
  without_rowid = true
}
schema "main" {
}
