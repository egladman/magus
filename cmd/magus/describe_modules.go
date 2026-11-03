package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

// describeModules implements `magus describe modules` (list) and
// `magus describe module <name>` (detail). Modules are static — the magus
// standard library — so this needs no workspace.
func describeModules(ctx context.Context, args []string) error {
	rest, err := commandParse("describe modules", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe module[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ModuleDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no name, lists every module; with a name, prints its methods")
			fmt.Fprintln(os.Stderr, "with Buzz signatures.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	var name string
	if len(rest) > 0 {
		name = rest[0]
	}
	out := hostmodules.Describe(name)
	if name != "" && len(out) == 0 {
		mods := hostmodules.All()
		names := make([]string, len(mods)) // module names, sorted for a stable suggestion
		for i, m := range mods {
			names[i] = m.Name
		}
		slices.Sort(names)
		msg := fmt.Sprintf("unknown module %q", name)
		if sug := hint.Nearest(name, names); sug != "" {
			msg += fmt.Sprintf("; did you mean %q?", sug)
		}
		slog.ErrorContext(ctx, msg, attr.Notice(""), attr.Component("magus describe module"))
		return exit.Reported(exit.Usage)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.ModuleReport{Definition: types.ModuleDefinition, Count: len(out), Modules: out})
	case outputName:
		names := make([]string, 0, len(out))
		for _, m := range out {
			names = append(names, m.Name)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	if name == "" {
		fmt.Printf("definition: %s\n\n", types.ModuleDefinition)
		fmt.Printf("modules (%d):\n", len(out))
		for _, m := range out {
			fmt.Printf("  %s\n", m.Name)
			if m.Doc != "" {
				fmt.Printf("    %s\n", firstLine(m.Doc))
			}
		}
		fmt.Println("\nRun `magus describe module <name>` for a module's methods and signatures.")
		return nil
	}

	printModuleDetail(os.Stdout, out[0])
	return nil
}

func printModuleDetail(w io.Writer, m types.ModuleEntry) {
	fmt.Fprintf(w, "module: %s\n", m.Name)
	if m.Doc != "" {
		fmt.Fprintf(w, "  %s\n", m.Doc)
	}
	fmt.Fprintln(w)
	if len(m.Fields) > 0 {
		fmt.Fprintf(w, "fields (%d):\n", len(m.Fields))
		for _, f := range m.Fields {
			fmt.Fprintf(w, "  %s: %s", f.Name, f.Type)
			if f.Doc != "" {
				fmt.Fprintf(w, "  - %s", firstLine(f.Doc))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "methods (%d):\n", len(m.Methods))
	printModuleMethods(w, m.Methods)
	if len(m.Objects) == 0 {
		return
	}
	var methods []types.ModuleMethodEntry
	for _, o := range m.Objects {
		methods = append(methods, o.Methods...)
	}
	fmt.Fprintf(w, "\nobject methods (%d):\n", len(methods))
	printModuleMethods(w, methods)
}

func printModuleMethods(w io.Writer, methods []types.ModuleMethodEntry) {
	for _, meth := range methods {
		fmt.Fprintf(w, "  %s\n", meth.Name)
		if meth.Doc != "" {
			fmt.Fprintf(w, "    %s\n", meth.Doc)
		}
		fmt.Fprintf(w, "    Signature: %s\n", meth.Buzz)
		if meth.BuzzStdlib != "" {
			fmt.Fprintf(w, "    (also in Buzz's stdlib: %s - the extra form is sandbox-aware)\n", meth.BuzzStdlib)
		}
	}
}
