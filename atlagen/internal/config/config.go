// Package config loads atlagen.yaml and merges it with the command-line
// flags into the options the generator pipeline uses.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/modfile"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/inspect"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/mapper"
)

// DefaultFile is the config file Resolve uses when Flags.Config is nil and
// the file exists in the working directory.
const DefaultFile = "atlagen.yaml"

// File is the content of atlagen.yaml (generator-spec §1.4).
type File struct {
	Version    int              `yaml:"version"`
	Schema     string           `yaml:"schema"`
	DevURL     string           `yaml:"dev_url"`
	Dialect    string           `yaml:"dialect"`
	SchemaName string           `yaml:"schema_name"`
	Out        string           `yaml:"out"`
	Package    string           `yaml:"package"`
	Nullable   string           `yaml:"nullable"`
	JSONTags   *bool            `yaml:"json_tags"`
	Types      []TypeOverride   `yaml:"types"`
	Columns    []ColumnOverride `yaml:"columns"`
	Names      Names            `yaml:"names"`
}

// TypeOverride is one `types:` entry.
type TypeOverride struct {
	DBType string `yaml:"db_type"`
	GoType string `yaml:"go_type"`
}

// ColumnOverride is one `columns:` entry.
type ColumnOverride struct {
	Column string `yaml:"column"`
	GoType string `yaml:"go_type"`
}

// Names holds the `names:` identifier overrides.
type Names struct {
	Tables map[string]string `yaml:"tables"`
	Edges  []EdgeName        `yaml:"edges"`
}

// EdgeName is one `names.edges` entry. Edges are derived in a later phase;
// for now the entries are only checked for shape.
type EdgeName struct {
	FK      string `yaml:"fk"`
	Name    string `yaml:"name"`
	Inverse string `yaml:"inverse"`
}

// LoadFile reads and decodes the config file at path. Unknown keys are
// errors, version must be 1, and required entry fields must be present.
// The contents of overrides are validated later by the mapper and naming.
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

func (f *File) validate() error {
	if f.Version != 1 {
		if f.Version == 0 {
			return errors.New("version: 1 is required")
		}
		return fmt.Errorf("unsupported config version %d (want version: 1)", f.Version)
	}
	for i, t := range f.Types {
		if t.DBType == "" {
			return fmt.Errorf("types[%d]: db_type is required", i)
		}
		if t.GoType == "" {
			return fmt.Errorf("types[%d]: go_type is required", i)
		}
	}
	for i, c := range f.Columns {
		if c.Column == "" {
			return fmt.Errorf("columns[%d]: column is required", i)
		}
		if c.GoType == "" {
			return fmt.Errorf("columns[%d]: go_type is required", i)
		}
	}
	for i, e := range f.Names.Edges {
		if e.FK == "" {
			return fmt.Errorf("names.edges[%d]: fk is required", i)
		}
		if e.Name == "" {
			return fmt.Errorf("names.edges[%d]: name is required", i)
		}
	}
	return nil
}

// Flags are the command-line options. A nil pointer means the flag was not
// given; a non-nil pointer overrides the config file even when empty.
type Flags struct {
	Config, Schema, DevURL, Dialect, SchemaName, Out, Package, Nullable *string
	JSONTags                                                            *bool
}

// Resolved holds the merged and validated options.
type Resolved struct {
	// Schema is "-" (stdin), an absolute file path, or an Atlas URL as given.
	Schema string
	// DevURL is passed to atlas. A relative SQLite path in it is made absolute
	// against the directory of its source.
	DevURL string
	// BaseDir is the directory Schema was given relative to: the config
	// file's directory when Schema came from atlagen.yaml, else the working
	// directory. It is the working directory of the atlas subprocess.
	BaseDir    string
	Dialect    string
	SchemaName string
	OutDir     string // absolute
	Package    string
	ImportPath string // import path of OutDir
	Nullable   mapper.NullStyle
	JSONTags   bool
	Types      []mapper.TypeOverride
	Columns    []mapper.ColumnOverride
	TableNames map[string]string
	Edges      []EdgeName
}

