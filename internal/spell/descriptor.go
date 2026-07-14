// Package spell holds the engine-agnostic spell types and the built-in spell
// registry: the Descriptor / Target / Charm value types the Buzz spell engine speaks,
// kept free of engine imports so the type package stays a neutral boundary.
// Importers conventionally alias it `ispell` (it is one of three `spell` packages).
package spell

import (
	"fmt"
	"sort"

	"github.com/egladman/magus/types"
)

// JSON Patch (RFC 6902) operation names. A charm is an ordered patch applied
// over the target's base argv, treated as a JSON array of strings.
const (
	OpAdd     = "add"
	OpRemove  = "remove"
	OpReplace = "replace"
	OpMove    = "move"
	OpCopy    = "copy"
	OpTest    = "test"
)

// The spell value types — PatchOp, Charm, Run, and the resolved SpellOp — live in
// the neutral types package. PatchOp/Charm/Run are mirrored to Buzz objects by
// magus-utils types (the Go struct is the single source of truth) and must sit in
// types so the generator can reflect them without importing this package, which
// would form an embed/codegen cycle; SpellOp (Go-internal, no Buzz mirror) joins
// its family there since it embeds Run. They are referenced as types.* throughout —
// no spell-local aliases.

// ValidatePatch checks a charm's ops are well-formed: a known op name, a
// non-root single-rooted JSON Pointer path, and a 'from' pointer for move/copy.
// Rejecting the root path ("") is what enforces the element-level boundary —
// a charm rewrites individual args, never swaps the whole argv (let alone cmd).
func ValidatePatch(ops []types.PatchOp) error {
	for i, op := range ops {
		switch op.Op {
		case OpAdd, OpRemove, OpReplace, OpMove, OpCopy, OpTest:
		default:
			return fmt.Errorf("magus/spell: op %d: unknown JSON Patch op %q", i, op.Op)
		}
		if op.Path == "" {
			return fmt.Errorf("magus/spell: op %d (%s): empty path targets the whole argv; charms edit elements, not the whole argv", i, op.Op)
		}
		if op.Path[0] != '/' {
			return fmt.Errorf("magus/spell: op %d (%s): path %q must begin with %q", i, op.Op, op.Path, "/")
		}
		if op.Op == OpMove || op.Op == OpCopy {
			if op.From == "" || op.From[0] != '/' {
				return fmt.Errorf("magus/spell: op %d (%s): requires a 'from' pointer beginning with %q", i, op.Op, "/")
			}
		}
	}
	return nil
}

// Descriptor is a spell's static description. For built-ins it is produced by
// compiling each spells/<name>/spell.buzz to bytecode (go:generate
// magus-utils spells), embedding the blob, and resolving its mgs_ functions at load
// time.
type Descriptor struct {
	Name        string                   `json:"name"`
	Needs       []string                 `json:"needs,omitempty"`
	Claims      []string                 `json:"claims,omitempty"`
	Provides    []string                 `json:"provides,omitempty"`
	Opaque      bool                     `json:"opaque,omitempty"`
	TargetNeeds map[string][]string      `json:"target_needs,omitempty"`
	Ops         map[string]types.SpellOp `json:"targets,omitempty"`
	// VersionCmd argv prints the spell's toolchain version, mixed into the cache key; empty = no probe.
	VersionCmd []string `json:"version_cmd,omitempty"`
	// Language is the canonical source language this spell adapts (e.g. "go",
	// "typescript"), declared by mgs_getLanguage. It tags the spell node so a
	// `language:` query groups the adapter with the files and symbols of that language;
	// empty for a spell that adapts no single source language (docker, cosign).
	Language string `json:"language,omitempty"`
	// DocOps names the ops authored as function handlers (sorted) — as opposed to
	// plain {cmd,args} record ops. `magus doctor` requires a doc comment on each of
	// these for a workspace-local Buzz spell. Not serialized: it is a resolution-path
	// fact (which authoring form an op used), not part of the spell's cache identity,
	// so it stays out of BuiltinsHash.
	DocOps []string `json:"-"`
}

// OpNames returns the spell's op names in sorted order.
func (d Descriptor) OpNames() []string {
	names := make([]string, 0, len(d.Ops))
	for name := range d.Ops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ServiceOpNames returns the names of the spell's service ops (sorted). A service
// op runs a long-running process, so its target is never cached.
func (d Descriptor) ServiceOpNames() []string {
	var names []string
	for name, op := range d.Ops {
		if op.IsService() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
