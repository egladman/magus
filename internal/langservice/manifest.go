// Package langservice provides editor language features - completion and hover -
// for Buzz magusfiles, driven by a build-time snapshot of the magus host module
// surface (see cmd/langservice-manifest and manifest_data.go). It is pure
// computation with no host, filesystem, or process access, so it compiles into
// the browser playground wasm alongside the interpreter.
//
// Diagnostics are deliberately not here: they reuse the dry evaluator's session
// and live in internal/dry (dry.Diagnostics). This package covers the
// symbol-facing features that need the module manifest instead of a live session.
package langservice

// Method is one callable on a module: its Buzz-surface name, one-line doc, and
// rendered call signature (e.g. "glob(pattern: str) > [str]").
type Method struct {
	Name string
	Doc  string
	Sig  string
}

// Field is a static, table-level value on a module (e.g. vcs.name), read without
// a call.
type Field struct {
	Name string
	Type string
	Doc  string
}

// Module is one importable magus host module and its surface, as captured by the
// manifest generator. The manifest is the full authoring surface - every module a
// magusfile may reference - independent of which modules actually execute in the
// browser. Which ones run there is decided at runtime by ExcludedModules against the
// interpreter's real registration, not baked in here.
type Module struct {
	Name    string
	Doc     string
	Fields  []Field
	Methods []Method
}

// moduleIndex maps a module's bare import name to its manifest entry, built once
// from the generated modules slice.
var moduleIndex = func() map[string]Module {
	m := make(map[string]Module, len(modules))
	for _, mod := range modules {
		m[mod.Name] = mod
	}
	return m
}()

// Modules returns the manifest module set (sorted by name by the generator).
func Modules() []Module { return modules }

// LookupModule returns the module registered under its bare import name.
func LookupModule(name string) (Module, bool) {
	m, ok := moduleIndex[name]
	return m, ok
}

// ExcludedModules returns the manifest modules NOT in available - the host modules
// a magusfile can name but that don't run in the browser playground (they need a
// process, filesystem, or network). The caller passes the set the interpreter
// actually registered (dry.PlaygroundHostModules), so the excluded list is derived
// from real wiring rather than a hand-kept flag - a module wired into the playground
// simply never appears here. The playground renders the result as a "not available
// here" notice.
func ExcludedModules(available []string) []Module {
	inPlayground := make(map[string]bool, len(available))
	for _, name := range available {
		inPlayground[name] = true
	}
	var out []Module
	for _, m := range modules {
		if !inPlayground[m.Name] {
			out = append(out, m)
		}
	}
	return out
}
