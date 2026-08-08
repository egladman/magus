package types

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/spells"
)

// workspaceScheme is the URI scheme every project reference renders as when
// piped back into a command: "workspace://<path>". The scheme is metadata,
// not display content, the same way "https://" is hidden in a browser bar -
// human-facing output strips it via the Display form.
const workspaceScheme = "workspace://"

// ProjectRef is the canonical project reference: holds the data once
// (workspace-relative path plus optional absolute dir) and exposes two
// render methods. One source of truth means the URI form and the display
// form cannot drift - they share the same fields and the same ProjectLabel
// / WorkspaceRef helpers both delegate to a method on this struct.
type ProjectRef struct {
	// Path is the workspace-relative identifier: "." for the root, "pkg/foo"
	// for a nested project. Always forward-slash, never absolute, never escapes.
	Path string `json:"path" yaml:"path"`
	// Name is the human label. It coincides with Path for a nested project and
	// diverges for the root, which reads as the workspace directory's base name
	// rather than a bare ".". Carried on the wire because consumers of
	// SymbolIndexStatus and friends have always been able to read it without
	// re-deriving it from a directory they do not have.
	Name string `json:"name" yaml:"name"`
	// Dir is the project's absolute directory ("" if unknown), used only to
	// derive Name for the root. Never serialized: it is a host-specific
	// absolute path, and every consumer of this type reads workspace-relative
	// data. Putting it on the wire would leak the user's directory layout.
	Dir string `json:"-" yaml:"-"`
}

// NewProjectRef builds a ProjectRef from a workspace-relative path and the
// project's absolute directory. The dir feeds the root display name; pass
// "" when it is not known.
func NewProjectRef(path, dir string) ProjectRef {
	r := ProjectRef{Path: path, Dir: dir}
	r.Name = r.Display()
	return r
}

// WorkspaceURI renders the project as a "workspace://<path>" reference -
// the machine-readable form users pipe back into commands (magus run,
// magus describe, magus query). An empty path is the workspace root, so
// error messages and structured output never print a bare "." or "".
func (r ProjectRef) WorkspaceURI() string {
	if r.Path == "" {
		return workspaceScheme + "."
	}
	return workspaceScheme + r.Path
}

