package main

import (
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// describeModules implements `magus describe modules` (list) and
// `magus describe module <name>` (detail). Modules are static — the magus
// standard library — so this needs no workspace.
func describeModules(args []string) error {
	rest, err := cmdParse("describe modules", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe module[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ModuleDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no name, lists every module; with a name, prints its methods")
			fmt.Fprintln(os.Stderr, "with Buzz signatures.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
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
	out := host.ModulesOutput(name)
	if name != "" && len(out.Modules) == 0 {
		mods := std.All()
		names := make([]string, len(mods)) // module names, sorted for a stable suggestion
		for i, m := range mods {
			names[i] = m.Name
		}
		slices.Sort(names)
		msg := fmt.Sprintf("magus describe module: unknown module %q", name)
		if sug := interactive.SuggestNearest(name, names); sug != "" {
			msg += fmt.Sprintf("; did you mean %q?", sug)
		}
		fmt.Fprintln(os.Stderr, msg)
		return errSilent{exitCode: 2}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, m := range out.Modules {
			fmt.Println(m.Name)
		}
		return nil
	}

	// text / wide
	if name == "" {
		fmt.Printf("definition: %s\n\n", out.Definition)
		fmt.Printf("modules (%d):\n", out.Count)
		for _, m := range out.Modules {
			fmt.Printf("  %s\n", m.Name)
			if m.Doc != "" {
				fmt.Printf("    %s\n", firstLine(m.Doc))
			}
		}
		fmt.Println("\nRun `magus describe module <name>` for a module's methods and signatures.")
		return nil
	}

	// detail (single module)
	m := out.Modules[0]
	fmt.Printf("module: %s\n", m.Name)
	if m.Doc != "" {
		fmt.Printf("  %s\n", m.Doc)
	}
	fmt.Println()
	if len(m.Fields) > 0 {
		fmt.Printf("fields (%d):\n", len(m.Fields))
		for _, f := range m.Fields {
			fmt.Printf("  %s: %s", f.Name, f.Type)
			if f.Doc != "" {
				fmt.Printf("  — %s", firstLine(f.Doc))
			}
			fmt.Println()
		}
		fmt.Println()
	}
	fmt.Printf("methods (%d):\n", len(m.Methods))
	for _, meth := range m.Methods {
		fmt.Printf("  %s\n", meth.Name)
		if meth.Doc != "" {
			fmt.Printf("    %s\n", meth.Doc)
		}
		fmt.Printf("    Signature: %s\n", meth.Buzz)
		if meth.BuzzStdlib != "" {
			fmt.Printf("    (also in Buzz's stdlib: %s — the extra form is sandbox-aware)\n", meth.BuzzStdlib)
		}
	}
	return nil
}
