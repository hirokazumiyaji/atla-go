// Package parser turns the JSON printed by `atlas schema inspect --format
// '{{ json . }}'` into an ir.Schema holding the database facts only. Go names
// and Go types are filled by later pipeline stages.
//
// Atlas versions and dialects print slightly different shapes (column
// references as strings or objects, index "parts" or "columns", several
// auto-increment spellings), so the input is decoded leniently.
package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
)

type atlasDoc struct {
	Schemas []atlasSchema `json:"schemas"`
}

type atlasSchema struct {
	Name   string       `json:"name"`
	Enums  []atlasEnum  `json:"enums"`
	Tables []atlasTable `json:"tables"`
}

type atlasEnum struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type atlasTable struct {
	Name        string            `json:"name"`
	Columns     []atlasColumn     `json:"columns"`
	PrimaryKey  json.RawMessage   `json:"primary_key"`
	Indexes     []atlasIndex      `json:"indexes"`
	ForeignKeys []json.RawMessage `json:"foreign_keys"`
}

type atlasColumn struct {
	Name          string            `json:"name"`
	Type          json.RawMessage   `json:"type"`
	Null          bool              `json:"null"`
	Default       json.RawMessage   `json:"default"`
	AutoIncrement json.RawMessage   `json:"auto_increment"`
	Identity      json.RawMessage   `json:"identity"`
	Attrs         []json.RawMessage `json:"attrs"`
}

type atlasIndex struct {
	Name    string            `json:"name"`
	Unique  bool              `json:"unique"`
	Columns []json.RawMessage `json:"columns"`
	Parts   []json.RawMessage `json:"parts"`
	Where   json.RawMessage   `json:"where"`
}

type atlasKey struct {
	Columns []json.RawMessage `json:"columns"`
	Parts   []json.RawMessage `json:"parts"`
}

var serialTypes = map[string]bool{
	"serial":      true,
	"serial2":     true,
	"serial4":     true,
	"serial8":     true,
	"smallserial": true,
	"bigserial":   true,
}

// Parse decodes Atlas inspect JSON and returns the schema named schemaName,
// or the first schema when schemaName is empty. Tables are sorted by DB name;
// columns, enum values, key columns and uniques keep Atlas order.
func Parse(data []byte, dialect, schemaName string) (*ir.Schema, error) {
	var doc atlasDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid atlas json: %w", err)
	}
	if len(doc.Schemas) == 0 {
		return nil, errors.New("atlas json has no schemas")
	}
	raw, err := selectSchema(doc.Schemas, schemaName)
	if err != nil {
		return nil, err
	}

	s := &ir.Schema{
		Name:    raw.Name,
		Dialect: dialect,
		Enums:   make([]ir.Enum, 0, len(raw.Enums)),
		Tables:  make([]ir.Table, 0, len(raw.Tables)),
	}
	enumByLower := make(map[string]string, len(raw.Enums))
	for i, e := range raw.Enums {
		if e.Name == "" {
			return nil, fmt.Errorf("schema %q: enum at index %d has no name", raw.Name, i)
		}
		values := make([]ir.EnumValue, 0, len(e.Values))
		for _, v := range e.Values {
			values = append(values, ir.EnumValue{DB: v})
		}
		s.Enums = append(s.Enums, ir.Enum{DBName: e.Name, Values: values})
		if _, dup := enumByLower[strings.ToLower(e.Name)]; !dup {
			enumByLower[strings.ToLower(e.Name)] = e.Name
		}
	}

	if len(raw.Tables) == 0 {
		return nil, fmt.Errorf("schema %q has no tables", raw.Name)
	}
	seen := make(map[string]bool, len(raw.Tables))
	for i := range raw.Tables {
		t, err := parseTable(&raw.Tables[i], i, dialect, enumByLower)
		if err != nil {
			return nil, err
		}
		if seen[t.DBName] {
			return nil, fmt.Errorf("duplicate table %q", t.DBName)
		}
		seen[t.DBName] = true
		s.Tables = append(s.Tables, t)
	}
	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].DBName < s.Tables[j].DBName })
	return s, nil
}

func selectSchema(schemas []atlasSchema, name string) (*atlasSchema, error) {
	if name == "" {
		return &schemas[0], nil
	}
	names := make([]string, 0, len(schemas))
	for i := range schemas {
		if schemas[i].Name == name {
			return &schemas[i], nil
		}
		names = append(names, schemas[i].Name)
	}
	return nil, fmt.Errorf("schema %q not found (available: %s)", name, strings.Join(names, ", "))
}

