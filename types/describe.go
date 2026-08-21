package types

import (
	"slices"

	"github.com/egladman/magus/spells"
)

// Describe-output types and the concept definitions printed by "magus describe".
//
// Naming rule for this file: a type carries the `Entry` suffix (CharmEntry -> now
// Charm is the exception that PROVES it; see below) only when a bare name of that
// type already exists and would collide - ProjectEntry (types.Project),
// TargetEntry (types.Target), WorkspaceEntry (types.Workspace), and FileEntry
// (types.File is reserved for a future promoted path type). Where no such collision
// exists, the bare name wins: Spell, Charm,
// EvaluatedProject, EvaluatedTarget, EvaluatedSpell.

// SpellDefinition is the human-readable description of a spell shown by "magus describe spells".
const SpellDefinition = "A spell is a language/runtime adapter that " +
	"teaches magus how to build, test, lint, and format projects of a given type. " +
	"Spells are registered at startup and bound to projects by importing the spell " +
	"and listing it in the spells of magus.project in the magusfile."

// SpellVersion is one probe's result: the tool it names, what it reported, and the
// cache-key fragment that value produces. Error is set instead of Version when the
// probe could not run, so a caller can tell "not installed" from "no probe declared".
type SpellVersion struct {
	Tool     string `json:"tool" yaml:"tool"`
	Version  string `json:"version,omitempty" yaml:"version,omitempty"`
	CacheKey string `json:"cache_key,omitempty" yaml:"cache_key,omitempty"`
	Error    string `json:"error,omitempty" yaml:"error,omitempty"`
}

// SpellToolchain is a derived executable inventory for one spell. It reports
// the base command Magus has already resolved from static operations and the
// operations that use it; it adds no spell-authoring contract.
type SpellToolchain struct {
	Command    string   `json:"command" yaml:"command"`
	Operations []string `json:"operations" yaml:"operations"`
}

// Spell is the structured view of a single spell.
type Spell struct {
	Name string `json:"name"              yaml:"name"`
	// BuzzImport is the module path a magusfile writes to bind this spell's handle:
	// "magus/spell/go", for `import "magus/spell/go"`. See spells.ModulePath.
	//
	// Named for Buzz because Language below already means something else on this
	// record - the language the spell ADAPTS (go, typescript). This one is the
	// language you write the import IN. Unqualified "module" left a reader to guess
	// which of the two it meant.
	//
	// A path, deliberately, and not the handle itself. internal/describe reads spell
	// imports STATICALLY to build the target graph, so a spell reached any way other
	// than a literal import would lose its target-uses-spell edge and under-report the
	// graph silently. Carrying the path keeps discovery dynamic and the import static:
	// look the spell up, read what to write, then write it.
	BuzzImport string `json:"buzz_import"       yaml:"buzz_import"`
	// BuiltIn reports whether this spell ships compiled into the binary, reachable
	// from any workspace as `import "magus/spell/<name>"`.
	//
	// False means it is a spell THIS workspace loaded from a path
	// (`import "spells/github/actions" as github`), registered when the magusfile
	// evaluated. Both end up in the same registry, so a listing that did not
	// distinguish them showed `github-actions` beside `go` as though a reader could
	// import it by handle and find it documented - they cannot, on either count.
	BuiltIn bool     `json:"built_in"          yaml:"built_in"`
	Sources []string `json:"sources,omitempty" yaml:"sources,omitempty"`
	Outputs []string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
	Opaque  bool     `json:"opaque,omitempty" yaml:"opaque,omitempty"`
	// Language is the canonical source language the spell adapts (e.g. "go",
	// "typescript"), empty for a spell tied to no single language. It tags the spell
	// node so `magus query language:go` reaches the adapter alongside that language's
	// files and symbols.
	Language string `json:"language,omitempty" yaml:"language,omitempty"`
	// VersionProbe reports whether the spell declares a toolchain-version command
	// (mgs_getVersionProbe). Its OUTPUT is mixed into every cache key for the
	// project (run.go's toolVersionsByProject), making it one of the few cache
	// inputs that is not a file - so "why did this key change" is unanswerable from
	// the spell inventory without it. Reported as a bool rather than the argv
	// because the argv survives only inside the probe closure; the descriptor keeps
	// it, and `magus describe spell <name>` docs render it from there.
	//
	// Its absence is not cosmetic: with every spell reporting identically whether or
	// not it probed, the inventory reads as though none of them do.
	VersionProbe bool `json:"version_probe,omitempty" yaml:"version_probe,omitempty"`
	// Versions are the probes' OBSERVED results, populated only when the caller asks
	// for them (they shell out, so they are never gathered by default).
	//
	// Declared and observed are different questions and only the second debugs
	// anything: VersionProbe above says a probe exists, which cannot tell you that the
	// toolchain on this machine has drifted from what the project pins, even though
	// that value is in every cache key. On the model rather than in a print helper so
	// every output format carries it - an agent reading -o json needs it most.
	Versions []SpellVersion `json:"versions,omitempty" yaml:"versions,omitempty"`
	// TargetDocs maps a target name to its handler's doc comment, where one
	// exists. Populated only for workspace-local Buzz spells (built-in docs are
	// not serialized in bytecode).
	TargetDocs map[string]string `json:"target_docs,omitempty" yaml:"target_docs,omitempty"`
	// OpCommands maps an op (target) name to the base argv it runs, rendered with
	// an empty charm set (element 0 is the tool). Present only for ops that declare
	// a static command; a function-op (whose argv is computed by executing its Buzz
	// body) has no entry. It lets the knowledge graph link an op to the tool it runs
	// without re-rendering, so `explain tool:go` reaches every op that runs go.
	OpCommands map[string][]string `json:"op_commands,omitempty" yaml:"op_commands,omitempty"`
	// Toolchains groups OpCommands by their base executable. It is derived rather
	// than declared, so a spell cannot misreport the commands it implements.
	Toolchains []SpellToolchain `json:"toolchains,omitempty" yaml:"toolchains,omitempty"`
}

