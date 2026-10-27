// Package parser turns the HCL printed by `atlas schema inspect -u <url>`
// (Atlas's default output format) into an ir.Schema holding the database
// facts only. Go names and Go types are filled by later pipeline stages.
//
// The document is walked as an hclsyntax AST without an evaluation context:
// references such as schema.public, column.id or table.users.column.id are
// read as traversals, and only literal expressions are evaluated.
//
// Type renderings, checked against Atlas's type registries
// (sql/postgres/sqlspec.go, sql/mysql/sqlspec.go, sql/sqlite/sqlspec.go and
// schemahcl.hclType):
//
//   - An identifier is a type name with spaces written as "_"
//     (double_precision, character_varying(255)); "_" is turned back into a
//     space. Call arguments are rendered back comma-joined without spaces.
//   - sql("<t>") is a type Atlas has no spec for (Postgres arrays, citext,
//     SQLite declared types such as FOO); <t> is used verbatim.
//   - enum.<name> (enum.<schema>.<name> when two schemas define the name) is
//     a Postgres enum.
//
// Exceptions to the plain rendering:
//
//   - String call arguments are rendered as SQL literals: the MySQL type
//     enum("a","b") becomes enum('a','b'), as MySQL itself reports it.
//   - Postgres interval types with a field are printed by Atlas without the
//     "interval" keyword (day_to_second, second(3)); for the postgres dialect
//     the keyword is restored ("interval day to second").
//   - A reference to another type object (domain.<name>, composite.<name>)
//     becomes its last name.
package parser

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
)

// filename names the input in HCL diagnostics.
const filename = "schema.hcl"

var serialTypes = map[string]bool{
	"serial":      true,
	"serial2":     true,
	"serial4":     true,
	"serial8":     true,
	"smallserial": true,
	"bigserial":   true,
}

// intervalFields are the Postgres interval types that Atlas prints by their
// field alone (sql/postgres/sqlspec.go, TypeRegistry).
var intervalFields = map[string]bool{
	"second":           true,
	"day to second":    true,
	"hour to second":   true,
	"minute to second": true,
	"year":             true,
	"month":            true,
	"day":              true,
	"hour":             true,
	"minute":           true,
	"year to month":    true,
	"day to hour":      true,
	"day to minute":    true,
	"hour to minute":   true,
}

// object is a top-level table or enum block.
type object struct {
	name   string
	schema string
	body   *hclsyntax.Body
}

type parser struct {
	dialect  string
	selected string
	tables   []object
	enums    []object
}

// Parse reads Atlas inspect HCL and returns the schema named schemaName.
// When schemaName is empty it picks the only schema, else "public", else
// "main", and fails if the choice is ambiguous. Only tables and enums of
// that schema are included. Tables are sorted by DB name; columns, enum
// values, key columns and uniques keep Atlas order.
func Parse(data []byte, dialect, schemaName string) (*ir.Schema, error) {
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '{' {
		return nil, errors.New(`input looks like JSON; atlagen reads Atlas HCL — run "atlas schema inspect" without --format`)
	}
	file, diags := hclsyntax.ParseConfig(data, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("invalid atlas hcl: %s", formatDiags(diags))
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, errors.New("invalid atlas hcl: unexpected body type")
	}

	p := &parser{dialect: dialect}
	var schemas []string
	for _, b := range body.Blocks {
		switch b.Type {
		case "schema":
			if name := blockName(b); name != "" {
				schemas = append(schemas, name)
			}
		case "table":
			t, err := readObject(b, len(p.tables))
			if err != nil {
				return nil, err
			}
			p.tables = append(p.tables, t)
		case "enum":
			e, err := readObject(b, len(p.enums))
			if err != nil {
				return nil, err
			}
			p.enums = append(p.enums, e)
		}
	}
	selected, err := selectSchema(schemas, schemaName)
	if err != nil {
		return nil, err
	}
	p.selected = selected

	s := &ir.Schema{
		Name:    selected,
		Dialect: dialect,
		Enums:   []ir.Enum{},
		Tables:  []ir.Table{},
	}
	seenEnum := make(map[string]bool)
	for _, e := range p.enums {
		if e.schema != selected {
			continue
		}
		if seenEnum[e.name] {
			return nil, fmt.Errorf("duplicate enum %q", e.name)
		}
		seenEnum[e.name] = true
		values, ok := stringList(e.body.Attributes["values"])
		if !ok {
			return nil, fmt.Errorf("enum %q: values must be a list of strings", e.name)
		}
		irValues := make([]ir.EnumValue, 0, len(values))
		for _, v := range values {
			irValues = append(irValues, ir.EnumValue{DB: v})
		}
		s.Enums = append(s.Enums, ir.Enum{DBName: e.name, Values: irValues})
	}

	seenTable := make(map[string]bool)
	for _, tb := range p.tables {
		if tb.schema != selected {
			continue
		}
		t, err := p.parseTable(tb)
		if err != nil {
			return nil, err
		}
		if seenTable[t.DBName] {
			return nil, fmt.Errorf("duplicate table %q", t.DBName)
		}
		seenTable[t.DBName] = true
		s.Tables = append(s.Tables, t)
	}
	if len(s.Tables) == 0 {
		return nil, fmt.Errorf("schema %q has no tables", selected)
	}
	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].DBName < s.Tables[j].DBName })
	return s, nil
}