func parseTable(raw *atlasTable, index int, dialect string, enumByLower map[string]string) (ir.Table, error) {
	if raw.Name == "" {
		return ir.Table{}, fmt.Errorf("table at index %d has no name", index)
	}
	t := ir.Table{
		DBName:      raw.Name,
		Columns:     make([]ir.Column, 0, len(raw.Columns)),
		Uniques:     [][]string{},
		ForeignKeys: []ir.ForeignKey{},
	}
	wrap := func(err error) error { return fmt.Errorf("table %q: %w", raw.Name, err) }

	pk, err := parsePrimaryKey(raw.PrimaryKey)
	if err != nil {
		return ir.Table{}, wrap(fmt.Errorf("primary key: %w", err))
	}
	t.PrimaryKey = pk
	inPK := make(map[string]bool, len(pk))
	for _, c := range pk {
		inPK[c] = true
	}

	known := make(map[string]bool, len(raw.Columns))
	for i := range raw.Columns {
		c, err := parseColumn(&raw.Columns[i], i, dialect, pk, inPK, enumByLower)
		if err != nil {
			return ir.Table{}, wrap(err)
		}
		if known[c.DBName] {
			return ir.Table{}, wrap(fmt.Errorf("duplicate column %q", c.DBName))
		}
		known[c.DBName] = true
		t.Columns = append(t.Columns, c)
	}
	if err := checkColumns("primary key", pk, known); err != nil {
		return ir.Table{}, wrap(err)
	}

	for i := range raw.Indexes {
		cols, ok := uniqueColumns(&raw.Indexes[i])
		if !ok {
			continue
		}
		label := fmt.Sprintf("index %q", raw.Indexes[i].Name)
		if raw.Indexes[i].Name == "" {
			label = fmt.Sprintf("unnamed index %d", i)
		}
		if err := checkColumns(label, cols, known); err != nil {
			return ir.Table{}, wrap(err)
		}
		t.Uniques = append(t.Uniques, cols)
	}

	for i, rawFK := range raw.ForeignKeys {
		fk, err := parseForeignKey(rawFK, i)
		if err != nil {
			return ir.Table{}, wrap(err)
		}
		if err := checkColumns(fmt.Sprintf("foreign key %q", fk.Symbol), fk.Columns, known); err != nil {
			return ir.Table{}, wrap(err)
		}
		t.ForeignKeys = append(t.ForeignKeys, fk)
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

func parseColumn(raw *atlasColumn, index int, dialect string, pk []string, inPK map[string]bool, enumByLower map[string]string) (ir.Column, error) {
	if raw.Name == "" {
		return ir.Column{}, fmt.Errorf("column at index %d has no name", index)
	}
	rawType := extractRawType(raw.Type)
	if rawType == "" {
		return ir.Column{}, fmt.Errorf("column %q: missing type", raw.Name)
	}
	c := ir.Column{
		DBName:     raw.Name,
		RawType:    rawType,
		Enum:       enumByLower[enumKey(rawType)],
		InPK:       inPK[raw.Name],
		HasDefault: columnHasDefault(raw, rawType),
	}
	// Primary-key columns are never NULL, even when Atlas reports null=true
	// (SQLite allows it for non-INTEGER keys).
	c.Nullable = raw.Null && !c.InPK
	// SQLite: an INTEGER PRIMARY KEY column is an alias of the rowid.
	if dialect == "sqlite" && len(pk) == 1 && pk[0] == raw.Name && strings.ToLower(rawType) == "integer" {
		c.HasDefault = true
	}
	return c, nil
}

// enumKey lowercases a raw type and strips a "schema." prefix.
func enumKey(rawType string) string {
	key := strings.ToLower(rawType)
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		key = key[i+1:]
	}
	return key
}

// extractRawType reads a column type given as a string, as an object with a
// "raw", "type" or "name" string, or as an object nesting one under "Type" or
// "T". It returns "" when no type is found.
func extractRawType(raw json.RawMessage) string {
	if s, ok := asString(raw); ok {
		return s
	}
	obj, ok := asObject(raw)
	if !ok {
		return ""
	}
	for _, key := range []string{"raw", "type", "name"} {
		if s, ok := asString(obj[key]); ok && s != "" {
			return s
		}
	}
	nested := obj["Type"]
	if !truthyValue(nested) {
		nested = obj["T"]
	}
	if _, ok := asObject(nested); ok {
		return extractRawType(nested)
	}
	return ""
}

// columnHasDefault reports whether the database fills the column when an
// INSERT omits it: a DEFAULT, a serial-family type, AUTO_INCREMENT or an
// identity, in any of the spellings Atlas uses.
func columnHasDefault(raw *atlasColumn, rawType string) bool {
	if !isNull(raw.Default) {
		return true
	}
	if serialTypes[normalizeTypeName(rawType)] {
		return true
	}
	if truthy(raw.AutoIncrement) || !isNull(raw.Identity) {
		return true
	}
	for _, a := range raw.Attrs {
		attr, ok := asObject(a)
		if !ok {
			continue
		}
		attrType, _ := asString(attr["type"])
		if attrType == "" {
			attrType, _ = asString(attr["@type"])
		}
		attrType = strings.ToLower(attrType)
		name, _ := asString(attr["name"])
		name = strings.ToLower(name)
		switch {
		case strings.Contains(attrType, "auto_increment"), strings.Contains(attrType, "identity"):
			return true
		case strings.Contains(name, "auto_increment"), strings.Contains(name, "identity"):
			return true
		case isJSONTrue(attr["auto_increment"]), !isNull(attr["identity"]):
			return true
		}
	}
	if obj, ok := asObject(raw.Type); ok && truthy(obj["auto_increment"]) {
		return true
	}
	return false
}

func normalizeTypeName(rawType string) string {
	base := strings.ToLower(strings.TrimSpace(rawType))
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	return base
}

func parsePrimaryKey(raw json.RawMessage) ([]string, error) {
	if isNull(raw) {
		return []string{}, nil
	}
	var key atlasKey
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, fmt.Errorf("invalid shape: %w", err)
	}
	refs := key.Columns
	if len(refs) == 0 {
		refs = key.Parts
	}
	cols, ok := columnRefs(refs)
	if !ok {
		return nil, errors.New("unrecognized column reference")
	}
	return cols, nil
}

