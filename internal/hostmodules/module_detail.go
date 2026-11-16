package hostmodules

import (
	"fmt"
	"io"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/types"
)

// PrintModuleDetail renders one module: its fields, methods and object methods.
func PrintModuleDetail(w io.Writer, m types.ModuleEntry) {
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
				fmt.Fprintf(w, "  - %s", describe.FirstLine(f.Doc))
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
