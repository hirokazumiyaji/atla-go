# atla-go

**Compile-time type-safe SQL toolkit & code generator for Go, powered by [Atlas](https://atlasgo.io/).**

`atla-go` is the Go sibling of [atla](https://github.com/hirokazumiyaji/atla). Atlas
owns the schema and migrations. `atlagen` reads the HCL that
`atlas schema inspect -u <url>` prints by default (without `--format`) and generates
typed models, column descriptors, query builders, and ent-style edge eager loading
from it; JSON input is rejected. The runtime package `atla` is standard-library only
and runs on `database/sql`.

> Status: P0–P2 implemented; P3 and later are in progress. See the documents below.

```go
users, err := client.User.Query().
    Where(user.Age.GTE(20), user.DeletedAt.IsNull()).
    OrderBy(user.CreatedAt.Desc()).
    Limit(10).
    WithPosts().
    All(ctx) // []*dbgen.User, error

m, err := client.Membership.Get(ctx, orgID, userID) // composite primary keys are first-class
```

## Documentation

- [System Architecture & Design Principles](docs/architecture.md)
- [`atla` Runtime Core Specification](docs/runtime-spec.md)
- [`atlagen` Code Generator Specification](docs/generator-spec.md)
- [Implementation Roadmap](docs/roadmap.md)
