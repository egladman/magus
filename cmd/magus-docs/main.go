// Command magus-docs generates Markdown documentation for every module
// registered in the host package. Run it manually to refresh the committed docs:
//
//	go run ./cmd/magus-docs -out ./docs/modules
//
// The output under docs/modules is committed; re-run this tool after
// changing a host module's bindings to keep the docs in sync.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/std"

	// Blank imports so all module init() functions run, populating std.All().
	_ "github.com/egladman/magus/std"
)

func main() {
	outDir := flag.String("out", "docs/modules", "output directory for module docs")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "magus-docs: mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	modules := std.All()
	slices.SortFunc(modules, func(a, b std.Module) int {
		return strings.Compare(a.Name, b.Name)
	})

	for _, m := range modules {
		if err := writeModule(*outDir, m); err != nil {
			fmt.Fprintf(os.Stderr, "magus-docs: %s: %v\n", m.Name, err)
			os.Exit(1)
		}
	}

	if err := writeIndex(*outDir, modules); err != nil {
		fmt.Fprintf(os.Stderr, "magus-docs: index: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "magus-docs: wrote %d module docs to %s\n", len(modules), *outDir)
}

func writeModule(dir string, m std.Module) error {
	return os.WriteFile(filepath.Join(dir, m.Name+".md"), []byte(renderModule(m)), 0o644)
}

func renderModule(m std.Module) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# `%s`\n\n", m.Name)
	if m.Doc != "" {
		fmt.Fprintf(&b, "%s\n\n", m.Doc)
	}

	// Naming convention note: each module is imported under its bare name, with
	// methods in camelCase.
	fmt.Fprintf(&b, "> **Naming convention:** import the module under its bare name "+
		"(`import \"%s\"`) and call methods in `camelCase` (`%s.someMethod`).\n\n",
		m.Name, m.Name)

	if len(m.Fields) > 0 {
		fmt.Fprintf(&b, "## Fields\n\n")
		fmt.Fprintln(&b, "| Field | Type | Description |")
		fmt.Fprintln(&b, "|-------|------|-------------|")
		for _, f := range m.Fields {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", f.Name, f.Type.GoType(), f.Doc)
		}
		b.WriteByte('\n')
	}

	if len(m.Methods) > 0 {
		fmt.Fprintf(&b, "## Methods\n\n")
		for _, meth := range m.Methods {
			fmt.Fprintf(&b, "### `%s`\n\n", meth.Name)
			if meth.Doc != "" {
				fmt.Fprintf(&b, "%s\n\n", meth.Doc)
			}
			fmt.Fprintf(&b, "**Signature:** `%s`\n\n", host.BuzzSignature(m, meth))
			if equiv, dup := host.BuzzStdlibEquiv(m.Name, meth.Name); dup {
				fmt.Fprintf(&b, "**Also in Buzz's stdlib:** `%s` — the magus form is sandbox-aware.\n\n", equiv)
			}
			if len(meth.Args) > 0 {
				fmt.Fprintln(&b, "| Parameter | Type | Optional | Description |")
				fmt.Fprintln(&b, "|-----------|------|----------|-------------|")
				for _, a := range meth.Args {
					opt := ""
					if a.Optional {
						opt = "yes"
					}
					fmt.Fprintf(&b, "| `%s` | `%s` | %s | |\n", a.Name, a.Type.GoType(), opt)
				}
				fmt.Fprintln(&b)
			}
			if len(meth.Returns) > 0 {
				rets := make([]string, len(meth.Returns))
				for i, r := range meth.Returns {
					if r.Name != "" {
						rets[i] = r.Name + " " + r.Type.GoType()
					} else {
						rets[i] = r.Type.GoType()
					}
				}
				fmt.Fprintf(&b, "**Returns:** %s\n\n", strings.Join(rets, ", "))
			}
		}
	}

	return b.String()
}

func writeIndex(dir string, modules []std.Module) error {
	return os.WriteFile(filepath.Join(dir, "index.md"), []byte(renderIndex(modules)), 0o644)
}

func renderIndex(modules []std.Module) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Magusfile Module Reference\n\n")
	fmt.Fprintf(&b, "These are the runtime utility modules. Import each under its bare name — `import \"fs\"`, then `fs.glob(...)` — with `camelCase` methods. magus layers these host methods onto Buzz's own stdlib, so a single `import \"fs\"` (or `os`, `crypto`) carries both surfaces, and the magus forms are sandbox-aware where Buzz's bare stdlib is not. Some methods also exist in Buzz's own stdlib (noted per-method); either works.\n\n")
	fmt.Fprintln(&b, "| Module | Description |")
	fmt.Fprintln(&b, "|--------|-------------|")
	for _, m := range modules {
		fmt.Fprintf(&b, "| [`%s`](%s.md) | %s |\n", m.Name, m.Name, m.Doc)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "## See also\n\n")
	return b.String()
}
