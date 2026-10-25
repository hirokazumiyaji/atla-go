// Package pipeline runs the atlagen front end: it loads the Atlas schema,
// parses it into the IR, names it and maps its column types.
package pipeline

import (
	"context"
	"io"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/config"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/inspect"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/ir"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/mapper"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/naming"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/parser"
)

// Run builds the resolved IR for r. stdin is read when r.Schema is "-".
// It returns the mapper's warnings; errors come unchanged from the stage
// that failed, each of which already carries its own context.
func Run(ctx context.Context, r *config.Resolved, stdin io.Reader) (*ir.Schema, []string, error) {
	data, err := inspect.Load(ctx, inspect.Source{
		Schema: r.Schema,
		DevURL: r.DevURL,
		Dir:    r.BaseDir,
		Stdin:  stdin,
	})
	if err != nil {
		return nil, nil, err
	}
	s, err := parser.Parse(data, r.Dialect, r.SchemaName)
	if err != nil {
		return nil, nil, err
	}
	if err := naming.Apply(s, naming.Overrides{Tables: r.TableNames, RootPackage: r.Package}); err != nil {
		return nil, nil, err
	}
	warnings, err := mapper.Apply(s, mapper.Options{
		Dialect:        r.Dialect,
		Nullable:       r.Nullable,
		Types:          r.Types,
		Columns:        r.Columns,
		EnumImportPath: r.ImportPath + "/enum",
	})
	if err != nil {
		return nil, nil, err
	}
	return s, warnings, nil
}
