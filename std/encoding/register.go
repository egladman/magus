// Package encoding aggregates the nine text-codec host modules that live
// under std/encoding/*: base64, csv, hex, ini, json, toml, url, xml, yaml.
//
// Each of the nine uses none of std's shared sandbox/exec helpers
// (resolvePath, checkRead, checkWrite, optStringDefault, ...): every helper a
// text codec needs is local to its own file, so splitting them into their
// own leaf packages hides real behavior behind a real package boundary
// instead of just renaming a file. The modules that do NOT have this property
// (fs, http, crypto, env, path, os, vcs, archive) keep their shared helpers in
// std: those helpers ARE the sandbox enforcement layer, so pulling any one of
// them out would force exporting resolvePath/checkRead/checkWrite onto the
// public API, the wrong direction to move right after fixing bypasses of
// exactly those checks.
//
// This package's Module values reach std's own registry vocabulary (the
// std.Module type) by importing std, which is the only direction that works:
// std/encoding needs the descriptor types, but std cannot import std/encoding
// back to collect these nine, or the two packages would cycle. So unlike the
// modules living flat in std, which std lists in its own registry, these nine
// are not named there. Each leaf's register.go exposes a plain Modules()
// function (see e.g. std/encoding/json/register.go), this file aggregates all
// nine explicitly, and the union with std's own All()/Get() is computed one
// layer further up, in internal/hostmodules, the first package in the import
// graph that imports both std and std/encoding. That is also why this package
// exports only Modules()/Get(): nothing here needs std's registry at all.
package encoding

import (
	"fmt"

	"github.com/egladman/magus/std"
	"github.com/egladman/magus/std/encoding/base64"
	"github.com/egladman/magus/std/encoding/csv"
	"github.com/egladman/magus/std/encoding/hex"
	"github.com/egladman/magus/std/encoding/ini"
	"github.com/egladman/magus/std/encoding/json"
	"github.com/egladman/magus/std/encoding/toml"
	"github.com/egladman/magus/std/encoding/url"
	"github.com/egladman/magus/std/encoding/xml"
	"github.com/egladman/magus/std/encoding/yaml"
)

// leafSets lists every std/encoding leaf's contribution, one entry per
// directory. Adding a tenth leaf package means adding one line here, the same
// kind of single, auditable edit as std/module.go's portable list.
//
// TestModulesMatchDirectories (in register_test.go) fails if a leaf directory
// exists here without a matching entry, or an entry here without a matching
// directory, so the omission cannot pass silently the way it could with a
// blank-import list.
var leafSets = []func() []std.Module{
	base64.Modules,
	csv.Modules,
	hex.Modules,
	ini.Modules,
	json.Modules,
	toml.Modules,
	url.Modules,
	xml.Modules,
	yaml.Modules,
}

// Modules returns every module std/encoding contributes to magus's host
// module set. Each is validated exactly as std's own registry validates the
// modules it lists (see std.ValidateModule's doc for why this package cannot
// use that registry).
//
// A duplicate Name across leaves panics for the same reason std's registry
// does: two modules answering to the same bare import name is a program
// error, not a runtime one.
func Modules() []std.Module {
	seen := map[string]bool{}
	var out []std.Module
	for _, leaf := range leafSets {
		for _, m := range leaf() {
			if err := std.ValidateModule(m); err != nil {
				panic(fmt.Sprintf("std/encoding: module %q: %s", m.Name, err))
			}
			if seen[m.Name] {
				panic(fmt.Sprintf("std/encoding: duplicate module registration: %q", m.Name))
			}
			seen[m.Name] = true
			out = append(out, m)
		}
	}
	return out
}

// Get looks up one std/encoding module by name.
func Get(name string) (std.Module, bool) {
	for _, m := range Modules() {
		if m.Name == name {
			return m, true
		}
	}
	return std.Module{}, false
}
