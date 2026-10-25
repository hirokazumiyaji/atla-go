table "membership_notes" {
  schema = schema.public
  column "id" {
    null = false
    type = bigserial
  }
  column "org_id" {
    null = false
    type = bigint
  }
  column "user_id" {
    null = false
    type = bigint
  }
  column "body" {
    null = false
    type = text
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "membership_notes_membership_fkey" {
    columns     = [column.org_id, column.user_id]
    ref_columns = [table.memberships.column.org_id, table.memberships.column.user_id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
}
table "memberships" {
  schema = schema.public
  column "org_id" {
    null = false
    type = bigint
  }
  column "user_id" {
    null = false
    type = bigint
  }
  column "slug" {
    null = false
    type = text
  }
  column "role" {
    null    = false
    type    = text
    default = "member"
  }
  column "deleted_at" {
    null = true
    type = timestamptz
  }
  column "slug_lower" {
    null = false
    type = text
    as {
      expr = "lower(slug)"
      type = STORED
    }
  }
  primary_key {
    columns = [column.org_id, column.user_id]
  }
  foreign_key "memberships_org_id_fkey" {
    columns     = [column.org_id]
    ref_columns = [table.orgs.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
  foreign_key "memberships_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_update   = NO_ACTION
    on_delete   = NO_ACTION
  }
  index "memberships_org_id_slug_key" {
    unique  = true
    columns = [column.org_id, column.slug]
  }
  index "memberships_org_id_lower_slug_key" {
    unique = true
    on {
      column = column.org_id
    }
    on {
      expr = "lower(slug)"
    }
  }
  index "memberships_user_id_active_key" {
    unique  = true
    columns = [column.user_id]
    where   = "(deleted_at IS NULL)"
  }
  index "memberships_role_idx" {
    columns = [column.role]
  }
}
table "orgs" {
  schema = schema.public
  column "id" {
    null = false
    type = bigserial
  }
  column "name" {
    null = false
    type = text
  }
  primary_key {
    columns = [column.id]
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
    type = text
  }
  primary_key {
    columns = [column.id]
  }
}
schema "public" {
  comment = "standard public schema"
}
