package spells

import (
	"fmt"
	"sort"
)

// PatchOpKind is one JSON Patch (RFC 6902) operation name. A charm is an ordered
// patch applied over the target's base argv, treated as a JSON array of strings.
//
// It is a defined type rather than a bare string so the Buzz mirror can declare it as
// an enum: charm.buzz used to write `op = "add"` by hand at every constructor, where a
// typo produced a patch that failed validation at load with a message naming the value
// rather than the line. `PatchOpKind.add` is checked when the spell compiles.
type PatchOpKind string

const (
	// OpNone is the zero value. It is not a valid operation - ValidatePatch rejects
	// it - and exists so the mirror's enum has a default case to name.
	OpNone    PatchOpKind = ""
	OpAdd     PatchOpKind = "add"
	OpRemove  PatchOpKind = "remove"
	OpReplace PatchOpKind = "replace"
	OpMove    PatchOpKind = "move"
	OpCopy    PatchOpKind = "copy"
	OpTest    PatchOpKind = "test"
)

// The spell value types - PatchOp, Charm, Command, Service, and the resolved Op -
// live HERE, in package spells, beside the Descriptor that carries them. They used
// to sit in types, referenced as types.* throughout, on the theory that magus-utils
// types needed a neutral package to reflect from without an embed/codegen cycle;
// the generator reads spells.* directly instead, and the cycle never materialized.
// (Run is the old name for Command, and has not existed since the op surface
// collapsed to one kind.)
//
// See doc.go for why the move happened and why the dependency runs one way.

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

// Manifest is one manifest an ecosystem declares: the file a project's dependencies
// are declared in (go.mod, package.json, Cargo.toml, pyproject.toml), plus the
// lockfiles that ecosystem resolves them into.
//
// Value keeps the field name Path uses so the two decode identically. mgs_listManifests
// returned [Path] before this type existed, and the decoder reads keys structurally, so
// a spell still returning Path values loads as a Manifest declaring no lockfile rather
// than failing. That is the whole compat story; there is no second decode path.
type Manifest struct {
	Value string `json:"value"`
	// LockCandidates names the lockfiles this ecosystem MIGHT resolve Value into, of
	// which exactly one will exist - not the lockfiles a project has. package.json
	// resolves into any of pnpm-lock.yaml, package-lock.json, npm-shrinkwrap.json,
	// yarn.lock or a bun lockfile depending on which package manager is in use, and
	// pyproject.toml into any of poetry.lock, pdm.lock, uv.lock or Pipfile.lock. Go and
	// Rust have exactly one each, which is what makes the plural easy to misread: a
	// consumer that takes the first element is right for go.sum and wrong for npm.
	//
	// Bare filenames rather than Paths, because a lockfile's DIRECTORY is not knowable
	// here. mgs_ functions run during spell discovery, before a project exists, and a
	// workspace hoists one lockfile to its root to serve many manifests. Which
	// directory holds the live one is resolved by walking up from the project, not
	// declared. (Locks are also relative to nothing, so Path's base - the reason Path
	// beats a string elsewhere - has nothing to carry.)
	LockCandidates []string `json:"lock_candidates,omitempty" buzz:"lockCandidates"`
}

// Descriptor is a spell's static description. For built-ins it is produced by
// compiling each spells/<name>/spell.buzz to bytecode (go:generate
// magus-utils spells), embedding the blob, and resolving its mgs_ functions at load
// time.
type Descriptor struct {
	Name     string   `json:"name"`
	Needs    []string `json:"needs,omitempty"`
	Claims   []string `json:"claims,omitempty"`
	Provides []string `json:"provides,omitempty"`
	// IgnoreDirs names non-source directories this spell's ecosystem generates
	// (vendor, node_modules, target, __pycache__) so the input-hashing walk prunes
	// them per-project instead of the engine hardcoding language-specific names.
	// Dot-directories are already skipped structurally, so only non-dot names belong
	// here. Declared by mgs_listIgnoreDirs.
	IgnoreDirs []string `json:"ignore_dirs,omitempty"`
	// Manifests is the ordered list of candidate manifests this spell's ecosystem
	// declares. Ordered because some ecosystems have genuine alternatives - the first
	// one present in a project directory is its manifest. Declared by
	// mgs_listManifests. Distinct from Needs (cache/affected input globs), from a
	// spell's DeclarationFiles (project discovery, not exposed on Descriptor), and
	// from VersionCmd (the toolchain's own version, not the project's).
	Manifests   []Manifest          `json:"manifests,omitempty"`
	Opaque      bool                `json:"opaque,omitempty"`
	TargetNeeds map[string][]string `json:"target_needs,omitempty"`
	Ops         map[string]Op       `json:"targets,omitempty"`
	// Tools is every binary this spell drives, keyed by the bin name an op names in
	// its Command - so no op restates which tool it runs, and everything magus knows
	// about that binary sits in one place.
	//
	// It replaces five separate declarations (a primary probe, named probes, a primary
	// key, named keys, readiness) split across two axes that were never orthogonal.
	// The split also hid its own subtleties: govulncheck declaring no cache key is a
	// deliberate choice, and in two parallel maps that reads as an absence nobody
	// notices rather than a decision someone made.
	//
	// There is no privileged "primary" tool. `go` had one only for historical cache-key
	// reasons, and nothing principled distinguished it from golangci-lint - both are
	// binaries the spell drives, so both key the cache as spell:tool:version.
	Tools map[string]Tool `json:"tools,omitempty"`

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
