// Package hostmodules is the union of every host module magus exposes to
// Buzz: std's own self-registering set (the 24 modules still living flat in
// std/*.go) plus std/encoding's nine explicitly-aggregated leaf packages
// (base64, csv, hex, ini, json, toml, url, xml, yaml).
//
// The union has to be computed here, one layer above std, rather than inside
// std itself: std/encoding imports std for the Module vocabulary (Module,
// Method, Arg, Ret, TypeTag), so std importing std/encoding back to collect
// its nine modules would cycle. This package imports both and is the ONLY
// place that does - see std.Register's doc and std/encoding/register.go's
// package doc for the two ends of that constraint.
//
// Every caller that needs "every host module magus has" - the CLI's describe
// command, magus\modules()/module(), the knowledge graph, the docs and
// bindings codegen, the manpage/manifest generators - reads through here
// rather than std.All()/std.Get() directly, so std.All() staying scoped to
// std's own 24 does not silently narrow what any of those surfaces reports.
package hostmodules

import (
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/std/encoding"
)

// All returns every host module: std's self-registered set plus
// std/encoding's explicitly aggregated set.
func All() []std.Module {
	return append(std.All(), encoding.Modules()...)
}

// Get looks up a module by name across both sets.
func Get(name string) (std.Module, bool) {
	if m, ok := std.Get(name); ok {
		return m, true
	}
	return encoding.Get(name)
}