// selectSchema picks the schema named name. Without a name it picks the only
// schema, else "public", else "main"; any other set of schemas is ambiguous.
func selectSchema(schemas []string, name string) (string, error) {
	if len(schemas) == 0 {
		return "", errors.New("atlas hcl has no schemas")
	}
	available := strings.Join(schemas, ", ")
	if name != "" {
		if slices.Contains(schemas, name) {
			return name, nil
		}
		return "", fmt.Errorf("schema %q not found (available: %s)", name, available)
	}
	if len(schemas) == 1 {
		return schemas[0], nil
	}
	for _, fallback := range []string{"public", "main"} {
		if slices.Contains(schemas, fallback) {
			return fallback, nil
		}
	}
	return "", fmt.Errorf("atlas hcl has several schemas (%s); set schema_name (--schema-name) to pick one", available)
}

// blockName returns the last label of a block: Atlas prints a schema
// qualifier label before the name when two schemas hold the same name.
func blockName(b *hclsyntax.Block) string {
	if len(b.Labels) == 0 {
		return ""
	}
	return b.Labels[len(b.Labels)-1]
}

// readObject reads the name and schema of a table or enum block; index
// counts the blocks of that type.
func readObject(b *hclsyntax.Block, index int) (object, error) {
	name := blockName(b)
	if name == "" {
		return object{}, fmt.Errorf("%s at index %d has no name", b.Type, index)
	}
	schema, err := schemaRef(b.Body)
	if err != nil {
		return object{}, fmt.Errorf("%s %q: %w", b.Type, name, err)
	}
	return object{name: name, schema: schema, body: b.Body}, nil
}

// schemaRef reads the "schema = schema.<name>" attribute of a block.
func schemaRef(body *hclsyntax.Body) (string, error) {
	attr, ok := body.Attributes["schema"]
	if !ok {
		return "", errors.New("missing schema reference")
	}
	names, ok := traversalNames(attr.Expr)
	if !ok || len(names) != 2 || names[0] != "schema" {
		return "", errors.New("schema: expected a reference like schema.<name>")
	}
	return names[1], nil
}

