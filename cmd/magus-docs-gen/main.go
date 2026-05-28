// Command magus-docs-gen generates Markdown documentation for every module
// registered in the host package. Run it manually to refresh the committed docs:
//
//	go run ./magus/cmd/magus-docs-gen -out ./magus/docs/modules
//
// The output under magus/docs/modules is committed; re-run this tool after
// changing a host module's bindings to keep the docs in sync.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/std"

	// Blank imports so all module init() functions run, populating std.All().
	_ "github.com/egladman/magus/internal/std"
)

func main() {
	outDir := flag.String("out", "docs/modules", "output directory for module docs")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "magus-docs-gen: mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	modules := std.All()
	slices.SortFunc(modules, func(a, b std.Module) int {
		return strings.Compare(a.Name, b.Name)
	})

	for _, m := range modules {
		if err := writeModule(*outDir, m); err != nil {
			fmt.Fprintf(os.Stderr, "magus-docs-gen: %s: %v\n", m.Name, err)
			os.Exit(1)
		}
	}

	if err := writeIndex(*outDir, modules); err != nil {
		fmt.Fprintf(os.Stderr, "magus-docs-gen: index: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "magus-docs-gen: wrote %d module docs to %s\n", len(modules), *outDir)
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

	// Naming convention note: Buzz reaches each module off the `import
	// "magus/extra"` aggregate in camelCase.
	fmt.Fprintf(&b, "> **Naming convention:** Buzz reaches modules off the "+
		"`import \"magus/extra\"` aggregate in `camelCase` (`extra.%s.someMethod`).\n\n",
		m.Name)

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
			fmt.Fprintf(&b, "**Signature:** `%s`\n\n", std.BuzzSignature(m, meth))
			if equiv, dup := std.NativeBuzzEquiv(m.Name, meth.Name); dup {
				fmt.Fprintf(&b, "**Also in Buzz's stdlib:** `%s` — the `extra` form is sandbox-aware.\n\n", equiv)
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
	fmt.Fprintf(&b, "These are the runtime utility modules. Take the single `import \"magus/extra\"` aggregate and reach modules off it — `extra.fs.glob(...)` — with `camelCase` methods. Each module is **self-complete**: `extra` carries a whole domain in one place (so you never straddle native `fs` and `extra.fs`), and the `extra` forms are sandbox-aware where Buzz's bare stdlib is not. Some methods also exist in Buzz's own stdlib (noted per-method); either works.\n\n")
	fmt.Fprintln(&b, "| Module | Description |")
	fmt.Fprintln(&b, "|--------|-------------|")
	for _, m := range modules {
		fmt.Fprintf(&b, "| [`%s`](%s.md) | %s |\n", m.Name, m.Name, m.Doc)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "## See also\n\n")
	return b.String()
}