// CharmDefinition is the human-readable description of a charm shown by "magus describe charms".
const CharmDefinition = "A charm is a named, shared execution modifier applied as an " +
	"RFC 6902 JSON Patch over a target's argv: it changes how a target runs (rw, gha), " +
	"never which target or project runs. See docs/charms.md."

// Charm is one charm in the inverse index: its name, whether it is a reserved
// built-in or a workspace default, its built-in doc (empty for a spell-defined
// charm), and every target that declares a patch for it.
type Charm struct {
	Name         string             `json:"name"                   yaml:"name"`
	Builtin      bool               `json:"builtin,omitempty"      yaml:"builtin,omitempty"`
	Default      bool               `json:"default,omitempty"      yaml:"default,omitempty"`
	Doc          string             `json:"doc,omitempty"          yaml:"doc,omitempty"`
	Declarations []CharmDeclaration `json:"declarations,omitempty" yaml:"declarations,omitempty"`
}

// CharmDeclaration is one target's declaration of a charm: the spell that owns the
// command and the before/after argv the charm's patch produces for that target.
// Before == After marks a declaration whose patch changes nothing for this target.
type CharmDeclaration struct {
	Project string   `json:"project"          yaml:"project"`
	Target  string   `json:"target"           yaml:"target"`
	Spell   string   `json:"spell"            yaml:"spell"`
	Before  []string `json:"before,omitempty" yaml:"before,omitempty"`
	After   []string `json:"after,omitempty"  yaml:"after,omitempty"`
}

// TargetDefinition is the human-readable description of a target shown by "magus describe targets".
const TargetDefinition = "A target is a named operation (e.g. build, test, lint) declared as an " +
	"exported function in a project's magusfile, which may compose a spell's " +
	"tool-native operations. 'ci' is the conventional anchor that the affected set " +
	"keys off — magus runs it read-only but does not hardcode its steps; the magusfile " +
	"composes them with ctx.needs."

// TargetEntry describes a single target available in the workspace.
type TargetEntry struct {
	Name     string   `json:"name"               yaml:"name"`
	Kind     string   `json:"kind"               yaml:"kind"`
	Spells   []string `json:"spells,omitempty"   yaml:"spells,omitempty"`
	Projects []string `json:"projects,omitempty" yaml:"projects,omitempty"`
}

// TargetGraphDefinition describes "magus describe graph".
const TargetGraphDefinition = "The target dependency graph is the ctx.needs " +
	"DAG of a project's magusfile: each node is a target (an exported function), each " +
	"edge a dependency it composes. It is extracted statically from the magusfile " +
	"source, so it shows every edge — including both arms of a runtime branch — and " +
	"flags any dependency cycle (which the run path rejects during dispatch)."