func (p *parser) parseTable(tb object) (ir.Table, error) {
	t := ir.Table{
		DBName:      tb.name,
		Columns:     []ir.Column{},
		PrimaryKey:  []string{},
		Uniques:     [][]string{},
		ForeignKeys: []ir.ForeignKey{},
	}
	wrap := func(err error) error { return fmt.Errorf("table %q: %w", tb.name, err) }

	for _, b := range tb.body.Blocks {
		if b.Type != "primary_key" {
			continue
		}
		cols, ok := keyColumns(b.Body)
		if !ok {
			return ir.Table{}, wrap(errors.New("primary key: unrecognized column reference"))
		}
		t.PrimaryKey = cols
		break
	}
	inPK := make(map[string]bool, len(t.PrimaryKey))
	for _, c := range t.PrimaryKey {
		inPK[c] = true
	}
	withoutRowid, err := boolAttr(tb.body, "without_rowid")
	if err != nil {
		return ir.Table{}, wrap(err)
	}
	// SQLite: a single-column INTEGER PRIMARY KEY is an alias of the rowid,
	// unless the table is WITHOUT ROWID (printed without_rowid = true).
	rowidPK := ""
	if p.dialect == "sqlite" && len(t.PrimaryKey) == 1 && !withoutRowid {
		rowidPK = t.PrimaryKey[0]
	}
	// Primary keys are NOT NULL only where the database enforces it. A SQLite
	// rowid table allows NULL in non-INTEGER and composite primary keys;
	// WITHOUT ROWID tables and the INTEGER PRIMARY KEY rowid alias do not.
	pkNotNull := p.dialect != "sqlite" || withoutRowid

	known := make(map[string]bool)
	columnIndex := 0
	for _, b := range tb.body.Blocks {
		if b.Type != "column" {
			continue
		}
		c, err := p.parseColumn(b, columnIndex, rowidPK, inPK, pkNotNull)
		if err != nil {
			return ir.Table{}, wrap(err)
		}
		columnIndex++
		if known[c.DBName] {
			return ir.Table{}, wrap(fmt.Errorf("duplicate column %q", c.DBName))
		}
		known[c.DBName] = true
		t.Columns = append(t.Columns, c)
	}
	if err := checkColumns("primary key", t.PrimaryKey, known); err != nil {
		return ir.Table{}, wrap(err)
	}

	indexes := map[string]int{}
	fkIndex := 0
	for _, b := range tb.body.Blocks {
		switch b.Type {
		case "index", "unique":
			label := fmt.Sprintf("%s %q", b.Type, blockName(b))
			if blockName(b) == "" {
				label = fmt.Sprintf("unnamed %s %d", b.Type, indexes[b.Type])
			}
			indexes[b.Type]++
			cols, ok, err := uniqueColumns(b)
			if err != nil {
				return ir.Table{}, wrap(fmt.Errorf("%s: %w", label, err))
			}
			if !ok {
				continue
			}
			if err := checkColumns(label, cols, known); err != nil {
				return ir.Table{}, wrap(err)
			}
			t.Uniques = append(t.Uniques, cols)
		case "foreign_key":
			fk, err := p.parseForeignKey(b, fkIndex, tb.name)
			if err != nil {
				return ir.Table{}, wrap(err)
			}
			fkIndex++
			if err := checkColumns(fmt.Sprintf("foreign key %q", fk.Symbol), fk.Columns, known); err != nil {
				return ir.Table{}, wrap(err)
			}
			t.ForeignKeys = append(t.ForeignKeys, fk)
		}
	}
	return t, nil
}

func checkColumns(label string, cols []string, known map[string]bool) error {
	for _, c := range cols {
		if !known[c] {
			return fmt.Errorf("%s: unknown column %q", label, c)
		}
	}
	return nil
}

// parseColumn reads one column block. rowidPK names the table's rowid-alias
// candidate ("" when there is none); pkNotNull reports whether the database
// guarantees that every primary-key column is NOT NULL.
func (p *parser) parseColumn(b *hclsyntax.Block, index int, rowidPK string, inPK map[string]bool, pkNotNull bool) (ir.Column, error) {
	name := blockName(b)
	if name == "" {
		return ir.Column{}, fmt.Errorf("column at index %d has no name", index)
	}
	wrap := func(err error) error { return fmt.Errorf("column %q: %w", name, err) }

	typeAttr, ok := b.Body.Attributes["type"]
	if !ok {
		return ir.Column{}, wrap(errors.New("missing type"))
	}
	rawType, enum, err := p.columnType(typeAttr.Expr)
	if err != nil {
		return ir.Column{}, wrap(err)
	}
	null, err := boolAttr(b.Body, "null")
	if err != nil {
		return ir.Column{}, wrap(err)
	}
	autoIncrement, err := boolAttr(b.Body, "auto_increment")
	if err != nil {
		return ir.Column{}, wrap(err)
	}
	_, hasDefault := b.Body.Attributes["default"]

	c := ir.Column{
		DBName:  name,
		RawType: rawType,
		Enum:    enum,
		InPK:    inPK[name],
	}
	// SQLite prints null = true for an INTEGER PRIMARY KEY even though it is
	// an alias of the rowid and therefore never NULL.
	rowidAlias := rowidPK == name && strings.ToLower(rawType) == "integer"
	// Primary-key columns are NOT NULL only when the database enforces it.
	// Ordinary SQLite rowid tables allow NULL in non-INTEGER and composite
	// primary keys, and Atlas prints null = true for those.
	pkGuaranteedNotNull := c.InPK && (pkNotNull || rowidAlias)
	c.Nullable = null && !pkGuaranteedNotNull
	// Serial types give Postgres only their implicit nextval default.
	c.HasDefault = hasDefault || autoIncrement || hasBlock(b.Body, "identity") ||
		(p.dialect == "postgres" && serialTypes[baseType(rawType)])
	// Atlas prints a generated column as an as { expr = "…" type = STORED }
	// block and also accepts as = "…" (specutil.ConvertGenExpr).
	_, asAttr := b.Body.Attributes["as"]
	c.Generated = asAttr || hasBlock(b.Body, "as")
	if rowidAlias {
		c.HasDefault = true
	}
	return c, nil
}

