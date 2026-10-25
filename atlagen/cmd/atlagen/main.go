// Command atlagen generates Go code for the atla runtime from a schema
// managed by Atlas. This phase ships the `ir` debugging command, which
// prints the resolved internal representation, and `version`.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/hirokazumiyaji/atla-go/atlagen/internal/config"
	"github.com/hirokazumiyaji/atla-go/atlagen/internal/pipeline"
)

const usage = `Usage: atlagen <command> [flags]

Commands:
  ir        print the resolved schema IR as indented JSON
  version   print the atlagen version

Run "atlagen <command> -h" for the flags of a command.
`

const irUsage = `Usage: atlagen ir [flags]

Prints the resolved schema IR as indented JSON.

Flags:
  --config <file>        config file (default ./atlagen.yaml if present)
  --schema, -s <src>     "-" (stdin, default), an Atlas HCL file, or an Atlas URL
  --dev-url <url>        dev database URL passed to atlas schema inspect
  --dialect <name>       postgres or sqlite (required)
  --schema-name <name>   schema to use when the input has several
  --out, -o <dir>        output directory (required)
  --package <name>       root package name (default: base name of --out)
  --nullable <style>     pointer (default) or sqlnull
  --json-tags            emit json tags on model fields (default true)
`

const versionUsage = `Usage: atlagen version

Prints the atlagen version.
`

// errUsage marks a usage error; the usage text has already been printed.
var errUsage = errors.New("usage error")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := 1
	if cwd, err := os.Getwd(); err != nil {
		fmt.Fprintf(os.Stderr, "atlagen: %v\n", err)
	} else {
		code = run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cwd)
	}
	stop()
	os.Exit(code)
}

// run executes the command line args and returns the exit code: 0 on
// success, 1 on a runtime error and 2 on a usage error.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) int {
	if len(args) == 0 {
		printTo(stderr, "%s", usage)
		return 2
	}
	var err error
	switch args[0] {
	case "ir":
		err = runIR(ctx, args[1:], stdin, stdout, stderr, cwd)
	case "version":
		err = runVersion(args[1:], stdout, stderr)
	default:
		printTo(stderr, "%s", usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		return 2
	default:
		printTo(stderr, "atlagen: %v\n", err)
		return 1
	}
}

// printTo writes a diagnostic to w. A failed write to stderr has nowhere
// to be reported, so its error is dropped.
func printTo(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

// newFlagSet returns a flag set that prints help and parse errors to stderr.
func newFlagSet(name, help string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("atlagen "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { printTo(stderr, "%s", help) }
	return fs
}

// parse parses args into fs. Parse errors, -h and positional arguments are
// usage errors.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer) error {
	if err := fs.Parse(args); err != nil {
		return errUsage // fs has printed the error and the usage
	}
	if fs.NArg() > 0 {
		printTo(stderr, "unexpected arguments: %q\n", fs.Args())
		fs.Usage()
		return errUsage
	}
	return nil
}

func runIR(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) error {
	fs := newFlagSet("ir", irUsage, stderr)
	var (
		cfg, schema, devURL, dialect, schemaName, out, pkg, nullable string
		jsonTags                                                     bool
	)
	fs.StringVar(&cfg, "config", "", "")
	// Each alias shares its variable, so the last one on the command line wins.
	fs.StringVar(&schema, "schema", "", "")
	fs.StringVar(&schema, "s", "", "")
	fs.StringVar(&devURL, "dev-url", "", "")
	fs.StringVar(&dialect, "dialect", "", "")
	fs.StringVar(&schemaName, "schema-name", "", "")
	fs.StringVar(&out, "out", "", "")
	fs.StringVar(&out, "o", "", "")
	fs.StringVar(&pkg, "package", "", "")
	fs.StringVar(&nullable, "nullable", "", "")
	fs.BoolVar(&jsonTags, "json-tags", true, "")
	if err := parse(fs, args, stderr); err != nil {
		return err
	}

	// Only flags given on the command line override the config file.
	var flags config.Flags
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "config":
			flags.Config = &cfg
		case "schema", "s":
			flags.Schema = &schema
		case "dev-url":
			flags.DevURL = &devURL
		case "dialect":
			flags.Dialect = &dialect
		case "schema-name":
			flags.SchemaName = &schemaName
		case "out", "o":
			flags.Out = &out
		case "package":
			flags.Package = &pkg
		case "nullable":
			flags.Nullable = &nullable
		case "json-tags":
			flags.JSONTags = &jsonTags
		}
	})

	r, err := config.Resolve(cwd, flags)
	if err != nil {
		return err
	}
	s, warnings, err := pipeline.Run(ctx, r, stdin)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		printTo(stderr, "atlagen: warning: %s\n", w)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(data, '\n'))
	return err
}

func runVersion(args []string, stdout, stderr io.Writer) error {
	if err := parse(newFlagSet("version", versionUsage, stderr), args, stderr); err != nil {
		return err
	}
	var v string
	if info, ok := debug.ReadBuildInfo(); ok {
		v = info.Main.Version
	}
	_, err := fmt.Fprintf(stdout, "atlagen %s\n", versionString(v))
	return err
}

// versionString returns the version to print for a module version from the
// build info: "devel" for a build outside a released module.
func versionString(v string) string {
	if v == "" || v == "(devel)" {
		return "devel"
	}
	return v
}
