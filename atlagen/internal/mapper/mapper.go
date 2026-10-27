// Package mapper resolves the Go type, atla column kind and scan plan of
// every column (generator-spec §4). It runs after naming.Apply, whose enum
// Go names it uses.
package mapper

import (
	"errors"
	"fmt"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
)

// NullStyle is the model field style of nullable columns (--nullable).
type NullStyle string

// Null styles.
const (
	NullPointer NullStyle = "pointer" // *T
	NullSQLNull NullStyle = "sqlnull" // sql.Null[T]
)

// TypeOverride maps every column of a database type to a Go type (`types:`).
type TypeOverride struct {
	DBType string // matched after NormalizeType (and Postgres alias folding)
	GoType string // "github.com/google/uuid.UUID", "time.Duration", "int64"
}

// ColumnOverride maps one column to a Go type (`columns:`). It wins over a
// TypeOverride.
type ColumnOverride struct {
	Column string // "table.column"
	GoType string
}

// Options configure Apply.
type Options struct {
	Dialect        string    // "postgres" or "sqlite"
	Nullable       NullStyle // "" means NullPointer
	Types          []TypeOverride
	Columns        []ColumnOverride
	EnumImportPath string // import path of the generated enum package
}

// mapping is a resolved Go type and its NOT NULL column kind.
type mapping struct {
	goType ir.GoType
	kind   ir.ColumnKind
}

func builtin(name string, kind ir.ColumnKind) mapping {
	return mapping{ir.GoType{Name: name}, kind}
}

var (
	int16Number   = builtin("int16", ir.KindNumber)
	int32Number   = builtin("int32", ir.KindNumber)
	int64Number   = builtin("int64", ir.KindNumber)
	float32Number = builtin("float32", ir.KindNumber)
	float64Number = builtin("float64", ir.KindNumber)
	stringColumn  = builtin("string", ir.KindColumn)
	stringString  = builtin("string", ir.KindString)
	boolColumn    = builtin("bool", ir.KindColumn)
	bytesColumn   = builtin("[]byte", ir.KindColumn)
	timeColumn    = mapping{ir.GoType{Name: "time.Time", ImportPath: "time"}, ir.KindColumn}
	jsonColumn    = mapping{ir.GoType{Name: "json.RawMessage", ImportPath: "encoding/json"}, ir.KindColumn}
)

// pgAliases folds Postgres type synonyms onto the names used in pgTypes.
var pgAliases = map[string]string{
	"int2":              "smallint",
	"int":               "integer",
	"int4":              "integer",
	"int8":              "bigint",
	"serial2":           "smallserial",
	"serial4":           "serial",
	"serial8":           "bigserial",
	"float4":            "real",
	"float8":            "double precision",
	"decimal":           "numeric",
	"bool":              "boolean",
	"character varying": "varchar",
	"character":         "char",
	"bpchar":            "char",
}

// pgTypes is generator-spec §4.1, keyed by canonical type name.
var pgTypes = map[string]mapping{
	"smallint":         int16Number,
	"smallserial":      int16Number,
	"integer":          int32Number,
	"serial":           int32Number,
	"bigint":           int64Number,
	"bigserial":        int64Number,
	"real":             float32Number,
	"double precision": float64Number,
	"numeric":          stringColumn,
	"money":            stringColumn,
	"boolean":          boolColumn,
	"text":             stringString,
	"varchar":          stringString,
	"char":             stringString,
	"citext":           stringString,
	"inet":             stringString,
	"cidr":             stringString,
	"macaddr":          stringString,
	"bytea":            bytesColumn,
	"uuid":             stringColumn,
	"date":             timeColumn,
	"timestamp":        timeColumn,
	"timestamptz":      timeColumn,
	"time":             stringColumn,
	"timetz":           stringColumn,
	"interval":         stringColumn,
	"json":             jsonColumn,
	"jsonb":            jsonColumn,
}

// sqliteTypes is generator-spec §4.2.
var sqliteTypes = map[string]mapping{
	"integer":   int64Number,
	"int":       int64Number,
	"bigint":    int64Number,
	"smallint":  int64Number,
	"tinyint":   int64Number,
	"real":      float64Number,
	"double":    float64Number,
	"float":     float64Number,
	"numeric":   stringColumn,
	"decimal":   stringColumn,
	"boolean":   boolColumn,
	"bool":      boolColumn,
	"text":      stringString,
	"varchar":   stringString,
	"char":      stringString,
	"clob":      stringString,
	"blob":      bytesColumn,
	"date":      timeColumn,
	"datetime":  timeColumn,
	"timestamp": timeColumn,
	"uuid":      stringColumn,
	"json":      jsonColumn,
}