// columnType renders a column's type expression as a raw type and reports
// the enum it names, if the enum belongs to the selected schema.
func (p *parser) columnType(expr hclsyntax.Expression) (rawType, enum string, err error) {
	unsupported := errors.New("unsupported type expression")
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		names, ok := traversalNames(e)
		if !ok {
			return "", "", unsupported
		}
		if len(names) == 1 {
			return p.typeName(names[0]), "", nil
		}
		if names[0] == "enum" && len(names) <= 3 {
			return p.enumType(names[1:])
		}
		return names[len(names)-1], "", nil
	case *hclsyntax.FunctionCallExpr:
		if e.Name == "sql" {
			if len(e.Args) != 1 {
				return "", "", unsupported
			}
			s, ok := stringLiteral(e.Args[0])
			if !ok {
				return "", "", unsupported
			}
			return s, "", nil
		}
		args := make([]string, 0, len(e.Args))
		for _, a := range e.Args {
			s, ok := typeArg(a)
			if !ok {
				return "", "", unsupported
			}
			args = append(args, s)
		}
		return p.typeName(e.Name) + "(" + strings.Join(args, ",") + ")", "", nil
	}
	return "", "", unsupported
}

// typeName turns an HCL type identifier back into the database type name.
func (p *parser) typeName(ident string) string {
	name := strings.ReplaceAll(ident, "_", " ")
	if p.dialect == "postgres" && intervalFields[name] {
		return "interval " + name
	}
	return name
}

// enumType resolves enum.<name> or enum.<schema>.<name>. An unqualified name
// prefers the selected schema; enum names match exactly.
func (p *parser) enumType(ref []string) (rawType, enum string, err error) {
	qualifier, name := "", ref[len(ref)-1]
	if len(ref) == 2 {
		qualifier = ref[0]
	}
	var found []string
	for _, e := range p.enums {
		if e.name != name || qualifier != "" && e.schema != qualifier {
			continue
		}
		if e.schema == p.selected {
			return name, name, nil
		}
		found = append(found, e.schema)
	}
	if len(found) == 1 {
		// Enums of other schemas are not part of the IR.
		return found[0] + "." + name, "", nil
	}
	return "", "", fmt.Errorf("unknown enum %q", strings.Join(ref, "."))
}

// typeArg renders one type call argument: numbers as written, strings as
// SQL literals.
func typeArg(expr hclsyntax.Expression) (string, bool) {
	if s, ok := stringLiteral(expr); ok {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'", true
	}
	// Atlas can print signed numbers, notably a negative Postgres numeric
	// scale: numeric(10,-2). HCL parses -2 as a unary operation, not a literal.
	switch expr.(type) {
	case *hclsyntax.LiteralValueExpr, *hclsyntax.UnaryOpExpr:
	default:
		return "", false
	}
	var s string
	if diags := gohcl.DecodeExpression(expr, nil, &s); diags.HasErrors() {
		return "", false
	}
	return s, true
}

func baseType(rawType string) string {
	base := strings.ToLower(strings.TrimSpace(rawType))
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	return base
}

// uniqueColumns returns the columns of an index or unique constraint that
// proves a column set unique: unique = true (implied for a "unique" block),
// no "where" predicate and only plain column parts.
func uniqueColumns(b *hclsyntax.Block) ([]string, bool, error) {
	if b.Type == "index" {
		unique, err := boolAttr(b.Body, "unique")
		if err != nil || !unique {
			return nil, false, err
		}
	}
	if _, partial := b.Body.Attributes["where"]; partial {
		return nil, false, nil
	}
	cols, ok := keyColumns(b.Body)
	if !ok || len(cols) == 0 {
		return nil, false, nil
	}
	return cols, true, nil
}