// TargetGraphNode is one target in the graph: its run name, doc comment, the
// targets it depends on, and the charm names its body branches on. The static
// extractor (internal/describe) populates it directly and `magus describe graph`
// serializes it. Wire keys are snake_case field names (dependencies, not the
// abbreviated deps), matching the project-level depends_on and the rest of this file.
type TargetGraphNode struct {
	Name string `json:"name" yaml:"name"`
	// Declared is the target's raw, as-written name when it differs from the normalized
	// Name (Name "go-build" declared as "goBuild" or "go_build"); empty when they match.
	// Name is the identity every edge and lookup keys on - the normalizer maps any
	// spelling to it - so Declared is provenance only: it conveys how the author wrote
	// the target, surfaced as the knowledge graph's declared_as attr.
	Declared     string   `json:"declared,omitempty"     yaml:"declared,omitempty"`
	Doc          string   `json:"doc,omitempty"          yaml:"doc,omitempty"`
	Dependencies []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	Charms       []string `json:"charms,omitempty"       yaml:"charms,omitempty"`
	// Spells are the spell ops the target's body invokes, captured statically from
	// the bracket (`go["go-test"]()`) and dotted (`md.markdownlint()`) call forms,
	// grouped by spell in first-appearance order. It shows which toolchain a
	// composite target drives - the part `deps` (sibling targets) omits.
	Spells []TargetSpellUse `json:"spells,omitempty" yaml:"spells,omitempty"`
	// CrossDependencies are dependencies on specific targets in *other* projects,
	// declared via a project import (<alias>.<target>). Unlike Dependencies (same-project
	// target names), each carries the other project's path, so the graph can draw a
	// target -> target edge across project boundaries instead of a coarse project -> project one.
	CrossDependencies []CrossTargetRef `json:"cross_dependencies,omitempty" yaml:"cross_dependencies,omitempty"`
	// Chain is the target's composition IN INVOCATION ORDER: the DISTINCT steps the
	// body names, in the order it first names them, across every ctx.needs call the
	// target makes. A step named twice appears once, at its first invocation - the
	// second mention adds no ordering the first has not already fixed.
	// Dependencies and CrossDependencies answer "what does this compose"
	// as two sets keyed by locality; Chain answers "in what order", which is the one
	// fact the source carries and neither set preserves once the graph merges them.
	// Empty for a target that composes nothing.
	//
	// DIRECT steps only - one level, never a recursive flattening. A step that itself
	// chains is described by ITS own record; expanding it here would print a plan the
	// magusfile never writes, and the pool (not this list) decides how a transitive
	// dependency actually schedules.
	Chain []ChainStep `json:"chain,omitempty" yaml:"chain,omitempty"`
	// ReadsFiles are the per-target file inputs the body declares via ctx.readsFiles(...),
	// captured statically in ONE representation where each entry carries its owning
	// project (InputRef). A bare-literal glob (ctx.readsFiles("glob")) is a same-project
	// input whose owning project is the target's own project; a ctx.readsFiles(<alias>.
	// file("lit")) entry is a cross-project input whose owning project is the imported
	// one. When present, inputs define the target's file cache footprint rather than
	// extending the project-wide baseline; the target's magusfiles and target-specific
	// spell sources remain included. The
	// single shape feeds the cache footprint, the affected-tracking depends_on edge (a
	// cross-project input only; a same-project one seeds by directory containment), and
	// the consumes edge to the file node in the owning project.
	ReadsFiles []InputRef `json:"reads_files,omitempty" yaml:"reads_files,omitempty"`
	// ReadsSecrets records that the target body reaches for a credential - magus\secret's
	// read, grant or endpoint. A resolved credential contributes NOTHING to the cache key
	// - deliberately, since hashing one would write it into cache metadata - so rotating
	// or revoking it invalidates nothing. A cacheable target that uses one therefore
	// becomes a replay that reports success without ever contacting the provider, which is
	// worst for exactly the authentication targets the `-login` convention encourages,
	// whose sources rarely change. MGS1026 reports the combination; skip_cache with a
	// reason is the fix.
	//
	// A GRANT carries the same hazard in a sharper form, which is why it counts here: the
	// magusfile never holds the value, so changing a grant's ref from staging to
	// production alters nothing the cache can see, and the target replays its old output
	// against a different credential. The name stays ReadsSecrets because it is a
	// Buzz-visible describe field (readsSecrets); the concept it records is "uses".
	ReadsSecrets bool `json:"reads_secrets,omitempty" yaml:"reads_secrets,omitempty"`
	// SecretRefs are the credential REFERENCES this target names, sorted and deduped -
	// never values, which magus does not have at describe time and would not print if it
	// did. It answers "which credentials does this target touch" without running it,
	// which is the question an operator reviewing a magusfile actually has.
	//
	// Only literal references appear. magus\secret.read takes a string literal, so its
	// reference is here; magus\secret.endpoint takes an object usually declared as a
	// `final` elsewhere, so its reference is not at the call site and only ReadsSecrets
	// records the use. Under-reporting is deliberate: resolving that identifier would
	// mean evaluating the magusfile, which a static read refuses to do.
	SecretRefs []string `json:"secret_refs,omitempty" yaml:"secret_refs,omitempty"`
	// WritesFiles are the per-target ctx.writesFiles(...) refs, each carrying its owning project
	// (empty means this target's own). When present, they define the target's
	// snapshot/replay set instead of inheriting project-wide and spell outputs.
	WritesFiles []OutputRef `json:"writes_files,omitempty" yaml:"writes_files,omitempty"`
	// ModifiesExistingFiles are the per-target ctx.modifiesExistingFiles(...) refs: existing files the target edits in place
	// rather than produces. Deliberately NOT unioned into the snapshot/replay set - see
	// UpdateRef for why magus must neither delete nor restore one.
	//
	ModifiesExistingFiles []UpdateRef `json:"modifies_existing_files,omitempty" yaml:"modifies_existing_files,omitempty"`
	// ExecOverrides are the canonical per-op execution overrides this target declares
	// via ctx.withEnv / ctx.withCwd, as "env:K=V" / "cwd:V" strings in declaration
	// order (hash.go sorts a copy at hash time; nothing sorts the stored value). They fold into the target's
	// CACHE KEY: a derived env changes what the tool does, so two runs differing only by
	// it must not share an entry. Read statically for the same reason inputs are - the
	// key is computed before the body runs, so a purely runtime derivation could never
	// reach it. A non-literal derive sets DynamicIO and is rejected at load.
	ExecOverrides []string `json:"exec_overrides,omitempty" yaml:"exec_overrides,omitempty"`
	// EnvAllow names environment variables this target declares via ctx.env, whose
	// PROCESS values fold into the cache key. The complement of ExecOverrides: an
	// override's value is written in the magusfile and hashed directly, while these
	// values are only knowable at run time, so the NAME is what is declared statically
	// and the value is read when the key is computed. That is what lets a target whose
	// env is genuinely derived from the environment stay cacheable instead of having to
	// opt out of the cache entirely.
	EnvAllow []string `json:"env_allow,omitempty" yaml:"env_allow,omitempty"`
	// Observations are the external facts this target declares via ctx.observes, as
	// canonical "key=value" strings in declaration order (hash.go sorts a copy at hash
	// time; nothing sorts the stored value). An observation is a fact the answer depends
	// on that the tree does not contain - a vulnerability feed's id, a remote schema's
	// revision - so the target can key on it instead of opting out of the cache with
	// skip_cache. Both halves are literals, hashed directly as ExecOverrides are, which
	// makes this ExecOverrides' mechanical twin; what differs is that an observation
	// changes nothing about HOW the target runs, only what its answer is a function of.
	// magus never interprets the value: it stores it, and a change is a miss.
	Observations []string `json:"observations,omitempty" yaml:"observations,omitempty"`
	// DynamicIO is set when a ctx.readsFiles/writesFiles/modifiesExistingFiles/envInputs/observes call carries a
	// non-literal argument. A computed glob is invisible to this static read, so the load
	// path rejects it loudly rather than silently caching an under-declared footprint.
	// Not serialized: it is a load-time validation signal, not part of the graph.
	DynamicIO bool `json:"-" yaml:"-" buzz:"-"`
	// DynamicExec is the execution-side counterpart: a ctx.withEnv / ctx.withCwd whose
	// argument is not a literal. It is NOT a load error - the override still takes effect
	// at run time, and a genuinely derived environment cannot be written literally - so it
	// only records that ExecOverrides is an incomplete view of what the target will run
	// with. Not serialized, same as DynamicIO.
	DynamicExec bool `json:"-" yaml:"-" buzz:"-"`
}

// CrossTargetRef names one target in another project: a target-level cross-project
// dependency. Project is workspace-relative (resolved from the dot-/repo-relative
// path written in the magusfile); Target is the kebab-normalized target name.
type CrossTargetRef struct {
	Project string `json:"project" yaml:"project"`
	Target  string `json:"target"  yaml:"target"`
}

// Ref spells the reference the way the CLI takes a target ref, "project:target" - the
// same method ChainStep carries, so a caller printing either kind of reference asks for
// it the same way. Project is never empty on this type (a cross-project ref names
// another project by definition), so there is no bare-name form.
func (r CrossTargetRef) Ref() string {
	return r.Project + ":" + r.Target
}

