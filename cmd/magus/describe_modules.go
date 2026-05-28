package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/std"
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

	spec, err := outputSpecOrDefault()
	if err != nil {
		return err
	}

	var name string
	if len(rest) > 0 {
		name = rest[0]
	}
	out := buildModulesOutput(name)
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

	switch spec.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(spec, out)
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
		if meth.NativeBuzz != "" {
			fmt.Printf("    (also in Buzz's stdlib: %s — the extra form is sandbox-aware)\n", meth.NativeBuzz)
		}
	}
	return nil
}

// buildModulesOutput assembles the describe-modules result. With name == "" it
// lists every module (summary only); with a name it returns just that module with
// its fields and methods populated (or an empty Modules slice if unknown).
func buildModulesOutput(name string) types.ModulesOutput {
	mods := std.All()
	slices.SortFunc(mods, func(a, b std.Module) int { return strings.Compare(a.Name, b.Name) })

	out := types.ModulesOutput{Definition: types.ModuleDefinition}
	for _, m := range mods {
		if name != "" && m.Name != name {
			continue
		}
		entry := types.ModuleEntry{Name: m.Name, Doc: m.Doc}
		if name != "" {
			for _, f := range m.Fields {
				entry.Fields = append(entry.Fields, types.ModuleFieldEntry{
					Name: f.Name, Type: f.Type.GoType(), Doc: f.Doc,
				})
			}
			for _, meth := range m.Methods {
				me := types.ModuleMethodEntry{
					Name: meth.Name,
					Doc:  meth.Doc,
					Buzz: std.BuzzSignature(m, meth),
				}
				if equiv, dup := std.NativeBuzzEquiv(m.Name, meth.Name); dup {
					me.NativeBuzz = equiv
				}
				entry.Methods = append(entry.Methods, me)
			}
		}
		out.Modules = append(out.Modules, entry)
	}
	out.Count = len(out.Modules)
	return out
}