// keyColumns reads the parts of a primary key or index, given as
// columns = [column.a, …] or as on { column = column.a } blocks. It reports
// false when a part is not a plain column, such as on { expr = "…" }.
func keyColumns(body *hclsyntax.Body) ([]string, bool) {
	cols := []string{}
	if attr, ok := body.Attributes["columns"]; ok {
		list, ok := columnList(attr.Expr)
		if !ok {
			return nil, false
		}
		cols = append(cols, list...)
	}
	for _, b := range body.Blocks {
		if b.Type != "on" {
			continue
		}
		if _, ok := b.Body.Attributes["expr"]; ok {
			return nil, false
		}
		attr, ok := b.Body.Attributes["column"]
		if !ok {
			return nil, false
		}
		c, ok := columnName(attr.Expr)
		if !ok {
			return nil, false
		}
		cols = append(cols, c)
	}
	return cols, true
}

func (p *parser) parseForeignKey(b *hclsyntax.Block, index int, table string) (ir.ForeignKey, error) {
	symbol := blockName(b)
	label := fmt.Sprintf("foreign key %q", symbol)
	if symbol == "" {
		label = fmt.Sprintf("foreign key at index %d", index)
	}
	fail := func(msg string) (ir.ForeignKey, error) { return ir.ForeignKey{}, fmt.Errorf("%s: %s", label, msg) }

	var cols []string
	if attr, ok := b.Body.Attributes["columns"]; ok {
		if cols, ok = columnList(attr.Expr); !ok {
			return fail("unrecognized column reference")
		}
	}
	if len(cols) == 0 {
		return fail("missing columns")
	}

	var refs []hclsyntax.Expression
	if attr, ok := b.Body.Attributes["ref_columns"]; ok {
		tuple, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
		if !ok {
			return fail("unrecognized referenced column")
		}
		refs = tuple.Exprs
	}
	if len(refs) == 0 {
		return fail("missing referenced columns")
	}
	var refQualifier, refTable string
	refCols := make([]string, 0, len(refs))
	for i, r := range refs {
		qualifier, t, c, ok := refColumn(r, table)
		if !ok {
			return fail("unrecognized referenced column")
		}
		if i > 0 && (qualifier != refQualifier || t != refTable) {
			return fail("referenced columns must belong to one table")
		}
		refQualifier, refTable = qualifier, t
		refCols = append(refCols, c)
	}
	if len(cols) != len(refCols) {
		return fail(fmt.Sprintf("%d columns reference %d columns", len(cols), len(refCols)))
	}
	if symbol == "" {
		symbol = "fk_" + strings.Join(cols, "_") + "_" + refTable
	}
	refSchema, external := p.refSchema(refQualifier, refTable)
	return ir.ForeignKey{
		Symbol:      symbol,
		Columns:     cols,
		RefSchema:   refSchema,
		RefTable:    refTable,
		RefColumns:  refCols,
		RefExternal: external,
	}, nil
}

// refColumn reads a referenced column: column.<c> (the table itself),
// table.<t>.column.<c>, or table.<schema>.<t>.column.<c>, which Atlas prints
// when two schemas hold a table named <t>.
func refColumn(expr hclsyntax.Expression, self string) (qualifier, table, column string, ok bool) {
	names, ok := traversalNames(expr)
	if !ok {
		return "", "", "", false
	}
	switch {
	case len(names) == 2 && names[0] == "column":
		return "", self, names[1], true
	case len(names) == 4 && names[0] == "table" && names[2] == "column":
		return "", names[1], names[3], true
	case len(names) == 5 && names[0] == "table" && names[3] == "column":
		return names[1], names[2], names[4], true
	}
	return "", "", "", false
}