// uniqueColumns returns the columns of a unique index whose parts are all
// plain columns. Non-unique, partial ("where") and expression indexes report
// false: they cannot prove that a column set is unique.
func uniqueColumns(idx *atlasIndex) ([]string, bool) {
	if !idx.Unique {
		return nil, false
	}
	if !isNull(idx.Where) {
		if s, ok := asString(idx.Where); !ok || s != "" {
			return nil, false
		}
	}
	refs := idx.Columns
	if len(refs) == 0 {
		refs = idx.Parts
	}
	cols, ok := columnRefs(refs)
	if !ok || len(cols) == 0 {
		return nil, false
	}
	return cols, true
}

func parseForeignKey(raw json.RawMessage, index int) (ir.ForeignKey, error) {
	obj, ok := asObject(raw)
	if !ok {
		return ir.ForeignKey{}, fmt.Errorf("foreign key at index %d is not an object", index)
	}
	symbol, _ := asString(obj["name"])
	if symbol == "" {
		symbol, _ = asString(obj["symbol"])
	}
	label := fmt.Sprintf("foreign key %q", symbol)
	if symbol == "" {
		label = fmt.Sprintf("foreign key at index %d", index)
	}

	cols, ok := columnRefs(asList(obj["columns"]))
	if !ok {
		return ir.ForeignKey{}, fmt.Errorf("%s: unrecognized column reference", label)
	}
	if len(cols) == 0 {
		return ir.ForeignKey{}, fmt.Errorf("%s: missing columns", label)
	}

	ref, hasRef := asObject(obj["ref"])
	if !hasRef {
		ref, hasRef = asObject(obj["references"])
	}
	refList := asList(obj["ref_columns"])
	if len(refList) == 0 {
		refList = asList(obj["referenced_columns"])
	}
	if len(refList) == 0 && hasRef {
		refList = asList(ref["columns"])
		if len(refList) == 0 {
			refList = asList(ref["ref_columns"])
		}
	}
	refCols, ok := columnRefs(refList)
	if !ok {
		return ir.ForeignKey{}, fmt.Errorf("%s: unrecognized referenced column", label)
	}

	refSchema, refTable := extractRefTable(obj, ref)
	if refTable == "" {
		return ir.ForeignKey{}, fmt.Errorf("%s: missing referenced table", label)
	}
	if len(refCols) == 0 {
		return ir.ForeignKey{}, fmt.Errorf("%s: missing referenced columns", label)
	}
	if len(cols) != len(refCols) {
		return ir.ForeignKey{}, fmt.Errorf("%s: %d columns reference %d columns", label, len(cols), len(refCols))
	}
	if symbol == "" {
		symbol = "fk_" + strings.Join(cols, "_") + "_" + refTable
	}
	return ir.ForeignKey{
		Symbol:     symbol,
		Columns:    cols,
		RefSchema:  refSchema,
		RefTable:   refTable,
		RefColumns: refCols,
	}, nil
}

