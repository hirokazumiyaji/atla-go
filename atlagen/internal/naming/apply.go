package naming

import (
	"errors"
	"fmt"
	"go/token"
	"slices"
	"strings"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
)

// Overrides are the user-supplied naming settings.
type Overrides struct {
	Tables      map[string]string // names.tables: table DB name → model name
	RootPackage string            // name of the generated root package
}

// reservedRoot are root-package identifiers declared by the generated client
// (Plan Decision 7).
var reservedRoot = []string{"Client", "NewClient", "Dialect"}

// reservedClient are the exported identifiers declared by the generated Client
// besides one repository field per model (Plan Decision 7). A model with one
// of these names would collide with the method.
var reservedClient = []string{"Tx"}

// derivedTypes are the root-package identifiers each model reserves besides
// itself (Plan Decision 7).
var derivedTypes = []struct{ suffix, what string }{
	{"Edges", "edges type"},
	{"Insert", "insert type"},
	{"Update", "update type"},
	{"Query", "query type"},
	{"Repo", "repo type"},
	{"Table", "table variable"},
	{"Key", "key type"},
	{"InsertQuery", "insert query type"},
	{"UpdateQuery", "update query type"},
	{"DeleteQuery", "delete query type"},
}

// reservedColumn are the identifiers each column package declares besides one
// variable per column (generator-spec §7.1); a column field with one of these
// names gets the suffix "Col".
var reservedColumn = setOf("Edges", "Table", "Columns", "As")

// Apply fills Table.Model, Table.Package, Column.GoName, Enum.GoName and
// EnumValue.Go. It reports every invalid override and every identifier clash,
// joined in a deterministic order.
func Apply(s *ir.Schema, o Overrides) error {
	errs := checkOverrides(s, o)
	for i := range s.Tables {
		errs = append(errs, nameTable(&s.Tables[i], o)...)
	}
	for i := range s.Enums {
		errs = append(errs, nameEnum(&s.Enums[i])...)
	}
	errs = append(errs, rootClashes(s.Tables)...)
	errs = append(errs, clientClashes(s.Tables)...)
	errs = append(errs, packageClashes(s.Tables)...)
	for _, t := range s.Tables {
		errs = append(errs, columnClashes(t)...)
	}
	errs = append(errs, enumClashes(s.Enums)...)
	return errors.Join(errs...)
}

func checkOverrides(s *ir.Schema, o Overrides) []error {
	known := make(map[string]bool, len(s.Tables))
	for _, t := range s.Tables {
		known[t.DBName] = true
	}
	var errs []error
	for _, key := range sortedKeys(o.Tables) {
		if !known[key] {
			errs = append(errs, fmt.Errorf("names.tables: %q does not match any table", key))
			continue
		}
		if v := o.Tables[key]; !token.IsIdentifier(v) || !token.IsExported(v) {
			errs = append(errs, fmt.Errorf("names.tables: table %q: %q is not an exported Go identifier", key, v))
		}
	}
	return errs
}

func nameTable(t *ir.Table, o Overrides) []error {
	var errs []error
	if m, ok := o.Tables[t.DBName]; ok {
		t.Model = m
	} else {
		t.Model = modelName(t.DBName)
	}
	if t.Model == "" {
		errs = append(errs, fmt.Errorf("table %q: cannot derive a Go identifier from the name; set one with names.tables", t.DBName))
	}
	t.Package = PackageName(t.Model, o.RootPackage)
	for j := range t.Columns {
		c := &t.Columns[j]
		c.GoName = Pascal(c.DBName)
		switch {
		case reservedColumn[c.GoName]:
			c.GoName += "Col"
		case c.GoName == "":
			errs = append(errs, fmt.Errorf("table %q: column %q: cannot derive a Go identifier from the name", t.DBName, c.DBName))
		}
	}
	return errs
}

func nameEnum(e *ir.Enum) []error {
	e.GoName = Pascal(e.DBName)
	if e.GoName == "" {
		return []error{fmt.Errorf("enum %q: cannot derive a Go identifier from the name", e.DBName)}
	}
	for k := range e.Values {
		// The value follows the type name, so a leading digit needs no prefix.
		v := concat(words(e.Values[k].DB))
		if v == "" {
			v = "Empty"
		}
		e.Values[k].Go = e.GoName + v
	}
	return nil
}