// ChainStep is one target a composed target invokes, in source order. Project is empty
// for a same-project step (the common case, `ctx.needs(build)`) and carries the other
// project's path for a cross-project one (`ctx.needs(<alias>.build)`) - the same
// empty-means-this-project convention InputRef uses, and it stays empty here even after
// resolution so a reader can tell the two apart at a glance. Deliberately its own type
// rather than a reused CrossTargetRef: that one names a target in ANOTHER project by
// definition, and a chain is mostly local steps.
type ChainStep struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Target  string `json:"target"            yaml:"target"`
}

// Ref spells the step the way the CLI takes a target ref: "target" for a same-project
// step, "project:target" for a cross-project one.
func (s ChainStep) Ref() string {
	if s.Project == "" {
		return s.Target
	}
	return s.Project + ":" + s.Target
}

// InputRef names one file input a target declares via ctx.readsFiles, in a single shape
// that carries the owning project for both a same-project glob and a cross-project file -
// maximally explicit: a local input's project is simply itself. Project is the owning
// project's path; Glob is the doublestar glob (or exact file) relative to that root. For a
// same-project input (ctx.readsFiles("glob")) Project is empty at extraction, meaning "this
// target's own project", and is filled to the project's path when resolved. For a
// cross-project input (ctx.readsFiles(<alias>.file("rel"))) Project names the imported
// project (dot-/repo-relative as written in the magusfile until resolved to
// workspace-relative, mirroring CrossTargetRef). Folding into the cache key, the
// affected-tracking depends_on edge, and the consumes edge all read this one shape.
type InputRef struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Glob    string `json:"glob" yaml:"glob"`
}

// OutputRef names one file output a target declares via ctx.writesFiles, in the same shape
// as InputRef: Project is the OWNING project (the tree written into) and Glob is relative
// to that root. For a same-project output (ctx.writesFiles("glob")) Project is empty at
// extraction and filled to the project's own path when resolved.
//
// A separate type from InputRef despite the identical shape, because the dependency edge
// each implies runs the OTHER WAY. A cross-project INPUT means "I read you, so I run
// after you". A cross-project OUTPUT means "I write your tree, so YOU run after ME" -
// the owner gains the edge, not the declarer. Sharing one type would let a caller pass
// an input where an output belongs and silently invert a build order.
type OutputRef struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Glob    string `json:"glob" yaml:"glob"`
}

// UpdateRef names one EXISTING file a target edits in place rather than produces, declared via
// ctx.modifiesExistingFiles. Same shape as OutputRef, and a third type for the same reason OutputRef
// is not InputRef: what magus is allowed to DO with the file differs, and sharing a
// type would let a caller pass one where the other belongs.
//
// An output is a file magus owns end to end, so magus may delete it (magus clean) and
// restore it wholesale from a cache snapshot. An update is a file magus does NOT own -
// a hand-written page with a generated region between markers, a lockfile a tool
// rewrites in place - where only part of the content is the target's to produce.
// Deleting one destroys authored content that regeneration cannot bring back, and
// replaying one from a snapshot silently reverts edits made since. So an update is
// never deleted and never replayed.
//
// It is a plain source for cache purposes: because it is NOT in the output set, it is
// not excluded from the source hash, so editing the authored prose around the generated
// region correctly invalidates the target that maintains it.
type UpdateRef struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Glob    string `json:"glob" yaml:"glob"`
}

// CrossFileMember is the reserved member on a project-import handle
// (`<alias>.file("rel")`) that resolves a cross-project file to a workspace-relative
// path. The static extractor (internal/describe) and the runtime resolver
// (internal/interp/bindings) MUST agree on this name; this single const is the
// shared source of truth so the two cannot drift apart.
const CrossFileMember = "file"

// TargetSpellUse is one spell a target invokes and the ops it calls on it.
type TargetSpellUse struct {
	Spell string   `json:"spell"         yaml:"spell"`
	Ops   []string `json:"ops,omitempty" yaml:"ops,omitempty"`
}

// TargetGraphProject is one project's target graph, plus a detected cycle (a path
// of node names that begins and ends at the same node) when the DAG is not acyclic.
type TargetGraphProject struct {
	Path   string            `json:"path"             yaml:"path"`
	Name   string            `json:"name"             yaml:"name"`
	Engine string            `json:"engine,omitempty" yaml:"engine,omitempty"`
	Nodes  []TargetGraphNode `json:"nodes,omitempty"  yaml:"nodes,omitempty"`
	Cycle  []string          `json:"cycle,omitempty"  yaml:"cycle,omitempty"`
	// DependsOn are the workspace-relative paths of the projects this project
	// depends on (its project-level deps, declared in magus.project).
	// They draw the project -> project arrows in the combined workspace graph;
	// intra-project target edges live on each node's Dependencies.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// RelPath is Path expressed relative to the VCS (repo) root, used only for an
	// unambiguous MAGUS.md heading when a project sits at the workspace root (Path
	// is "."). Display-only and repo-derived, so it is not serialized; the run path
	// still addresses the project by Path. Empty outside a repo.
	RelPath string `json:"-" yaml:"-" buzz:"-"`
}

// Label is the human display name for this project, the single source every render site
// uses so none prints a bare ".": the pre-collapsed RelPath (which reads as the repo
// name for the workspace root), falling back to the shared never-'.' rule on Path.
func (p TargetGraphProject) Label() string {
	name := p.Name
	if name == "" {
		name = p.RelPath
	}
	return ProjectDisplayName(p.Path, name, "")
}

// TargetGraphOutput is the top-level result for "describe graph".
//
// The Buzz `object TargetGraph` mirror is generated from this struct by
// cmd/magus-utils types, so magus.targets's result can be annotated `> TargetGraph`
// for compile-checked field access. Definition carries `buzz:"-"` for the same reason
// ProjectsOutput's does: BuzzObject drops it, so a mirrored field would be one the Buzz
// value never has.
type TargetGraphOutput struct {
	Definition string               `json:"definition" yaml:"definition" buzz:"-"`
	Projects   []TargetGraphProject `json:"projects"   yaml:"projects"`
}

