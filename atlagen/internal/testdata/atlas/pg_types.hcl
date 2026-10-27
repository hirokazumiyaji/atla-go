table "t" {
  schema = schema.public
  column "c_smallint" {
    null = false
    type = smallint
  }
  column "c_int2" {
    null = false
    type = int2
  }
  column "c_smallserial" {
    null = false
    type = smallserial
  }
  column "c_integer" {
    null = false
    type = integer
  }
  column "c_int" {
    null = false
    type = int
  }
  column "c_int4" {
    null = false
    type = int4
  }
  column "c_serial" {
    null = false
    type = serial
  }
  column "c_bigint" {
    null = false
    type = bigint
  }
  column "c_int8" {
    null = false
    type = int8
  }
  column "c_bigserial" {
    null = false
    type = bigserial
  }
  column "c_real" {
    null = false
    type = real
  }
  column "c_float4" {
    null = false
    type = float4
  }
  column "c_double_precision" {
    null = false
    type = double_precision
  }
  column "c_float8" {
    null = false
    type = float8
  }
  column "c_numeric" {
    null = false
    type = numeric
  }
  column "c_decimal" {
    null = false
    type = decimal
  }
  column "c_money" {
    null = false
    type = money
  }
  column "c_boolean" {
    null = false
    type = boolean
  }
  column "c_text" {
    null = false
    type = text
  }
  column "c_varchar" {
    null = false
    type = varchar
  }
  column "c_char" {
    null = false
    type = char
  }
  column "c_citext" {
    null = false
    type = sql("citext")
  }
  column "c_inet" {
    null = false
    type = inet
  }
  column "c_cidr" {
    null = false
    type = cidr
  }
  column "c_macaddr" {
    null = false
    type = macaddr
  }
  column "c_bytea" {
    null = false
    type = bytea
  }
  column "c_uuid" {
    null = false
    type = uuid
  }
  column "c_date" {
    null = false
    type = date
  }
  column "c_timestamp" {
    null = false
    type = timestamp
  }
  column "c_timestamptz" {
    null = false
    type = timestamptz
  }
  column "c_time" {
    null = false
    type = time
  }
  column "c_timetz" {
    null = false
    type = timetz
  }
  column "c_interval" {
    null = false
    type = interval
  }
  column "c_json" {
    null = false
    type = json
  }
  column "c_jsonb" {
    null = false
    type = jsonb
  }
  column "tags" {
    null = false
    type = sql("text[]")
  }
  column "role" {
    null = false
    type = enum.user_role
  }
  column "nullable_int" {
    null = true
    type = integer
  }
  primary_key {
    columns = [column.c_bigserial]
  }
}
enum "user_role" {
  schema = schema.public
  values = ["admin", "member", "guest"]
}
schema "public" {
  comment = "standard public schema"
}
