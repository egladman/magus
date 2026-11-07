// Package hostmodules is the union of every host module magus exposes to
// Buzz: std's own list (the modules living flat in std/*.go) plus std/encoding's nine explicitly-aggregated leaf packages
// (base64, csv, hex, ini, json, toml, url, xml, yaml).
//
// The union has to be computed here, one layer above std, rather than inside
// std itself: std/encoding imports std for the Module vocabulary (Module,
// Method, Arg, Ret, TypeTag), so std importing std/encoding back to collect
// its nine modules would cycle. This package imports both and is the ONLY
// place that does; see std/module.go's portable list and
// std/encoding/register.go's package doc for the two ends of that constraint.
//
// Every caller that needs "every host module magus has" (the CLI's describe
// command, magus\modules()/module(), the knowledge graph, the docs and
// bindings codegen, the manpage/manifest generators) reads through here
// rather than std.All()/std.Get() directly, so std.All() staying scoped to
// std's own set does not silently narrow what any of those callers reports.
package hostmodules

import (
	"fmt"

	"github.com/egladman/magus/std"
	"github.com/egladman/magus/std/encoding"
	termkitgen "github.com/egladman/magus/std/termkit/gen"
)

// All returns every host module: std's own set, std/encoding's aggregated
// set, and the terminal UI library's generated set.
func All() []std.Module {
	return append(append(std.All(), encoding.Modules()...), termkit()...)
}

// Get looks up a module by name across every set.
func Get(name string) (std.Module, bool) {
	if m, ok := std.Get(name); ok {
		return m, true
	}
	if m, ok := encoding.Get(name); ok {
		return m, true
	}
	for _, m := range termkit() {
		if m.Name == name {
			return m, true
		}
	}
	return std.Module{}, false
}

// termkit validates the generated modules as std validates its own, so a
// descriptor that disagrees with its Impl fails the first reader.
func termkit() []std.Module {
	mods := termkitgen.Modules()
	for _, m := range mods {
		if err := std.ValidateModule(m); err != nil {
			panic(fmt.Sprintf("std/termkit/gen: module %q: %s", m.Name, err))
		}
	}
	return mods
}
