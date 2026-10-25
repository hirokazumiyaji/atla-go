// Package ir defines the internal representation that the atlagen pipeline
// builds from an Atlas schema. The parser fills the database facts; the naming
// and mapper stages fill the Go-facing fields.
package ir

// Schema is one database schema selected from the Atlas inspect HCL.
type Schema struct {
	Name    string  `json:"name"`
	Dialect string  `json:"dialect"`
	Enums   []Enum  `json:"enums"`
	Tables  []Table `json:"tables"` // sorted by DBName
}

// Enum is a schema-level enum type.
type Enum struct {
	DBName string      `json:"db_name"`
	GoName string      `json:"go_name"`
	Values []EnumValue `json:"values"`
}

// EnumValue is one value of an Enum.
type EnumValue struct {
	DB string `json:"db"`
	Go string `json:"go"`
}

// Table is one table of the schema.
type Table struct {
	DBName      string       `json:"db_name"`
	Model       string       `json:"model"`
	Package     string       `json:"package"`
	Columns     []Column     `json:"columns"`     // Atlas order
	PrimaryKey  []string     `json:"primary_key"` // column DB names; may be empty or composite
	Uniques     [][]string   `json:"uniques"`     // plain-column unique indexes, index order
	ForeignKeys []ForeignKey `json:"foreign_keys"`
}

// Column is one column of a table.
type Column struct {
	DBName     string     `json:"db_name"`
	GoName     string     `json:"go_name"`
	RawType    string     `json:"raw_type"`
	Enum       string     `json:"enum"` // DB name of the schema enum this column uses, or ""
	GoType     GoType     `json:"go_type"`
	Kind       ColumnKind `json:"kind"`
	Nullable   bool       `json:"nullable"`
	HasDefault bool       `json:"has_default"`
	Generated  bool       `json:"generated"` // a generated (computed) column; never written
	InPK       bool       `json:"in_pk"`
	Scan       ScanPlan   `json:"scan"`
}

// GoType is the Go type of a column and the package that declares it.
type GoType struct {
	Name       string `json:"name"`        // "time.Time", "int64", "enum.UserRole"
	ImportPath string `json:"import_path"` // "time", "", "<pkg>/enum"
}

// ColumnKind names the atla column descriptor type of a column.
type ColumnKind string

// Column kinds. The Null variants are used for nullable columns.
const (
	KindColumn     ColumnKind = "Column"
	KindNumber     ColumnKind = "NumberColumn"
	KindString     ColumnKind = "StringColumn"
	KindNullColumn ColumnKind = "NullColumn"
	KindNullNumber ColumnKind = "NullNumberColumn"
	KindNullString ColumnKind = "NullStringColumn"
)

// ScanPlan says how the generated scanner reads a column.
type ScanPlan string

// Scan plans (generator-spec §4.4).
const (
	ScanDirect    ScanPlan = "direct"
	ScanWiden     ScanPlan = "widen"
	ScanNullPtr   ScanPlan = "nullptr"
	ScanNullWiden ScanPlan = "nullwiden"
)

// ForeignKey is a foreign-key constraint declared on a table.
//
// RefSchema and RefExternal locate the referenced table:
//   - RefSchema "" and RefExternal false: the table is in the selected schema.
//   - RefSchema "<s>": the table is in schema <s> of the inspected document.
//   - RefExternal true (RefSchema ""): the table is not present in the
//     inspected document, e.g. an FK into a schema that was not inspected.
//     P4 skips the edge with a warning.
type ForeignKey struct {
	Symbol      string   `json:"symbol"`
	Columns     []string `json:"columns"`
	RefSchema   string   `json:"ref_schema"`
	RefTable    string   `json:"ref_table"`
	RefColumns  []string `json:"ref_columns"`
	RefExternal bool     `json:"ref_external"`
}

// RequiredForInsert reports whether an INSERT must set the column: it is
// NOT NULL, has no default and is not generated.
func (c Column) RequiredForInsert() bool {
	return !c.Nullable && !c.HasDefault && !c.Generated
}

// Column returns the column with the given DB name.
func (t Table) Column(dbName string) (Column, bool) {
	for _, c := range t.Columns {
		if c.DBName == dbName {
			return c, true
		}
	}
	return Column{}, false
}
