// Package spell holds the engine-agnostic spell types and the built-in spell
// registry: the Spec / Target / Charm value types the Buzz spell engine speaks,
// kept free of engine imports so the type package stays a neutral boundary.
// Importers conventionally alias it `ispell` (it is one of three `spell` packages).
package spell

import (
	"fmt"
	"sort"
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

// PatchOp is one RFC 6902 operation over the argv array. Path/From are
// single-token JSON Pointers (RFC 6901) into the array — "/N" for an index, or
// "/-" (add only) for the append position. Value is the string element for
// add/replace/test; From is the source pointer for move/copy.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
	From  string `json:"from,omitempty"`
}

// Charm declares how one active charm modifies a target's argv: an ordered RFC
// 6902 JSON Patch applied over the base. Charms are element-level — whole-document
// (root, empty-path) replacement is rejected by ValidatePatch — so multiple active
// charms compose without one wiping another (see fork.resolveCharmArgs).
type Charm struct {
	Ops []PatchOp `json:"ops,omitempty"`
}

// ValidatePatch checks a charm's ops are well-formed: a known op name, a
// non-root single-rooted JSON Pointer path, and a 'from' pointer for move/copy.
// Rejecting the root path ("") is what enforces the element-level boundary —
// a charm rewrites individual args, never swaps the whole argv (let alone cmd).
func ValidatePatch(ops []PatchOp) error {
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

// Op is a single dispatchable surface of a spell — one tool-native Operation (see
// docs/operations.md). An op either forks a declared command — Cmd/Args run via
// PATH with no script VM, magus passing the command straight through to the tool —
// or, when Func is set, dispatches that exported spell function in-VM with the
// invoke request's Params. The two are mutually exclusive: an empty Func means a
// fork op (Cmd may itself be empty, for a no-op marker op). Charms maps a charm
// name to a Charm; the patches of the active charms are concatenated in
// sorted-name order and applied over the base argv (see fork.resolveCharmArgs).
// Capture makes the op's magusfile method return the {stdout, stderr, code, ok}
// record (the same shape os.exec returns) instead of void — for ops whose output
// is the point (a hash, a revision date) rather than a build action whose exit
// code is all that matters.
type Op struct {
	Capture bool     `json:"capture,omitempty"`
	Cmd     string   `json:"cmd,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Func names an exported spell function (Buzz/Teal) to dispatch in-VM with the
	// invoke request's Params, returning its Data. When empty the op is a fork
	// target (Cmd/Args); when set, Cmd is unused.
	Func   string           `json:"fn,omitempty"`
	Charms map[string]Charm `json:"charms,omitempty"`
	// Doc is the handler function's documentation comment (see buzz Chunk.Doc),
	// surfaced by `magus describe` and enforced by `magus doctor` for local Buzz
	// spells. Empty for fork built-ins (their Doc is not serialized in bytecode)
	// and for Teal spells (no comment-capture). omitempty keeps it out of
	// BuiltinsHash so the cache key is unaffected.
	Doc string `json:"doc,omitempty"`
}

// Spec is a spell's static description. For built-ins it is produced by compiling
// each magus/spells/<name>/spell.buzz to bytecode (go:generate magus-spells-gen),
// embedding the blob, and resolving its mgs_ functions at load time.
type Spec struct {
	Name        string              `json:"name"`
	Needs       []string            `json:"needs,omitempty"`
	Claims      []string            `json:"claims,omitempty"`
	Provides    []string            `json:"provides,omitempty"`
	Opaque      bool                `json:"opaque,omitempty"`
	TargetNeeds map[string][]string `json:"target_needs,omitempty"`
	Ops         map[string]Op       `json:"targets,omitempty"`
	// VersionCmd argv prints the spell's toolchain version, mixed into the cache key; empty = no probe.
	VersionCmd []string `json:"version_cmd,omitempty"`
	// DocOps names the ops authored as function handlers (sorted) — as opposed to
	// plain {cmd,args} record ops. `magus doctor` requires a doc comment on each of
	// these for a workspace-local Buzz spell. Not serialized: it is a resolution-path
	// fact (which authoring form an op used), not part of the spell's cache identity,
	// so it stays out of BuiltinsHash.
	DocOps []string `json:"-"`
}

// OpNames returns the spell's op names in sorted order.
func (s Spec) OpNames() []string {
	names := make([]string, 0, len(s.Ops))
	for name := range s.Ops {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