// sqliteAffinity are SQLite's type affinity rules in precedence order (Plan
// Decision 12).
var sqliteAffinity = []struct {
	substrings []string
	m          mapping
}{
	{[]string{"int"}, int64Number},
	{[]string{"char", "clob", "text"}, stringString},
	{[]string{"blob"}, bytesColumn},
	{[]string{"real", "floa", "doub"}, float64Number}, //nolint:misspell // SQLite affinity substrings
}

// Builtin Go types accepted as override go_type values.
var (
	integerTypes = setOf("int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune")
	floatTypes = setOf("float32", "float64")
	otherTypes = setOf("bool", "string", "[]byte")
)

// numberTypes are the integer and float types accepted by atla.Number
// (runtime column.go), which is the constraint on NumberColumn. uint and
// uintptr are integers, but they are not in that set, so overrides using them
// get Column instead.
var numberTypes = func() map[string]bool {
	m := make(map[string]bool, len(integerTypes)+len(floatTypes))
	for t := range integerTypes {
		if t != "uint" && t != "uintptr" {
			m[t] = true
		}
	}
	for t := range floatTypes {
		m[t] = true
	}
	return m
}()

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

var nullKinds = map[ir.ColumnKind]ir.ColumnKind{
	ir.KindColumn: ir.KindNullColumn,
	ir.KindNumber: ir.KindNullNumber,
	ir.KindString: ir.KindNullString,
}

// typeOverride is a parsed TypeOverride.
type typeOverride struct {
	dbType string // as written
	m      mapping
	used   bool // some column has this database type
}