// ProjectDefinition is the human-readable description of a project shown by "magus describe projects".
const ProjectDefinition = "A project is a directory the workspace recognized as a " +
	"unit of work, bound to one or more spells. Projects are " +
	"discovered by the presence of a magusfile (magusfile.buzz, or a magusfiles/ subdirectory), " +
	"or reported by a workspace provider the magusfile wired, " +
	"and are the basic unit of caching, scheduling, and dependency tracking."

// ProjectEntry is the structured view of a single project. Its Buzz mirror is
// generated alongside Projects; DependsOn is tagged because BuzzObject emits the
// camelCase `dependsOn` the rest of the Buzz surface uses, not the snake_case
// JSON name.
type ProjectEntry struct {
	Path string `json:"path"                yaml:"path"`
	// Name is the project's DECLARED name (magus.project's "name" key), empty when
	// it declares none. Carried on the boundary because a consumer rendering a
	// human label has to prefer it over the directory basename - without it,
	// `magus ls` printed the checkout directory ("agent-harness-handoff-92f105" in
	// a worktree) while MAGUS.md, built from the same workspace, printed "magus".
	Name string `json:"name,omitempty"      yaml:"name,omitempty"`
	// Origin is what put this project in the workspace: "magusfile", or
	// "provider:<spell>" for one a workspace provider reported. It is on the boundary
	// because a provided project has no file to open - without it, `magus describe
	// project libs/foo` describes a project whose declaration the reader cannot find.
	//
	// Plain string, not the ProjectOrigin the engine carries: a boundary record is
	// data (Buzz and JSON both see a str either way), and the named type exists to
	// stop a Go caller comparing against a prefix, which no wire consumer can do.
	Origin string   `json:"origin,omitempty"    yaml:"origin,omitempty"`
	Dir    string   `json:"dir"                 yaml:"dir"`
	Spell  string   `json:"spell,omitempty"     yaml:"spell,omitempty"`
	Spells []string `json:"spells,omitempty"    yaml:"spells,omitempty"`
	// Sources and Outputs are the DECLARED globs, project-relative (as written in
	// the magusfile/spell). EvaluatedProject populates these same fields (via its
	// embedded ProjectEntry) with the RESOLVED, workspace-rooted globs instead
	// (joined against the project path, plus the magusfile's own globs folded into
	// Sources) - the same name, a different representation, because the evaluated
	// view answers "what does the cache key actually see" rather than "what was
	// written".
	Sources   []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty" buzz:"dependsOn"`
	Exclusive bool     `json:"exclusive,omitempty"  yaml:"exclusive,omitempty"`
	// Manifests lists this project's spells' version-manifest candidates
	// (spells.Spell.Manifests), filtered to the ones that actually exist in Dir and
	// kept in declared order - so element 0, when present, is "the" manifest under
	// the first-existing-file-wins rule. A project with no manifest-declaring spell,
	// or none of whose candidates exist, has an empty list: it carries no version of
	// its own.
	Manifests []string `json:"manifests,omitempty" yaml:"manifests,omitempty"`
	// Lockfiles lists the lockfile each entry in Manifests actually resolves into
	// (spells.Manifest.LockCandidates), found by walking from Dir up to the workspace
	// root and taking the first candidate that exists. Empty when the ecosystem has no
	// lockfile, when none has been written yet, or when the manifest is one that does
	// not lock (setup.py).
	//
	// These are WORKSPACE-RELATIVE, unlike Manifests, which are bare filenames. That
	// asymmetry is the useful part rather than an inconsistency: a manifest is always
	// in Dir, so its directory says nothing, while a lockfile may be hoisted to a
	// workspace root several levels up to serve many projects - so which directory
	// holds it is the only thing resolving it determines. A pnpm workspace member
	// reports "pnpm-lock.yaml" at the root here while its Manifests says
	// "package.json" beside it.
	Lockfiles []string `json:"lockfiles,omitempty" yaml:"lockfiles,omitempty"`
}

// ProjectsOutput is the top-level result for "describe projects".
//
// The Buzz `object Projects` mirror is generated from this struct by
// cmd/magus-utils types, so magus.ls's result can be annotated `> Projects` for
// compile-checked field access. Definition carries `buzz:"-"` to keep the mirror
// honest: BuzzObject drops it, so a mirrored field would be one the Buzz value never
// has.
type ProjectsOutput struct {
	Definition string         `json:"definition" yaml:"definition" buzz:"-"`
	Workspace  string         `json:"workspace"  yaml:"workspace"`
	Count      int            `json:"count"      yaml:"count"`
	Projects   []ProjectEntry `json:"projects"   yaml:"projects"`
}

// ModuleDefinition is the human-readable description shown by "magus describe modules".
const ModuleDefinition = "A module is a magus standard-library namespace a magusfile imports for " +
	"host capabilities — filesystem, exec, vcs, crypto, http, and more. Import " +
	"each under its bare name (import \"fs\", then fs.glob(...)); magus layers these " +
	"methods onto Buzz's own stdlib. The magus forms are sandbox-aware; some methods " +
	"also exist in Buzz's own stdlib."

// ModuleMethodEntry is one method of a module, with its Buzz call form.
type ModuleMethodEntry struct {
	Name       string `json:"name"                  yaml:"name"`
	Doc        string `json:"doc,omitempty"         yaml:"doc,omitempty"`
	Buzz       string `json:"buzz"                  yaml:"buzz"`
	BuzzStdlib string `json:"buzz_stdlib,omitempty" yaml:"buzz_stdlib,omitempty"`
}

// ModuleFieldEntry is one static, table-level value on a module (e.g. vcs.name).
type ModuleFieldEntry struct {
	Name string `json:"name"          yaml:"name"`
	Type string `json:"type"          yaml:"type"`
	Doc  string `json:"doc,omitempty" yaml:"doc,omitempty"`
}

// ModuleEntry is a module's summary; Fields/Methods are populated only for the detail view.
type ModuleEntry struct {
	Name    string              `json:"name"              yaml:"name"`
	Doc     string              `json:"doc,omitempty"     yaml:"doc,omitempty"`
	Fields  []ModuleFieldEntry  `json:"fields,omitempty"  yaml:"fields,omitempty"`
	Methods []ModuleMethodEntry `json:"methods,omitempty" yaml:"methods,omitempty"`
}