// refSchema returns the schema of a referenced table, or "" when it is the
// selected schema. An unqualified name means the selected schema if it holds
// the table, else the only other schema that does.
//
// external reports an unqualified reference to a table that no schema of the
// document holds. A single-schema inspect prints an FK into a schema that was
// not inspected this way: Atlas stubs the target table
// (sql/internal/sqlx/sqlx.go) and prints table.<t>.column.<c> without a
// qualifier, because QualifyReferences (sql/internal/specutil/spec.go) only
// qualifies references to tables in the output.
//
// Limitation: if the selected schema also has a table named <t>, such a
// reference cannot be told apart from a same-schema reference and resolves
// to the selected schema. Inspecting every schema involved (a realm-wide
// URL) avoids this.
func (p *parser) refSchema(qualifier, table string) (schema string, external bool) {
	if qualifier != "" {
		if qualifier == p.selected {
			return "", false
		}
		return qualifier, false
	}
	var others []string
	for _, t := range p.tables {
		if t.name != table {
			continue
		}
		if t.schema == p.selected {
			return "", false
		}
		others = append(others, t.schema)
	}
	switch len(others) {
	case 0:
		return "", true
	case 1:
		return others[0], false
	}
	// Several other schemas hold <t>: Atlas would have qualified the
	// reference, so this does not occur in inspect output.
	return "", false
}

// columnList reads [column.a, column.b].
func columnList(expr hclsyntax.Expression) ([]string, bool) {
	tuple, ok := expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, false
	}
	cols := make([]string, 0, len(tuple.Exprs))
	for _, e := range tuple.Exprs {
		c, ok := columnName(e)
		if !ok {
			return nil, false
		}
		cols = append(cols, c)
	}
	return cols, true
}

// columnName reads column.<name> (or column["<name>"]).
func columnName(expr hclsyntax.Expression) (string, bool) {
	names, ok := traversalNames(expr)
	if !ok || len(names) != 2 || names[0] != "column" {
		return "", false
	}
	return names[1], true
}

// traversalNames returns the steps of a reference such as table.users or
// column["2fa"]. Atlas writes names that are not identifiers as string
// index keys.
func traversalNames(expr hclsyntax.Expression) ([]string, bool) {
	e, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(e.Traversal))
	for _, step := range e.Traversal {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			names = append(names, s.Name)
		case hcl.TraverseAttr:
			names = append(names, s.Name)
		case hcl.TraverseIndex:
			var key string
			if diags := gohcl.DecodeExpression(&hclsyntax.LiteralValueExpr{Val: s.Key}, nil, &key); diags.HasErrors() {
				return nil, false
			}
			names = append(names, key)
		default:
			return nil, false
		}
	}
	return names, true
}

// stringLiteral reads a quoted string without interpolations.
func stringLiteral(expr hclsyntax.Expression) (string, bool) {
	t, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok || !t.IsStringLiteral() {
		return "", false
	}
	v, diags := t.Value(nil)
	if diags.HasErrors() {
		return "", false
	}
	return v.AsString(), true
}

// stringList reads a list of string literals; a missing attribute is not a
// list.
func stringList(attr *hclsyntax.Attribute) ([]string, bool) {
	if attr == nil {
		return nil, false
	}
	tuple, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, false
	}
	values := make([]string, 0, len(tuple.Exprs))
	for _, e := range tuple.Exprs {
		s, ok := stringLiteral(e)
		if !ok {
			return nil, false
		}
		values = append(values, s)
	}
	return values, true
}

// boolAttr reads an optional boolean attribute; absent means false.
func boolAttr(body *hclsyntax.Body, name string) (bool, error) {
	attr, ok := body.Attributes[name]
	if !ok {
		return false, nil
	}
	var v bool
	if diags := gohcl.DecodeExpression(attr.Expr, nil, &v); diags.HasErrors() {
		return false, fmt.Errorf("%s: %s", name, formatDiags(diags))
	}
	return v, nil
}

// formatDiags renders error diagnostics as "schema.hcl:line:col: summary;
// detail", joined by "; ".
func formatDiags(diags hcl.Diagnostics) string {
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Severity != hcl.DiagError {
			continue
		}
		msg := d.Summary
		if d.Detail != "" {
			msg += "; " + d.Detail
		}
		if r := d.Subject; r != nil {
			msg = fmt.Sprintf("%s:%d:%d: %s", r.Filename, r.Start.Line, r.Start.Column, msg)
		}
		msgs = append(msgs, msg)
	}
	return strings.Join(msgs, "; ")
}

func hasBlock(body *hclsyntax.Body, blockType string) bool {
	for _, b := range body.Blocks {
		if b.Type == blockType {
			return true
		}
	}
	return false
}