// Display renders the project for human consumption: the bare path for
// nested projects, the dir basename for the root (so a bare "." never
// appears in logs or Mermaid labels), and "(workspace root)" as the final
// fallback. Use this in every place a human reads the path; use WorkspaceURI
// in every place the user is expected to copy it back into a command.
func (r ProjectRef) Display() string {
	if r.Path != "" && r.Path != "." {
		return r.Path
	}
	if r.Dir != "" {
		if base := filepath.Base(r.Dir); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return "(workspace root)"
}

// ProjectLabel is a convenience for the Display form when the caller only
// has a path and a dir, not a ProjectRef value. Routes through the struct so
// the two forms share one definition; add new rendering rules to Display,
// and ProjectLabel picks them up automatically.
func ProjectLabel(path, dir string) string {
	return ProjectRef{Path: path, Dir: dir}.Display()
}

// WorkspaceRef is a convenience for the WorkspaceURI form when the caller
// only has a path. The dir is intentionally absent: a URI is path-only, and
// Display's dir-based root naming has no place in the machine-readable form.
func WorkspaceRef(path string) string {
	return ProjectRef{Path: path}.WorkspaceURI()
}

// ProjectDisplayName returns an explicit display name when available,
// otherwise derives the shared never-dot label from the project path and
// directory. Used where a project carries a declared human name (a name:
// annotation in magusfile) that should win over the path-derived default.
func ProjectDisplayName(path, name, dir string) string {
	if name != "" && name != "." {
		return name
	}
	return ProjectLabel(path, dir)
}

// Binding is the per-spell registration state attached to a project.
// One Binding is created per WithSpell call.
type Binding struct {
	Name          string // spell identifier
	ClaimWeight   int    // higher wins on glob collision; ties fall back to last-wins
	AddedClaims   []string
	RemovedClaims []string
}

// ProjectOrigin is what put a project in the workspace. It is a named type rather
// than a bare string because its two cases are not interchangeable: OriginMagusfile
// is a whole value you compare, while a provided project's origin CARRIES the
// provider's name, so it has no single constant to compare against. Constructing
// and reading it through ProvidedBy and Provider keeps that asymmetry out of every
// call site - a hand-written `origin == "provider"` would be a condition that never
// fires.
type ProjectOrigin string

// OriginMagusfile is the origin of a directory discovery found a magusfile in.
const OriginMagusfile ProjectOrigin = "magusfile"

// originProviderPrefix tags an origin with the workspace provider that reported it.
const originProviderPrefix = "provider:"

// ProvidedBy returns the origin of a project a workspace provider reported. The
// spell name is part of the value because a workspace can wire more than one
// provider, and "which tool says this is a project" is the whole question a reader
// has about a directory with no magusfile in it.
func ProvidedBy(spellName string) ProjectOrigin {
	return ProjectOrigin(originProviderPrefix + spellName)
}

// Provider returns the spell that reported this project, and whether a provider
// reported it at all. It is the read half of ProvidedBy: the spell name lives inside
// the value, so a caller comparing against a bare "provider" would be writing a
// condition that never fires.
func (o ProjectOrigin) Provider() (string, bool) {
	name, ok := strings.CutPrefix(string(o), originProviderPrefix)
	return name, ok
}

// Project is the record magus maintains for every directory with a marker file.
type Project struct {
	Path string // repo-relative directory, forward slashes (e.g. "api", ".")
	// Name is the declared human label from magus.project's "name" key, or "" to
	// derive one from the path. It exists for the ROOT project, whose path is "."
	// and whose label would otherwise fall back to the checkout's directory
	// basename - so a worktree, a clone under a different name, or a CI checkout
	// each renamed the root project and rewrote every generated index that names
	// it. Declaring the name makes generated output reproducible anywhere.
	Name string
	// Origin is what put this project in the workspace (see ProjectOrigin). It is
	// PROVENANCE, never identity - nothing dispatches on it - and it exists because a
	// provided project has no file to point at, so "where did this come from" would
	// otherwise be unanswerable for exactly the projects a reader has never seen
	// declared anywhere. Named Origin rather than Source because Sources below is the
	// unrelated glob list, and one letter is not enough distance between "where this
	// project came from" and "the files it is built from".
	Origin    ProjectOrigin
	Dir       string // absolute filesystem path
	Spell     string // primary spell name; use Spells for fan-out dispatch
	Spells    []string
	Bindings  []*Binding // parallel to Spells, in registration order
	Sources   []string   // doublestar globs relative to Dir for the cache key
	Outputs   []string   // doublestar globs snapshotted into and replayed from cache
	DependsOn []string
	Exclusive bool
	// NoLanguage is the reason a project binds no toolchain spell ON PURPOSE, from
	// magus.project's "no_language" key. A spell-less project is legal and common, so
	// doctor's language-coverage check cannot tell an intentional one (a polyglot
	// harness no single pack describes) from a real gap (someone forgot to import the
	// go spell) without being told. Carrying the REASON rather than a bare bool is what
	// keeps the opt-out honest: it has to say what it is instead of silencing a check.
	NoLanguage     string
	WatchIgnores   []IgnorePattern
	TargetPolicies map[string]Target // per-target execution policy; values carry only the policy fields of Target
	// TargetInputs are per-target file inputs declared in a target body via
	// ctx.readsFiles(...), keyed by normalized target name (DefaultTargetNameNormalizer,
	// matching the TargetPolicies key space buildStep looks up). ONE representation
	// covers both a same-project glob and a cross-project file: each InputRef carries its
	// owning project (workspace-relative once resolved) and the glob/file relative to
	// that project. When present they DEFINE the target's file footprint: buildStep
	// retains the owning magusfiles and target-specific spell sources, then folds these
	// refs to workspace-relative globs via path.Join(Project, Rel). A cross-project input's owning project
	// is also unioned into DependsOn so a change to it marks this project affected; a
	// same-project input needs no such edge (it seeds by directory containment).
	// Populated statically at load from describe.Extract.
	TargetInputs map[string][]InputRef
	// TargetOutputs are per-target ctx.writesFiles refs. When a target has any, they
	// replace the broad project/spell output baseline for that target's replay set.
	TargetOutputs map[string][]OutputRef
	// TargetUpdates are per-target ctx.modifiesExistingFiles refs: existing files the
	// target changes in place.
	// Deliberately absent from AllOutputs, which is what makes magus clean skip them and
	// the cache neither snapshot nor replay them. See types.UpdateRef.
	TargetUpdates map[string][]UpdateRef
	// TargetExecOverrides are per-target ctx.withEnv / ctx.withCwd overrides, in
	// declaration order, folded into the cache key. See TargetGraphNode.ExecOverrides.
	TargetExecOverrides map[string][]string
	// MagusfileTargets are the target names this project's magusfile exports, normalized.
	// They live here rather than on the magusfile spell because that spell is ONE global
	// instance shared by every project, so it cannot know what any particular magusfile
	// declares - which is why its Targets() is empty and why nothing could previously ask
	// whether a magusfile shadows a spell op of the same name.
	MagusfileTargets []string
	// TargetCrossDeps are the cross-project targets each target depends on, declared
	// via a project import (<alias>.<target>). The descendant-write audit reads them:
	// when a parent target depends on a target INSIDE a descendant project, the writes
	// that descendant makes are its own, not the parent reaching across a boundary.
	TargetCrossDeps map[string][]CrossTargetRef
	// TargetEnvAllow are per-target ctx.env declarations: env var NAMES whose process
	// values fold into the cache key. See TargetGraphNode.EnvAllow.
	TargetEnvAllow map[string][]string
	// InboundOutputs are output globs OTHER projects declare INTO this project's tree
	// via ctx.writesFiles(<alias>.file(...)), keyed by the WRITING project's path. Globs are
	// relative to THIS project's root, so they compose with Outputs directly - which is
	// the whole reason they are filed here rather than left on the writer, whose own
	// globs are relative to a different root. Without this a cross-project output would
	// be invisible to every consumer that asks a project what lands in its tree: clean,
	// watch's rebuild-loop guard, ownership lookup, and the merge driver. The writer key
	// is what lets the merge driver regenerate the file, since only the writer can.
	// Populated at load, after the walk, once every project is known (the owner may not
	// be discovered yet when the writer declares it).
	InboundOutputs map[string][]string
	ResolvedSpells []*spells.Spell // set at the end of magus.Open; immutable thereafter
}

// AllOutputs is every output glob that lands in this project's tree, deduplicated and
// PROJECT-ROOT RELATIVE: the project-wide Outputs, every per-target ctx.writesFiles glob
// this project declares for itself (TargetOutputs), and every glob another project
// declares into it (InboundOutputs). It is the "what files appear in this tree" view -
// consumed by `magus clean --outputs`, watch's rebuild-loop guard, output-ownership
// lookup, and the merge driver - as opposed to the per-target cache view (buildStep's
// step.Outputs), which stays scoped to the one target being run.
//
// A cross-project ref in TargetOutputs is skipped here and counted on the OWNER instead,
// through that project's InboundOutputs: its glob is relative to the tree it writes
// into, so returning it from the writer would have every caller resolve it against the
// wrong root. The two halves meet on the owner, where the glob is already relative.
//
// The result never aliases p.Outputs. Callers treat it as their own slice (the merge
// driver builds workspace-relative globs from it, clean ranges it), and p.Outputs carries
// spare capacity from AttachSpell, so handing the live backing array out of an exported
// method lets one append reach into the project record.
//
// Contributions are sorted so the result is deterministic despite the maps.
func (p *Project) AllOutputs() []string {
	if len(p.TargetOutputs) == 0 && len(p.InboundOutputs) == 0 {
		return slices.Clone(p.Outputs)
	}
	var extra []string
	add := func(glob string) {
		if !slices.Contains(p.Outputs, glob) && !slices.Contains(extra, glob) {
			extra = append(extra, glob)
		}
	}
	for _, refs := range p.TargetOutputs {
		for _, ref := range refs {
			if ref.Project != "" && ref.Project != p.Path {
				continue // written into another tree; that project counts it
			}
			add(ref.Glob)
		}
	}
	for _, globs := range p.InboundOutputs {
		for _, glob := range globs {
			add(glob)
		}
	}
	slices.Sort(extra)
	return append(slices.Clone(p.Outputs), extra...)
}

// AttachSpell associates spell with p without applying registration overrides.
func (p *Project) AttachSpell(spell *spells.Spell) {
	// Internal plumbing never claims the primary slot - see the same rule in
	// magus.bindSpell. The magusfile registration attaches on every project (it is
	// how a project is discovered), so it won this race everywhere and `magus ls`
	// answered "spell: magusfile" for almost every project: true by construction,
	// and therefore no answer at all.
	if p.Spell == "" && !spell.Internal() {
		p.Spell = spell.Name()
	}
	p.Spells = append(p.Spells, spell.Name())
	p.Bindings = append(p.Bindings, &Binding{Name: spell.Name()})
	p.Sources = append(p.Sources, spell.Sources()...)
	p.Outputs = append(p.Outputs, spell.Outputs()...)
}