// Resolve merges the config file and flags. Flags win over the config file.
// The config file is Flags.Config (relative to cwd) when set, else
// atlagen.yaml in cwd when it exists, else none. Relative paths from the
// config file resolve against its directory; relative paths from flags
// resolve against cwd.
func Resolve(cwd string, f Flags) (*Resolved, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}

	file, cfgDir, err := loadConfig(cwd, f.Config)
	if err != nil {
		return nil, err
	}

	r := &Resolved{
		BaseDir:    cwd,
		Schema:     "-",
		Dialect:    pick(f.Dialect, file.Dialect),
		SchemaName: pick(f.SchemaName, file.SchemaName),
		Package:    pick(f.Package, file.Package),
		JSONTags:   true,
		TableNames: file.Names.Tables,
		Edges:      file.Names.Edges,
	}

	// Schema: a file path is made absolute against the directory of its
	// source; "-" and URLs are kept, with BaseDir set for the atlas run.
	schema, schemaDir := file.Schema, cfgDir
	if f.Schema != nil {
		schema, schemaDir = *f.Schema, cwd
	}
	if schema != "" && schema != "-" {
		r.BaseDir = schemaDir
		r.Schema = schema
		if !inspect.IsURL(schema) {
			r.Schema = absJoin(schemaDir, schema)
		}
	}

	// The atlas subprocess has a single working directory (the schema's), so a
	// relative SQLite path in the dev URL must be resolved against its own
	// source first.
	devURL, devDir := file.DevURL, cfgDir
	if f.DevURL != nil {
		devURL, devDir = *f.DevURL, cwd
	}
	r.DevURL = resolveDevURL(devDir, devURL)

	out, outDir := file.Out, cfgDir
	if f.Out != nil {
		out, outDir = *f.Out, cwd
	}

	switch {
	case f.JSONTags != nil:
		r.JSONTags = *f.JSONTags
	case file.JSONTags != nil:
		r.JSONTags = *file.JSONTags
	}

	if err := checkDialect(r.Dialect); err != nil {
		return nil, err
	}
	if out == "" {
		return nil, errors.New("out is required (--out or out: in atlagen.yaml)")
	}
	r.OutDir = absJoin(outDir, out)

	switch nullable := pick(f.Nullable, file.Nullable); nullable {
	case "":
		r.Nullable = mapper.NullPointer
	case string(mapper.NullPointer), string(mapper.NullSQLNull):
		r.Nullable = mapper.NullStyle(nullable)
	default:
		return nil, fmt.Errorf("invalid nullable %q (want pointer|sqlnull)", nullable)
	}

	if r.Package == "" {
		base := filepath.Base(r.OutDir)
		if !validPackage(base) {
			return nil, fmt.Errorf("out directory %q is not a valid Go package name; set --package", base)
		}
		r.Package = base
	} else if !validPackage(r.Package) {
		return nil, fmt.Errorf("invalid package name %q (want a lowercase Go identifier that is not a keyword)", r.Package)
	}

	if r.DevURL != "" && !inspect.IsURL(r.Schema) {
		return nil, fmt.Errorf("--dev-url requires --schema to be an Atlas URL, got %q", r.Schema)
	}

	for _, t := range file.Types {
		r.Types = append(r.Types, mapper.TypeOverride{DBType: t.DBType, GoType: t.GoType})
	}
	for _, c := range file.Columns {
		r.Columns = append(r.Columns, mapper.ColumnOverride{Column: c.Column, GoType: c.GoType})
	}

	if r.ImportPath, err = ModuleImportPath(r.OutDir); err != nil {
		return nil, err
	}
	return r, nil
}

// loadConfig returns the config file to merge (empty when there is none)
// and the directory its relative paths resolve against.
func loadConfig(cwd string, flag *string) (*File, string, error) {
	path := filepath.Join(cwd, DefaultFile)
	if flag != nil {
		path = absJoin(cwd, *flag)
	} else if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return &File{}, cwd, nil
	}
	file, err := LoadFile(path)
	if err != nil {
		return nil, "", err
	}
	return file, filepath.Dir(path), nil
}

func checkDialect(d string) error {
	switch d {
	case "postgres", "sqlite":
		return nil
	case "mysql", "tidb":
		return fmt.Errorf("dialect %q is not supported yet", d)
	case "":
		return errors.New("dialect is required (--dialect or dialect: in atlagen.yaml)")
	default:
		return fmt.Errorf("unknown dialect %q (want postgres or sqlite)", d)
	}
}

var packageRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validPackage(name string) bool {
	return packageRE.MatchString(name) && !token.IsKeyword(name)
}

// pick returns the flag value when the flag is set, else the config value.
func pick(flag *string, config string) string {
	if flag != nil {
		return *flag
	}
	return config
}

// resolveDevURL makes a relative file path in a SQLite dev URL absolute
// against dir. The atlas subprocess has one working directory, so a dev URL
// must not depend on the directory chosen for the schema.
func resolveDevURL(dir, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "sqlite" && u.Scheme != "libsql+file") {
		return raw
	}
	// Atlas passes everything after "://" to the SQLite driver as its DSN.
	p := u.Host + u.Path
	prefix := ""
	if rest, ok := strings.CutPrefix(p, "file:"); ok {
		prefix, p = "file:", rest
	}
	// ":memory:", "file::memory:" and mode=memory select in-memory databases;
	// absolute paths are already independent of the working directory.
	if p == "" || strings.HasPrefix(p, ":") || filepath.IsAbs(p) || u.Query().Get("mode") == "memory" {
		return raw
	}
	u.Host, u.Path = "", prefix+absJoin(dir, p)
	return u.String()
}

// absJoin returns path made absolute against dir.
func absJoin(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

// ModuleImportPath returns the Go import path of outDir: the module path of
// the nearest go.mod at or above outDir joined with the relative directory.
// outDir need not exist yet.
func ModuleImportPath(outDir string) (string, error) {
	abs, err := filepath.Abs(outDir)
	if err != nil {
		return "", err
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		gomod := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(gomod)
		switch {
		case err == nil:
			mod := modfile.ModulePath(data)
			if mod == "" {
				return "", fmt.Errorf("%s: no module directive", gomod)
			}
			rel, err := filepath.Rel(dir, abs)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return mod, nil
			}
			return mod + "/" + filepath.ToSlash(rel), nil
		case !errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("reading %s: %w", gomod, err)
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("no go.mod found at or above %s", abs)
		}
	}
}