// EvaluatedTargetDefinition is the human-readable description of an evaluated target shown by "magus describe".
const EvaluatedTargetDefinition = "An evaluated target shows the fully-resolved " +
	"dispatch plan for a specific path:target pair: the workspace-rooted source and " +
	"output globs that feed the cache key, the chain of targets it composes in " +
	"invocation order, the spells that will fire (with " +
	"target-specific sources), " +
	"and any behavioral policy (CheckClean, TrackVolatile, Exclusive)."

// EvaluatedSpell is one spell's contribution to an evaluated target.
type EvaluatedSpell struct {
	Name          string   `json:"name"                        yaml:"name"`
	TargetSources []string `json:"target_sources,omitempty"    yaml:"target_sources,omitempty"`
	// Command is the fork command this spell's op would run for the target, with
	// the requested charms applied (cmd as element 0). Empty for function-op or
	// no-op targets, whose argv isn't statically knowable. Preview only: `magus
	// describe` renders it; nothing is executed.
	Command []string `json:"command,omitempty"           yaml:"command,omitempty"`
	// CharmTrace is the step-by-step application of the active charms over this
	// spell's base argv: element 0 is the base command (no charms), and each
	// subsequent step is the command after one more charm's patch, in the
	// deterministic sorted-name order magus applies them. Populated only when
	// charms are active and change the command; the RFC 6902 patch made legible by
	// `magus describe target ...:charm --explain`.
	CharmTrace []spells.CharmTraceStep `json:"charm_trace,omitempty"       yaml:"charm_trace,omitempty"`
	// Conflicts lists the active charms whose edit is overridden by another active
	// charm on this command (both edit the same argument; the winner is decided by
	// sorted charm name, so the loser has no effect). Empty when the active charms
	// have disjoint edits. `magus describe target ...:a,b` surfaces it before a run.
	Conflicts []spells.CharmConflict `json:"conflicts,omitempty"         yaml:"conflicts,omitempty"`
	// Service is set only when this spell's op is a service (a long-running process
	// magus supervises rather than runs to completion). It carries the static, pre-run
	// facts; Command above is the process itself. Nil for an ordinary command op.
	Service *spells.ServiceView `json:"service,omitempty" yaml:"service,omitempty"`
}

// EvaluatedTarget is the fully-resolved view of a single path:target pair.
type EvaluatedTarget struct {
	Project string   `json:"project"             yaml:"project"`
	Target  string   `json:"target"              yaml:"target"`
	Dir     string   `json:"dir"                 yaml:"dir"`
	Sources []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	// Chain is the targets this one composes, in invocation order; empty when it
	// composes nothing. See TargetGraphNode.Chain, which it is copied from.
	Chain     []ChainStep      `json:"chain,omitempty"      yaml:"chain,omitempty"`
	DependsOn []string         `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Charms    []string         `json:"charms,omitempty"     yaml:"charms,omitempty"`
	Spells    []EvaluatedSpell `json:"spells,omitempty"     yaml:"spells,omitempty"`
	Policy    *Target          `json:"policy,omitempty"    yaml:"policy,omitempty"` // only the policy fields of Target are meaningful (SkipCache/Exclusive/Drift/RetryOnVolatile)
	Exclusive bool             `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
}

// EvaluatedProject is the fully-resolved view of a project: every ProjectEntry
// fact (Name and Spell included - embedding rather than restating them makes
// "evaluated = declared + resolution" a compile-time fact) plus the resolution
// fields resolving it adds. ResolvedSpells is deliberately not named Spells: that
// name means DECLARED spell names on ProjectEntry.Spells and would mean RESOLVED
// spell steps here - one field name for two different facts.
//
// The embedded ProjectEntry's Sources/Outputs are populated with the RESOLVED,
// workspace-rooted globs (see the field comment on ProjectEntry.Sources), not the
// declared project-relative ones ListProjects reports - the one deliberate
// exception to "same values ListProjects builds" for the rest of the embed.
type EvaluatedProject struct {
	ProjectEntry
	ResolvedSpells []EvaluatedSpell  `json:"resolved_spells,omitempty" yaml:"resolved_spells,omitempty"`
	TargetPolicies map[string]Target `json:"target_policies,omitempty"  yaml:"target_policies,omitempty"`
}

// BuzzObject is the Buzz boundary map for one evaluated project. Written explicitly
// rather than left to the BuzzObject ProjectEntry promotes: an embedded ProjectEntry's
// BuzzObject is promoted onto EvaluatedProject too, which would satisfy host's boundary view
// (host/helpers.go) while emitting only the declared half and silently dropping
// ResolvedSpells/TargetPolicies. EvaluatedProject is not on the Buzz mirror
// allowlist and no std/ host method returns it today, so nothing calls this yet -
// it exists to keep that latent trap from going live if one ever does. Neither
// EvaluatedSpell nor Target (whose zero value serves double duty as a per-target
// policy - see Target's identity-fields comment) has its own BuzzObject, so their
// fields are read directly rather than through a promoted-in-the-same-way call.
func (p EvaluatedProject) BuzzObject() BuzzObject {
	m := BuzzObject{
		"path":      p.Path,
		"name":      p.Name,
		"dir":       p.Dir,
		"spell":     p.Spell,
		"spells":    p.Spells,
		"sources":   p.Sources,
		"outputs":   p.Outputs,
		"dependsOn": p.DependsOn,
		"exclusive": p.Exclusive,
		"manifests": p.Manifests,
	}
	spells := make([]any, len(p.ResolvedSpells))
	for i, s := range p.ResolvedSpells {
		spells[i] = map[string]any{
			"name":          s.Name,
			"targetSources": s.TargetSources,
		}
	}
	policies := make(map[string]any, len(p.TargetPolicies))
	for name, t := range p.TargetPolicies {
		policies[name] = map[string]any{
			"skipCache": t.SkipCache,
			"exclusive": t.Exclusive,
			"slots":     t.Slots,
			"memory_mb": t.MemoryMB,
		}
	}
	m["resolvedSpells"] = spells
	m["targetPolicies"] = policies
	return m
}

