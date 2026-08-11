package std

import (
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// DescribeModules is the single typed core behind both `magus describe modules`
// (the CLI formats it) and the native magus.modules()/magus.module() host methods
// (which marshal it to Buzz via ModuleEntry.BuzzObject). With name == "" it returns
// every module as a summary (name + doc); with a name it returns just that module
// with its fields and methods (and per-method Buzz signatures) populated, or an
// empty Modules slice if the name is unknown. Routing both surfaces through this
// one function is what guarantees they can't drift.
func DescribeModules(name string) []types.ModuleEntry {
	// Go modules and Buzz modules are ONE surface here, deliberately. This
	// function is what `magus describe modules`, the knowledge graph, the docs
	// site, REPL completion, MCP describe_kind and magus\describeModules all read,
	// so folding both kinds in at this one point is what makes a Buzz-implemented
	// module indistinguishable from a Go-implemented one everywhere it is seen.
	// Which language implements a stdlib module is an implementation detail, and
	// this is the line that keeps it one.
	mods := append(All(), SourceModulesAsModules()...)
	slices.SortFunc(mods, func(a, b Module) int { return strings.Compare(a.Name, b.Name) })

	var out []types.ModuleEntry
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
					Buzz: BuzzSignature(m, meth),
				}
				if equiv, dup := BuzzStdlibEquiv(m.Name, meth.Name); dup {
					me.BuzzStdlib = equiv
				}
				entry.Methods = append(entry.Methods, me)
			}
		}
		out = append(out, entry)
	}
	return out
}