// extractRefTable finds the referenced table of a foreign key under
// "ref_table", "referenced_table" or "table", or in the "ref"/"references"
// object (a string, or an object with "table", "name" or "t").
func extractRefTable(fk, ref map[string]json.RawMessage) (schema, table string) {
	for _, key := range []string{"ref_table", "referenced_table", "table"} {
		if schema, table = tableNameFromValue(fk[key]); table != "" {
			return schema, table
		}
	}
	if s, ok := asString(fk["ref"]); ok && s != "" {
		return splitQualified(s)
	}
	for _, key := range []string{"table", "name", "t"} {
		if schema, table = tableNameFromValue(ref[key]); table != "" {
			return schema, table
		}
	}
	return "", ""
}

func tableNameFromValue(raw json.RawMessage) (schema, table string) {
	if s, ok := asString(raw); ok && s != "" {
		return splitQualified(s)
	}
	obj, ok := asObject(raw)
	if !ok {
		return "", ""
	}
	if name, ok := asString(obj["name"]); ok && name != "" {
		return schemaName(obj["schema"]), name
	}
	if nested, ok := obj["table"]; ok {
		return tableNameFromValue(nested)
	}
	return "", ""
}

// splitQualified splits "schema.table"; a name without a dot has no schema.
func splitQualified(s string) (schema, table string) {
	if schema, table, ok := strings.Cut(s, "."); ok && schema != "" && table != "" {
		return schema, table
	}
	return "", s
}

func schemaName(raw json.RawMessage) string {
	if s, ok := asString(raw); ok {
		return s
	}
	if obj, ok := asObject(raw); ok {
		s, _ := asString(obj["name"])
		return s
	}
	return ""
}

// columnRefs resolves a list of column references. It reports false when an
// entry is not a plain column, such as an index expression part.
func columnRefs(refs []json.RawMessage) ([]string, bool) {
	cols := make([]string, 0, len(refs))
	for _, r := range refs {
		c, ok := columnRef(r)
		if !ok {
			return nil, false
		}
		cols = append(cols, c)
	}
	return cols, true
}

// columnRef resolves "email", {"name": "email"}, {"column": "email"},
// {"c": "email"}, {"column": {"name": "email"}} and {"c": {"name": "email"}}.
func columnRef(raw json.RawMessage) (string, bool) {
	if s, ok := asString(raw); ok {
		return s, s != ""
	}
	obj, ok := asObject(raw)
	if !ok {
		return "", false
	}
	if s, ok := asString(obj["name"]); ok && s != "" {
		return s, true
	}
	for _, key := range []string{"column", "c"} {
		if s, ok := asString(obj[key]); ok && s != "" {
			return s, true
		}
		if col, ok := asObject(obj[key]); ok {
			if s, ok := asString(col["name"]); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func isJSONTrue(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("true"))
}

// truthy reports a JSON true or one of the strings "true", "1", "yes".
func truthy(raw json.RawMessage) bool {
	if isJSONTrue(raw) {
		return true
	}
	s, ok := asString(raw)
	if !ok {
		return false
	}
	switch strings.ToLower(s) {
	case "true", "1", "yes":
		return true
	}
	return false
}

// truthyValue mirrors Python truthiness for "x or y" fallbacks.
func truthyValue(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "false", "0", `""`, "[]", "{}":
		return false
	}
	return true
}

func asString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return "", false
	}
	return s, true
}

func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, false
	}
	return obj, true
}

func asList(raw json.RawMessage) []json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return nil
	}
	return list
}