// EvaluatedProjectsOutput is the top-level result for "describe projects --evaluated".
type EvaluatedProjectsOutput struct {
	Definition string             `json:"definition" yaml:"definition"`
	Workspace  string             `json:"workspace"  yaml:"workspace"`
	Count      int                `json:"count"      yaml:"count"`
	Projects   []EvaluatedProject `json:"projects"   yaml:"projects"`
}

// WorkspaceDefinition is the human-readable description of a workspace shown by "magus describe workspaces".
const WorkspaceDefinition = "A workspace is a magus root directory that owns a set " +
	"of projects, a configuration file, a content-addressed cache, and VCS " +
	"integration. Every magus invocation operates within exactly one workspace, " +
	"identified by walking up from the current directory to the nearest go.mod."

// WorkspaceEntry holds details about the active workspace.
type WorkspaceEntry struct {
	Root         string `json:"root"                    yaml:"root"`
	VCSBaseRef   string `json:"vcs_base_ref,omitempty"  yaml:"vcs_base_ref,omitempty"`
	CacheDir     string `json:"cache_dir,omitempty"     yaml:"cache_dir,omitempty"`
	Concurrency  int    `json:"concurrency,omitempty"   yaml:"concurrency,omitempty"`
	ProjectCount int    `json:"project_count"           yaml:"project_count"`
}

// WorkspaceConfig carries infrastructure details for Inspector.Workspace
// that are not part of the WorkspaceRepository interface (cache path,
// concurrency).
type WorkspaceConfig struct {
	CacheDir    string
	Concurrency int
}

// ToolDefinition is the human-readable description printed by "magus describe tool".
const ToolDefinition = "A tool is a binary a spell drives. magus probes its version on " +
	"every run, because that version keys the cache. A project may also hold it to a " +
	"window - an inclusive min and an exclusive below - intersected with what the " +
	"declaring spell requires."

// FileDefinition is the human-readable description printed by "magus describe file".
const FileDefinition = "Describe file classifies paths against the workspace's declared " +
	"globs: the project that owns each path, whether it is a declared output (generated: " +
	"regenerate it, never hand-edit), a declared source (it feeds cache keys and the " +
	"affected set), or one magus maintains itself outside any target, and which projects " +
	"claim it either way. It answers \"can I disregard this changed file\" from the " +
	"workspace's own declarations, and - over several paths in one call - which single " +
	"declaration covers more than one of them."

// FileEntry classifies one workspace-relative path.
type FileEntry struct {
	Path string `json:"path" yaml:"path"`
	// Project is the owning project by directory containment (longest project
	// path prefixing the file), empty when no project dir contains it.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	// Role summarizes the strongest claim: "output" (a declared output glob
	// matches - the file is generated), "source" (a declared source glob
	// matches), "maintained" (no project declares it, but magus's own core writes
	// it - see IsMagusMaintained), or "unclaimed" (no declared glob matches; it
	// invalidates no cache key and affects no target). An unclaimed path may
	// still carry Claims: an in-place edit is a declaration none of these roles
	// rank, and the Hint says so when that is what happened.
	//
	// maintained is a REFINEMENT of unclaimed, not a rank above source: both are
	// invisible to the cache and the affected set. It is separate because the
	// handling rule inverts. An unclaimed path may be residue to ignore, so its
	// hint says to check the ignore rules; a maintained path is one magus wrote
	// and expects committed, and telling someone to consider ignoring it is
	// advice to drop magus's own bookkeeping.
	Role string `json:"role" yaml:"role"`
	// OutputOf and SourceOf list the projects whose declared output/source globs
	// match the path. A path can be both (a committed generated file is often a
	// source of downstream targets); Role reports output in that case because
	// the regeneration rule dominates how the file should be treated.
	OutputOf []string `json:"output_of,omitempty" yaml:"output_of,omitempty"`
	SourceOf []string `json:"source_of,omitempty" yaml:"source_of,omitempty"`
	// Claims are the individual declarations that name the path, each with the
	// target that made it and the glob that matched. OutputOf/SourceOf are the
	// project-level summary of the same facts; the declaration is the finer unit,
	// and it is the one that answers "which target rewrites this" for a caller
	// splitting work across agents.
	//
	// The set is wider than Role ranks: it also carries the in-place edits of
	// ctx.modifiesExistingFiles ("update"), which is a write nobody replays or
	// cleans, so Role deliberately leaves such a file reading as its source or
	// unclaimed self.
	Claims []FileClaim `json:"claims,omitempty" yaml:"claims,omitempty"`
	// DependsOn is the owning project's DIRECT declared dependencies, verbatim
	// from Project.DependsOn - carried here so one classification call answers
	// both "who owns this path" and "what does that owner run behind". It is not
	// the transitive closure; `magus graph deps` computes that.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// Hint is the one-line handling rule for the role, ready to surface to a
	// human or an agent.
	Hint string `json:"hint,omitempty" yaml:"hint,omitempty"`
}

// FileClaim is one declaration that names a path: the project whose magusfile
// declared it, the target that did, and the workspace-rooted glob that matched.
//
// A cross-project write is attributed to the DECLARING project, not the tree it
// lands in - the opposite of FileEntry.OutputOf, which follows Project.AllOutputs
// and counts it on the owner. Both are true and neither is redundant: the owner
// says whose tree the file appears in, the declarer says whose target puts it
// there, and only the second one can regenerate it.
type FileClaim struct {
	Project string `json:"project" yaml:"project"`
	// Target is empty for a project-wide or spell-supplied glob, which every
	// target of that project carries.
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
	// Role is "output" (ctx.writesFiles or a project/spell output glob), "source"
	// (ctx.readsFiles or a project/spell source glob), or "update"
	// (ctx.modifiesExistingFiles). The first two are also FileEntry.Role values;
	// "update" has no FileEntry.Role counterpart - see FileEntry.Claims.
	Role string `json:"role" yaml:"role"`
	Glob string `json:"glob" yaml:"glob"`
	// Paths is populated only on FileReport.Overlaps, where one claim is reported
	// once for the whole request and lists every path in it that the declaration
	// covers. It is empty on a FileEntry's own claims, where the path is the
	// entry's.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
}