// rootClashes reports tables sharing a model, then root-package identifiers
// (models, their derived types and the reserved names) declared twice.
func rootClashes(tables []ir.Table) []error {
	byModel, models := group(tables, func(t ir.Table) (string, string) { return t.Model, t.DBName })
	var errs []error
	for _, m := range sorted(models) {
		if names := byModel[m]; len(names) > 1 {
			errs = append(errs, fmt.Errorf("tables %s map to the same model %q; rename them with names.tables", quoteAll(names), m))
		}
	}
	ids := map[string][]string{}
	for _, r := range reservedRoot {
		ids[r] = append(ids[r], "reserved identifier")
	}
	for _, m := range models {
		table := byModel[m][0]
		ids[m] = append(ids[m], fmt.Sprintf("model of table %q", table))
		for _, d := range derivedTypes {
			id := m + d.suffix
			ids[id] = append(ids[id], fmt.Sprintf("%s of model %q from table %q", d.what, m, table))
		}
	}
	for _, id := range sortedKeys(ids) {
		if owners := ids[id]; len(owners) > 1 {
			errs = append(errs, fmt.Errorf("root identifier %q is generated more than once (%s); rename with names.tables", id, strings.Join(owners, ", ")))
		}
	}
	return errs
}

// clientClashes reports models whose Client field would collide with a method
// the generated Client declares.
func clientClashes(tables []ir.Table) []error {
	var errs []error
	for _, t := range tables {
		if t.Model != "" && slices.Contains(reservedClient, t.Model) {
			errs = append(errs, fmt.Errorf("table %q: model %q collides with the Client.%s method; rename it with names.tables", t.DBName, t.Model, t.Model))
		}
	}
	return errs
}

// packageClashes reports tables with different models but the same package.
// Tables sharing a model are already reported by rootClashes.
func packageClashes(tables []ir.Table) []error {
	byPkg, pkgs := group(tables, func(t ir.Table) (string, string) {
		if t.Model == "" {
			return "", ""
		}
		return t.Package, t.DBName
	})
	model := make(map[string]string, len(tables))
	for _, t := range tables {
		model[t.DBName] = t.Model
	}
	var errs []error
	for _, p := range sorted(pkgs) {
		names := byPkg[p]
		distinct := map[string]bool{}
		for _, n := range names {
			distinct[model[n]] = true
		}
		if len(distinct) > 1 {
			errs = append(errs, fmt.Errorf("tables %s share package %q; rename them with names.tables", quoteAll(names), p))
		}
	}
	return errs
}

func columnClashes(t ir.Table) []error {
	byField, fields := group(t.Columns, func(c ir.Column) (string, string) { return c.GoName, c.DBName })
	var errs []error
	for _, f := range fields {
		if names := byField[f]; len(names) > 1 {
			errs = append(errs, fmt.Errorf("table %q: columns %s map to the same field %q", t.DBName, quoteAll(names), f))
		}
	}
	return errs
}

// enumClashes reports enums sharing a type name, then identifiers of the enum
// package (types, their Values functions and value constants) declared twice.
func enumClashes(enums []ir.Enum) []error {
	byType, types := group(enums, func(e ir.Enum) (string, string) { return e.GoName, e.DBName })
	var errs []error
	for _, ty := range sorted(types) {
		if names := byType[ty]; len(names) > 1 {
			errs = append(errs, fmt.Errorf("enums %s map to the same type %q", quoteAll(names), ty))
		}
	}
	ids := map[string][]string{}
	for _, e := range enums {
		if e.GoName == "" || byType[e.GoName][0] != e.DBName {
			continue
		}
		ids[e.GoName] = append(ids[e.GoName], fmt.Sprintf("type of enum %q", e.DBName))
		fn := e.GoName + "Values"
		ids[fn] = append(ids[fn], fmt.Sprintf("values function of enum %q", e.DBName))
		for _, v := range e.Values {
			ids[v.Go] = append(ids[v.Go], fmt.Sprintf("value %q of enum %q", v.DB, e.DBName))
		}
	}
	for _, id := range sortedKeys(ids) {
		if owners := ids[id]; len(owners) > 1 {
			errs = append(errs, fmt.Errorf("enum identifier %q is generated more than once (%s)", id, strings.Join(owners, ", ")))
		}
	}
	return errs
}

// group maps each non-empty key to the DB names of the items that produce it,
// and returns the keys in first-appearance order.
func group[T any](items []T, key func(T) (k, dbName string)) (map[string][]string, []string) {
	m := map[string][]string{}
	var order []string
	for _, it := range items {
		k, name := key(it)
		if k == "" {
			continue
		}
		if _, seen := m[k]; !seen {
			order = append(order, k)
		}
		m[k] = append(m[k], name)
	}
	return m, order
}

func quoteAll(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
