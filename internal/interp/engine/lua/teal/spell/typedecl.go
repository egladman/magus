package spell

import (
	"sort"
	"strings"
	"sync"

	ispell "github.com/egladman/magus/internal/spell"
)

// isTealIdent reports whether s is a valid Teal/Lua identifier and so can be a
// bare record field. Op names that aren't (e.g. the kebab "golangci-lint") are
// emitted with Teal's ["field"]: type syntax instead, and reached from a
// magusfile by subscript: go["golangci-lint"]().
func isTealIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// reservedFields are the MagusSpell spec fields; an operation whose name
// collides with one of these is skipped when emitting op-method fields so the
// generated record stays well-formed.
var reservedFields = map[string]bool{
	"name": true, "claims": true, "opaque": true, "version_cmd": true,
	"needs": true, "provides": true, "depends_on": true,
	"target_needs": true, "ops": true,
	"listTargets": true,
}

// staticFields are the authoring fields both spell records carry, ahead of the
// per-op methods.
const staticFields = `   name:         string
   claims:       {string}
   opaque:       boolean
   version_cmd:  {string}
   needs:        function(dir: string): {string}
   provides:     function(): {string}
   depends_on:   function(dir: string): {string}
   target_needs: {string : {string}}
   ops:          {string : OpSpec}
   listTargets:  function(): {string}
`

// opMethodType is the Teal type of every op method: it takes optional TargetOpts
// and returns the ExecRecord of a captured op (non-captured ops return nil at
// runtime, which Teal callers ignore by not assigning).
const opMethodType = "function(opts?: TargetOpts): ExecRecord"

// SpellTypeDecl returns the Teal declarations for the two spell record types,
// generated from the canonical spec so the typed surface for
// require("magus.spell.<name>") can never drift from the built-in registry.
//
//   - MagusSpell types built-in spell requires (require("magus.spell.go")). It is
//     closed: the authoring fields plus one op-method field per tool-native op,
//     unioned across every built-in, so an op no built-in declares is a compile
//     error. Op names follow the CLI command (docs/spells.md) and are mostly
//     kebab-case, so they are emitted as ["op"] fields and reached by subscript
//     (go["golangci-lint"]()).
//   - WorkspaceSpell types workspace-local spell requires (require("spells.foo")).
//     A local spell's ops are not known at type-gen time and follow the same
//     CLI-command convention, so this record carries the authoring fields plus a
//     metamethod __index fallback that types any op access (foo["mytool"]()).
//
// Both are emitted as global records (forward-resolvable) appended to the preamble.
var SpellTypeDecl = sync.OnceValue(func() string {
	spec := ispell.Builtins()
	ops := make(map[string]struct{})
	for _, m := range spec {
		for op := range m.Targets {
			if !reservedFields[op] {
				ops[op] = struct{}{}
			}
		}
	}
	opNames := make([]string, 0, len(ops))
	for op := range ops {
		opNames = append(opNames, op)
	}
	sort.Strings(opNames)

	var b strings.Builder

	b.WriteString("global record MagusSpell\n")
	b.WriteString(staticFields)
	for _, op := range opNames {
		b.WriteString("   ")
		if isTealIdent(op) {
			b.WriteString(op)
		} else {
			b.WriteString(`["`)
			b.WriteString(op)
			b.WriteString(`"]`)
		}
		b.WriteString(": ")
		b.WriteString(opMethodType)
		b.WriteByte('\n')
	}
	b.WriteString("end\n")

	// WorkspaceSpell deliberately omits the map-typed authoring fields (ops,
	// target_needs): with a {string:_} field present the Teal checker resolves a
	// string-literal subscript against the record and ignores __index, which would
	// defeat the op fallback. name + listTargets cover what a magusfile reads off a
	// local handle; everything else is reached through __index.
	b.WriteString("global record WorkspaceSpell\n")
	b.WriteString("   name:        string\n")
	b.WriteString("   listTargets: function(): {string}\n")
	b.WriteString("   metamethod __index: function(self: WorkspaceSpell, op: string): ")
	b.WriteString(opMethodType)
	b.WriteByte('\n')
	b.WriteString("end\n")

	return b.String()
})
