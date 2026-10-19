package atla

import (
	"database/sql"

	"github.com/hirokazumiyaji/atla-go/dialect"
)

// Table describes a generated table to the runtime. Every field is
// precomputed by the generator. For use by generated code.
type Table[R any] struct {
	// Dialect is the database dialect the table's SQL is written for.
	Dialect dialect.Dialect
	// Ident is the quoted base table name, possibly schema-qualified.
	Ident string
	// RawName is the unquoted table name, reported in QueryInfo and DBError.
	RawName string
	// AliasName is the quoted alias, or "" when the table is not aliased.
	// The FROM item is Ident, or Ident + " AS " + AliasName.
	AliasName string
	// SelectList is the qualified column list, e.g.
	// `"users"."id", "users"."name"`.
	SelectList string
	// ReturningList is the unqualified column list for RETURNING, e.g.
	// `"id", "name"`.
	ReturningList string
	// NumCols is the number of columns in SelectList.
	NumCols int

	// Scan is the single-table fast path: one rows.Scan call into the row.
	Scan func(*sql.Rows) (*R, error)

	// NewRowScanner supports composition. rows.Scan must receive every column
	// of a row in one call, so a JOIN collects the targets of each table into
	// one []any, calls rows.Scan once, and then lets each table finish its row.
	NewRowScanner func() RowScanner[R]

	// NewNullableRowScanner scans every column into sql.Null temporaries and
	// reports absence (all primary-key columns NULL) for the nullable side of
	// a LEFT JOIN. It is nil for tables without a primary key.
	NewNullableRowScanner func() RowScanner[R]

	// Alias returns the table aliased as alias, with SelectList qualified by
	// the alias.
	Alias func(alias string) Table[R]
}

// appendFrom appends the table's FROM item to b.
func (t Table[R]) appendFrom(b []byte) []byte {
	b = append(b, t.Ident...)
	if t.AliasName != "" {
		b = append(b, " AS "...)
		b = append(b, t.AliasName...)
	}
	return b
}

// RowScanner scans one table's share of a result row. For use by generated
// code.
type RowScanner[R any] interface {
	// Targets appends this table's scan destinations to dst.
	Targets(dst []any) []any
	// Finish converts the scanned values into a row. The nullable scanner
	// returns nil, nil when the row is absent.
	Finish() (*R, error)
}