// magusMaintainedFiles are the workspace-relative paths magus's own core writes
// outside any target's declared globs.
//
// .gitattributes is the whole list, and it is here rather than in the merge-driver
// code that writes it because two features have to agree about it: staging
// (StagingPlan.Maintained) and classification (FileEntry.Role). They disagreed -
// `magus vcs add` reported the file as one magus maintains while `magus describe
// file` called it unclaimed and suggested checking the ignore rules, for a file
// magus had just written and needs tracked.
//
// Deliberately NOT derived from the declared output globs: it is the inverse of
// them. EnsureMergeDriver writes .gitattributes FROM every project's output globs,
// so a project declaring it would be circular - the input to the derivation
// claiming to be its own product.
var magusMaintainedFiles = map[string]bool{
	".gitattributes": true,
}

// IsMagusMaintained reports whether path is one magus's own core writes and expects
// committed, rather than a target output or anything a project declares. The path is
// workspace-relative and slash-separated, as FileEntry.Path and StagingPlan carry it.
func IsMagusMaintained(path string) bool { return magusMaintainedFiles[path] }

// The *Report types below are RENDER shapes, not domain types: the {definition,
// count, items} envelope `magus describe ... -o json` emits. The Inspector method
// hands back a plain slice and the caller that is actually serializing wraps it
// here, so both the CLI and the MCP handler emit the same JSON without either of
// them owning the shape.
//
// Two methods are NOT on this pattern, and it is worth knowing which before adding
// a third: ListProjects and EvaluateProjects still return
// ProjectsOutput / EvaluatedProjectsOutput. Both carry a real Workspace field, so
// their envelope is not purely derivable the way {constant, len(), items} is, and
// converting them would mean returning a slice plus an out-of-band workspace root.
// TargetGraphOutput is a third shape again - it has no Count at all, and it is
// json.Marshal'd straight onto the wire for the browser graph explorer.
//
// So: `Report` means "rebuilt at the render edge from a slice", `Output` means "the
// method returns this". Do not add a new `Output` without a field that earns it.
//
// They are deliberately not what the repository returns. Definition is a package
// constant and Count is len(items), so carrying them on a domain type meant every
// call site that filtered the slice had to reassign Count by hand - a
// denormalization that a single forgotten line ships as a wrong count, and which
// had grown a test (len(Spells) != Count) purely to catch itself.

// FileReport is the "describe file <path>..." envelope.
type FileReport struct {
	Definition string      `json:"definition" yaml:"definition"`
	Count      int         `json:"count"      yaml:"count"`
	Files      []FileEntry `json:"files"      yaml:"files"`
	// Overlaps are the declarations that cover MORE THAN ONE of the classified
	// paths, each listing the paths it covers. Grouped by declaration rather than
	// emitted per pair: a hundred paths under one glob is a hundred rows here and
	// five thousand as pairs, and the declaration is the shared thing anyway.
	//
	// A fact, not a verdict. Two paths under one glob mean one target rewrites
	// both, which may be a collision between two authors or may be exactly what a
	// single author intends; nothing here decides which.
	Overlaps []FileClaim `json:"overlaps,omitempty" yaml:"overlaps,omitempty"`
}

// NewFileReport wraps a classification in the wire envelope. A constructor rather
// than a literal at each render edge, because Overlaps is derived from files and
// two call sites (the CLI and the MCP tool) building it by hand is the same
// forgotten-line hazard the Count field above already documents.
func NewFileReport(files []FileEntry) FileReport {
	return FileReport{
		Definition: FileDefinition,
		Count:      len(files),
		Files:      files,
		Overlaps:   fileOverlaps(files),
	}
}

// fileOverlaps groups the entries' claims by declaration, keeping the ones that
// cover several distinct paths. Order follows first appearance, so the result is
// stable for a given argument order.
func fileOverlaps(files []FileEntry) []FileClaim {
	type key struct{ project, target, role, glob string }
	index := map[key]int{}
	var claims []FileClaim
	for _, f := range files {
		for _, c := range f.Claims {
			k := key{c.Project, c.Target, c.Role, c.Glob}
			i, seen := index[k]
			if !seen {
				index[k] = len(claims)
				c.Paths = []string{f.Path}
				claims = append(claims, c)
				continue
			}
			if !slices.Contains(claims[i].Paths, f.Path) {
				claims[i].Paths = append(claims[i].Paths, f.Path)
			}
		}
	}
	var out []FileClaim
	for _, c := range claims {
		if len(c.Paths) > 1 {
			out = append(out, c)
		}
	}
	return out
}

// CharmReport is the "describe charm[s]" envelope.
type CharmReport struct {
	Definition string  `json:"definition" yaml:"definition"`
	Count      int     `json:"count"      yaml:"count"`
	Charms     []Charm `json:"charms"     yaml:"charms"`
}

// EvaluatedTargetReport is the "describe target <path:target>" envelope.
type EvaluatedTargetReport struct {
	Definition string            `json:"definition" yaml:"definition"`
	Count      int               `json:"count"      yaml:"count"`
	Targets    []EvaluatedTarget `json:"targets"    yaml:"targets"`
}

// TargetReport is the "describe target[s]" envelope.
type TargetReport struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Targets    []TargetEntry `json:"targets"    yaml:"targets"`
}

// WorkspaceReport is the "describe workspace[s]" envelope.
type WorkspaceReport struct {
	Definition string           `json:"definition" yaml:"definition"`
	Count      int              `json:"count"      yaml:"count"`
	Workspaces []WorkspaceEntry `json:"workspaces" yaml:"workspaces"`
}

// ModuleReport is the "describe module[s]" envelope.
type ModuleReport struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Modules    []ModuleEntry `json:"modules"    yaml:"modules"`
}

// SpellReport is the "describe spell[s]" envelope.
type SpellReport struct {
	Definition string  `json:"definition" yaml:"definition"`
	Count      int     `json:"count"      yaml:"count"`
	Spells     []Spell `json:"spells"     yaml:"spells"`
}
