// Package atla is the zero-dependency runtime of atla-go, a typed SQL builder
// for Go that replaces ent in projects whose schema is managed by Atlas.
//
// The schema lives in Atlas and Go code is generated from it ahead of time, so
// queries are type-checked by the compiler and the hot read path performs no
// reflection. Execution goes through database/sql with context.Context first
// and error last, and the package never depends on a specific database driver.
package atla
