package hostbuzz

import (
	"slices"
	"strings"

	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// ModulesOutput is the single typed core behind both `magus describe modules`
// (the CLI formats it) and the native magus.modules()/magus.module() host methods
// (which marshal it to Buzz via ModuleEntry.Record). With name == "" it returns
// every module as a summary (name + doc); with a name it returns just that module
// with its fields and methods (and per-method Buzz signatures) populated, or an
// empty Modules slice if the name is unknown. Routing both surfaces through this
// one function is what guarantees they can't drift.
func ModulesOutput(name string) types.ModulesOutput {
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
					Buzz: BuzzSignature(m, meth),
				}
				if equiv, dup := NativeBuzzEquiv(m.Name, meth.Name); dup {
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