// Apply fills Column.GoType, Column.Kind and Column.Scan of every column.
//
// Resolution order per column: a `columns:` override, a `types:` override,
// the schema enum, then the dialect table. It returns the sorted warnings
// (unknown SQLite types, `types:` overrides that match no column) or every
// error joined in schema order.
func Apply(s *ir.Schema, o Options) (warnings []string, err error) {
	if o.Dialect != "postgres" && o.Dialect != "sqlite" {
		return nil, fmt.Errorf("dialect %q is not supported", o.Dialect)
	}
	style := o.Nullable
	if style == "" {
		style = NullPointer
	}
	if style != NullPointer && style != NullSQLNull {
		return nil, fmt.Errorf("unknown nullable style %q (want %q or %q)", style, NullPointer, NullSQLNull)
	}

	types, typeErrs := parseTypeOverrides(o.Dialect, o.Types)
	columns, colErrs := parseColumnOverrides(s, o.Columns)
	if len(typeErrs)+len(colErrs) > 0 {
		return nil, errors.Join(append(typeErrs, colErrs...)...)
	}
	enums := make(map[string]string, len(s.Enums))
	for _, e := range s.Enums {
		enums[e.DBName] = e.GoName
	}

	var errs []error
	for ti := range s.Tables {
		t := &s.Tables[ti]
		for ci := range t.Columns {
			c := &t.Columns[ci]
			key := typeKey(o.Dialect, c.RawType)
			to := types[key]
			if to != nil {
				to.used = true
			}
			var (
				m    mapping
				warn string
				err  error
			)
			cm, hasColumnOverride := columns[t.DBName+"."+c.DBName]
			switch {
			case hasColumnOverride:
				m = cm
			case to != nil:
				m = to.m
			case c.Enum != "":
				m, err = enumMapping(c.Enum, enums, o.EnumImportPath)
			case o.Dialect == "postgres":
				m, err = postgresMapping(key, c.RawType)
			default:
				m, warn = sqliteMapping(key, c.RawType)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("table %q: column %q: %w", t.DBName, c.DBName, err))
				continue
			}
			if warn != "" {
				warnings = append(warnings, fmt.Sprintf("table %q: column %q: %s", t.DBName, c.DBName, warn))
			}
			c.GoType = m.goType
			c.Kind = m.kind
			if c.Nullable {
				c.Kind = nullKinds[m.kind]
			}
			c.Scan = scanPlan(o.Dialect, m, c.Nullable, style)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	for _, to := range types {
		if !to.used {
			warnings = append(warnings, fmt.Sprintf("types: override for db type %q matches no column", to.dbType))
		}
	}
	sort.Strings(warnings)
	return warnings, nil
}

// typeKey is the key that database types are looked up and matched by: the
// normalized type, with Postgres synonyms folded.
func typeKey(dialect, raw string) string {
	key := NormalizeType(raw)
	if dialect != "postgres" {
		return key
	}
	base, array := strings.CutSuffix(key, "[]")
	if alias, ok := pgAliases[base]; ok {
		base = alias
	}
	if array {
		return base + "[]"
	}
	return base
}

func parseTypeOverrides(dialect string, overrides []TypeOverride) (map[string]*typeOverride, []error) {
	out := make(map[string]*typeOverride, len(overrides))
	var errs []error
	for _, ov := range overrides {
		key := typeKey(dialect, ov.DBType)
		if key == "" {
			errs = append(errs, errors.New("types: empty db_type"))
			continue
		}
		if _, dup := out[key]; dup {
			errs = append(errs, fmt.Errorf("types: duplicate override for db type %q", ov.DBType))
			continue
		}
		m, err := overrideMapping(ov.GoType)
		if err != nil {
			errs = append(errs, fmt.Errorf("types: db type %q: %w", ov.DBType, err))
			continue
		}
		out[key] = &typeOverride{dbType: ov.DBType, m: m}
	}
	return out, errs
}

func parseColumnOverrides(s *ir.Schema, overrides []ColumnOverride) (map[string]mapping, []error) {
	known := make(map[string]bool)
	for _, t := range s.Tables {
		for _, c := range t.Columns {
			known[t.DBName+"."+c.DBName] = true
		}
	}
	out := make(map[string]mapping, len(overrides))
	var errs []error
	for _, ov := range overrides {
		table, col, ok := strings.Cut(ov.Column, ".")
		switch {
		case !ok || table == "" || col == "":
			errs = append(errs, fmt.Errorf("columns: invalid column %q (want table.column)", ov.Column))
			continue
		case !known[ov.Column]:
			errs = append(errs, fmt.Errorf("columns: unknown column %q", ov.Column))
			continue
		}
		if _, dup := out[ov.Column]; dup {
			errs = append(errs, fmt.Errorf("columns: duplicate override for %q", ov.Column))
			continue
		}
		m, err := overrideMapping(ov.GoType)
		if err != nil {
			errs = append(errs, fmt.Errorf("columns: column %q: %w", ov.Column, err))
			continue
		}
		out[ov.Column] = m
	}
	return out, errs
}

// overrideMapping parses an override go_type and picks its kind (Plan
// Decision 10).
func overrideMapping(goType string) (mapping, error) {
	t, err := parseGoType(goType)
	if err != nil {
		return mapping{}, err
	}
	kind := ir.KindColumn
	switch {
	case numberTypes[t.Name]:
		kind = ir.KindNumber
	case t.Name == "string":
		kind = ir.KindString
	}
	return mapping{t, kind}, nil
}

var (
	pathElem     = regexp.MustCompile(`^[A-Za-z0-9._~+-]+$`)
	majorVersion = regexp.MustCompile(`^v[0-9]+$`)
	gopkgVersion = regexp.MustCompile(`\.v[0-9]+$`)
)

// parseGoType parses "path.Type" or a builtin. The import path is everything
// before the last "." after the last "/"; the qualifier is the path's last
// element, skipping a "/vN" major-version element and a gopkg.in ".vN" suffix.
func parseGoType(s string) (ir.GoType, error) {
	if integerTypes[s] || floatTypes[s] || otherTypes[s] {
		return ir.GoType{Name: s}, nil
	}
	invalid := func(why string) (ir.GoType, error) {
		return ir.GoType{}, fmt.Errorf("invalid go_type %q: %s", s, why)
	}
	slash := strings.LastIndexByte(s, '/')
	dot := strings.LastIndexByte(s[slash+1:], '.')
	if dot < 0 {
		return invalid("want a builtin or path.Type")
	}
	path, name := s[:slash+1+dot], s[slash+1+dot+1:]
	if !token.IsIdentifier(name) || !token.IsExported(name) {
		return invalid("type name must be an exported identifier")
	}
	elems := strings.Split(path, "/")
	for _, e := range elems {
		if !pathElem.MatchString(e) {
			return invalid("bad import path")
		}
	}
	pkg := elems[len(elems)-1]
	if majorVersion.MatchString(pkg) && len(elems) > 1 {
		pkg = elems[len(elems)-2]
	}
	pkg = gopkgVersion.ReplaceAllString(pkg, "")
	if !token.IsIdentifier(pkg) {
		return invalid(fmt.Sprintf("package name %q derived from the import path is not an identifier", pkg))
	}
	return ir.GoType{Name: pkg + "." + name, ImportPath: path}, nil
}

func enumMapping(enum string, enums map[string]string, importPath string) (mapping, error) {
	goName, ok := enums[enum]
	if !ok {
		return mapping{}, fmt.Errorf("unknown enum %q", enum)
	}
	return mapping{ir.GoType{Name: "enum." + goName, ImportPath: importPath}, ir.KindString}, nil
}

func postgresMapping(key, raw string) (mapping, error) {
	if strings.HasSuffix(key, "[]") {
		return mapping{}, fmt.Errorf("array type %q is not supported in v1; add a columns: or types: override", raw)
	}
	if m, ok := pgTypes[key]; ok {
		return m, nil
	}
	return mapping{}, fmt.Errorf("unsupported type %q; add a types: or columns: override", raw)
}

func sqliteMapping(key, raw string) (m mapping, warning string) {
	if m, ok := sqliteTypes[key]; ok {
		return m, ""
	}
	for _, a := range sqliteAffinity {
		for _, sub := range a.substrings {
			if strings.Contains(key, sub) {
				return a.m, ""
			}
		}
	}
	return stringColumn, fmt.Sprintf("unknown sqlite type %q mapped to string", raw)
}

// widenTypes are the builtins scanned through an int64/float64 local (Plan
// Decision 11). int64 and float64 already match the driver values. uint and
// uint64 are excluded because their values can exceed int64 and database/sql
// scans them directly with strconv.ParseUint. uintptr keeps the widen plan
// because database/sql cannot scan into it directly.
var widenTypes = func() map[string]bool {
	m := setOf("float32")
	for t := range integerTypes {
		switch t {
		case "int64", "uint64", "uint":
			continue
		}
		m[t] = true
	}
	return m
}()

// scanPlan picks the scan plan (generator-spec §4.4, Plan Decision 11).
func scanPlan(dialect string, m mapping, nullable bool, style NullStyle) ir.ScanPlan {
	// SQLite drivers return JSON text as a string, which database/sql cannot
	// scan into json.RawMessage directly. Scan through []byte, which accepts
	// both string and []byte driver values, then convert.
	if dialect == "sqlite" && m.goType == jsonColumn.goType {
		if nullable {
			return ir.ScanNullTextBytes
		}
		return ir.ScanTextBytes
	}
	widen := widenTypes[m.goType.Name]
	switch {
	case widen && nullable:
		return ir.ScanNullWiden
	case widen:
		return ir.ScanWiden
	case nullable && style == NullPointer:
		return ir.ScanNullPtr
	default:
		return ir.ScanDirect
	}
}

// NormalizeType normalizes a raw database type for lookup: lowercased,
// whitespace collapsed, length/precision arguments removed, time-zone
// clauses folded ("timestamp with time zone" → "timestamptz"), interval
// fields dropped ("interval hour" → "interval") and array dimensions reduced
// to one "[]" suffix. An "unsigned" modifier is dropped; it matters only for
// MySQL (a later phase).
func NormalizeType(raw string) string {
	s := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
	array := false
	for strings.HasSuffix(s, "]") {
		i := strings.LastIndexByte(s, '[')
		if i < 0 {
			break
		}
		s, array = strings.TrimSpace(s[:i]), true
	}
	fields := strings.Fields(stripParens(s))
	kept := fields[:0]
	for _, f := range fields {
		if f != "unsigned" {
			kept = append(kept, f)
		}
	}
	s = strings.Join(kept, " ")
	switch s {
	case "timestamp with time zone":
		s = "timestamptz"
	case "timestamp without time zone":
		s = "timestamp"
	case "time with time zone":
		s = "timetz"
	case "time without time zone":
		s = "time"
	}
	if strings.HasPrefix(s, "interval ") {
		s = "interval"
	}
	if array {
		s += "[]"
	}
	return s
}

// stripParens replaces every parenthesized group with a space.
func stripParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			if depth == 0 {
				b.WriteByte(' ')
			}
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
