package types

// Describe-output types and the concept definitions printed by "magus describe".

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

// SpellEntry is the structured view of a single spell.
type SpellEntry struct {
	Name    string   `json:"name"              yaml:"name"`
	Sources []string `json:"sources,omitempty" yaml:"sources,omitempty"`
	Outputs []string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Claims  []string `json:"claims,omitempty"  yaml:"claims,omitempty"`
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
	Opaque  bool     `json:"opaque,omitempty" yaml:"opaque,omitempty"`
	// Language is the canonical source language the spell adapts (e.g. "go",
	// "typescript"), empty for a spell tied to no single language. It tags the spell
	// node so `magus query language:go` reaches the adapter alongside that language's
	// files and symbols.
	Language string `json:"language,omitempty" yaml:"language,omitempty"`
	// VersionProbe reports whether the spell declares a toolchain-version command
	// (mgs_getVersionCommand). Its OUTPUT is mixed into every cache key for the
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
}

// SpellsOutput is the top-level result for "describe spells".
type SpellsOutput struct {
	Definition string       `json:"definition" yaml:"definition"`
	Count      int          `json:"count"      yaml:"count"`
	Spells     []SpellEntry `json:"spells"     yaml:"spells"`
}

// CharmDefinition is the human-readable description of a charm shown by "magus describe charms".
const CharmDefinition = "A charm is a named, shared execution modifier applied as an " +
	"RFC 6902 JSON Patch over a target's argv: it changes how a target runs (rw, gha), " +
	"never which target or project runs. See docs/charms.md."

// CharmsOutput is the payload of "magus describe charm[s]": the inverse charm
// index, every charm name known in the workspace paired with the declarations that
// give it meaning. It is the transpose of EvaluatedTargetsOutput's per-target charm
// list (one charm, every target that declares it).
type CharmsOutput struct {
	Definition string       `json:"definition" yaml:"definition"`
	Count      int          `json:"count"      yaml:"count"`
	Charms     []CharmEntry `json:"charms"     yaml:"charms"`
}

// CharmEntry is one charm in the inverse index: its name, whether it is a reserved
// built-in or a workspace default, its built-in doc (empty for a spell-defined
// charm), and every target that declares a patch for it.
type CharmEntry struct {
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

// TargetsOutput is the top-level result for "describe targets".
type TargetsOutput struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Targets    []TargetEntry `json:"targets"    yaml:"targets"`
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
	// Inputs are the per-target file inputs the body declares via ctx.inputs(...),
	// captured statically in ONE representation where each entry carries its owning
	// project (InputRef). A bare-literal glob (ctx.inputs("glob")) is a same-project
	// input whose owning project is the target's own project; a ctx.inputs(<alias>.
	// file("lit")) entry is a cross-project input whose owning project is the imported
	// one. Inputs ADD to the target's cache key - unioned onto the project-wide and
	// spell-contributed globs, never replacing them, so the footprint can only grow. The
	// single shape feeds the cache footprint, the affected-tracking depends_on edge (a
	// cross-project input only; a same-project one seeds by directory containment), and
	// the consumes edge to the file node in the owning project.
	Inputs []InputRef `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	// Outputs are the per-target ctx.outputs(...) refs, each carrying its owning project
	// (empty means this target's own). They ADD to the target's snapshot/replay set -
	// unioned onto the project-wide and spell-contributed globs, never replacing them.
	Outputs []OutputRef `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	// Updates are the per-target ctx.updates(...) refs: files the target edits in place
	// rather than produces. Deliberately NOT unioned into the snapshot/replay set - see
	// UpdateRef for why magus must neither delete nor restore one.
	Updates []UpdateRef `json:"updates,omitempty" yaml:"updates,omitempty"`
	// ExecOverrides are the canonical per-op execution overrides this target declares
	// via ctx.withEnv / ctx.withCwd, as "env:K=V" / "cwd:V" strings in declaration
	// order (hash.go sorts a copy at hash time; nothing sorts the stored value). They fold into the target's
	// CACHE KEY: a derived env changes what the tool does, so two runs differing only by
	// it must not share an entry. Read statically for the same reason inputs are - the
	// key is computed before the body runs, so a purely runtime derivation could never
	// reach it. A non-literal derive sets DynamicIO and is rejected at load.
	ExecOverrides []string `json:"exec_overrides,omitempty" yaml:"exec_overrides,omitempty"`
	// DynamicIO is set when a ctx.inputs/outputs call carries a non-literal
	// argument. A computed glob is invisible to this static read, so the load path
	// rejects it loudly rather than silently caching an under-declared footprint.
	// Not serialized: it is a load-time validation signal, not part of the graph.
	DynamicIO bool `json:"-" yaml:"-" buzz:"-"`
}

// buzzList is the Buzz boundary form of an optional []string: never nil.
//
// Every list below is `omitempty` on the wire, where absent and empty are the same
// thing. On the Buzz boundary they are NOT: a nil slice arrives as null, and null is
// not a list - `deps.len()` on it raises "null is not callable" at render time, far
// from the field that was empty. The mirrors generated from these structs default
// every list field to `[]`, so returning one is also what the annotation promises.
func buzzList(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ToMap is the Buzz boundary map for one target. Keys are the camelCase names the
// generated mirror declares, not the snake_case JSON ones.
func (n TargetGraphNode) ToMap() map[string]any {
	spells := make([]any, len(n.Spells))
	for i, s := range n.Spells {
		spells[i] = s.ToMap()
	}
	cross := make([]any, len(n.CrossDependencies))
	for i, c := range n.CrossDependencies {
		cross[i] = c.ToMap()
	}
	inputs := make([]any, len(n.Inputs))
	for i, in := range n.Inputs {
		inputs[i] = in.ToMap()
	}
	outputs := make([]any, len(n.Outputs))
	for i, o := range n.Outputs {
		outputs[i] = o.ToMap()
	}
	return map[string]any{
		"name":              n.Name,
		"declared":          n.Declared,
		"doc":               n.Doc,
		"dependencies":      buzzList(n.Dependencies),
		"charms":            buzzList(n.Charms),
		"spells":            spells,
		"crossDependencies": cross,
		"inputs":            inputs,
		"outputs":           outputs,
	}
}

// CrossTargetRef names one target in another project: a target-level cross-project
// dependency. Project is workspace-relative (resolved from the dot-/repo-relative
// path written in the magusfile); Target is the kebab-normalized target name.
type CrossTargetRef struct {
	Project string `json:"project" yaml:"project"`
	Target  string `json:"target"  yaml:"target"`
}

// ToMap is the Buzz boundary map for one cross-project target reference.
func (c CrossTargetRef) ToMap() map[string]any {
	return map[string]any{
		"project": c.Project,
		"target":  c.Target,
	}
}

// InputRef names one file input a target declares via ctx.inputs, in a single shape
// that carries the owning project for both a same-project glob and a cross-project file -
// maximally explicit: a local input's project is simply itself. Project is the owning
// project's path; Glob is the doublestar glob (or exact file) relative to that root. For a
// same-project input (ctx.inputs("glob")) Project is empty at extraction, meaning "this
// target's own project", and is filled to the project's path when resolved. For a
// cross-project input (ctx.inputs(<alias>.file("rel"))) Project names the imported
// project (dot-/repo-relative as written in the magusfile until resolved to
// workspace-relative, mirroring CrossTargetRef). Folding into the cache key, the
// affected-tracking depends_on edge, and the consumes edge all read this one shape.
type InputRef struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Glob    string `json:"glob" yaml:"glob"`
}

// ToMap is the Buzz boundary map for one declared file input.
func (r InputRef) ToMap() map[string]any {
	return map[string]any{
		"project": r.Project,
		"glob":    r.Glob,
	}
}

// OutputRef names one file output a target declares via ctx.outputs, in the same shape
// as InputRef: Project is the OWNING project (the tree written into) and Glob is relative
// to that root. For a same-project output (ctx.outputs("glob")) Project is empty at
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

// UpdateRef names one file a target EDITS IN PLACE rather than produces, declared via
// ctx.updates. Same shape as OutputRef, and a third type for the same reason OutputRef
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

// ToMap is the Buzz boundary map for one declared file output.
func (r OutputRef) ToMap() map[string]any {
	return map[string]any{
		"project": r.Project,
		"glob":    r.Glob,
	}
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

// ToMap is the Buzz boundary map for one spell a target drives.
func (u TargetSpellUse) ToMap() map[string]any {
	return map[string]any{
		"spell": u.Spell,
		"ops":   buzzList(u.Ops),
	}
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

// ToMap is the Buzz boundary map for one project's target graph. RelPath is dropped:
// it exists for Label(), which a Go render site calls, and mirroring it would put a
// field on the Buzz value that the value never carries.
func (p TargetGraphProject) ToMap() map[string]any {
	nodes := make([]any, len(p.Nodes))
	for i, n := range p.Nodes {
		nodes[i] = n.ToMap()
	}
	return map[string]any{
		"path":      p.Path,
		"name":      p.Name,
		"engine":    p.Engine,
		"nodes":     nodes,
		"cycle":     buzzList(p.Cycle),
		"dependsOn": buzzList(p.DependsOn),
	}
}

// TargetGraphOutput is the top-level result for "describe graph".
//
// The Buzz `object TargetGraph` mirror is generated from this struct by
// cmd/magus-utils types, so magus.targets's result can be annotated `> TargetGraph`
// for compile-checked field access. Definition carries `buzz:"-"` for the same reason
// ProjectsOutput's does: ToMap drops it, so a mirrored field would be one the Buzz
// value never has.
type TargetGraphOutput struct {
	Definition string               `json:"definition" yaml:"definition" buzz:"-"`
	Projects   []TargetGraphProject `json:"projects"   yaml:"projects"`
}

// ToMap is the Buzz boundary map magus.targets returns. Definition is dropped: it is
// prose for a human reading `magus describe`, not something a magusfile branches on.
func (o TargetGraphOutput) ToMap() map[string]any {
	projects := make([]any, len(o.Projects))
	for i, p := range o.Projects {
		projects[i] = p.ToMap()
	}
	return map[string]any{"projects": projects}
}

// ProjectDefinition is the human-readable description of a project shown by "magus describe projects".
const ProjectDefinition = "A project is a directory the workspace recognized as a " +
	"unit of work, bound to one or more spells. Projects are " +
	"discovered by the presence of a magusfile (magusfile.buzz, or a magusfiles/ subdirectory) " +
	"and are the basic unit of caching, scheduling, and dependency tracking."

// ProjectEntry is the structured view of a single project. Its Buzz mirror is
// generated alongside Projects; DependsOn is tagged because ToMap emits the
// camelCase `dependsOn` the rest of the Buzz surface uses, not the snake_case
// JSON name.
type ProjectEntry struct {
	Path string `json:"path"                yaml:"path"`
	// Name is the project's DECLARED name (magus.project's "name" key), empty when
	// it declares none. Carried on the boundary because a consumer rendering a
	// human label has to prefer it over the directory basename - without it,
	// `magus ls` printed the checkout directory ("agent-harness-handoff-92f105" in
	// a worktree) while MAGUS.md, built from the same workspace, printed "magus".
	Name      string   `json:"name,omitempty"      yaml:"name,omitempty"`
	Dir       string   `json:"dir"                 yaml:"dir"`
	Spell     string   `json:"spell,omitempty"     yaml:"spell,omitempty"`
	Spells    []string `json:"spells,omitempty"    yaml:"spells,omitempty"`
	Sources   []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty" buzz:"dependsOn"`
	Exclusive bool     `json:"exclusive,omitempty"  yaml:"exclusive,omitempty"`
}

// ToMap is the Buzz boundary map for one project (magus.ls's entries).
func (p ProjectEntry) ToMap() map[string]any {
	return map[string]any{
		"path":      p.Path,
		"name":      p.Name,
		"dir":       p.Dir,
		"spell":     p.Spell,
		"spells":    p.Spells,
		"sources":   p.Sources,
		"outputs":   p.Outputs,
		"dependsOn": p.DependsOn,
		"exclusive": p.Exclusive,
	}
}

// ProjectsOutput is the top-level result for "describe projects".
//
// The Buzz `object Projects` mirror is generated from this struct by
// cmd/magus-utils types, so magus.ls's result can be annotated `> Projects` for
// compile-checked field access. Definition carries `buzz:"-"` to keep the mirror
// honest: ToMap drops it, so a mirrored field would be one the Buzz value never
// has.
type ProjectsOutput struct {
	Definition string         `json:"definition" yaml:"definition" buzz:"-"`
	Workspace  string         `json:"workspace"  yaml:"workspace"`
	Count      int            `json:"count"      yaml:"count"`
	Projects   []ProjectEntry `json:"projects"   yaml:"projects"`
}

// ToMap is the Buzz boundary map magus.ls returns. Definition is dropped: it is
// prose for a human reading `magus describe`, not something a magusfile branches on.
func (o ProjectsOutput) ToMap() map[string]any {
	projects := make([]any, len(o.Projects))
	for i, p := range o.Projects {
		projects[i] = p.ToMap()
	}
	return map[string]any{
		"workspace": o.Workspace,
		"count":     o.Count,
		"projects":  projects,
	}
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

// ToMap is the Buzz boundary map for a method entry (magus.module's methods).
func (m ModuleMethodEntry) ToMap() map[string]any {
	return map[string]any{"name": m.Name, "doc": m.Doc, "buzz": m.Buzz, "buzzStdlib": m.BuzzStdlib}
}

// ModuleFieldEntry is one static, table-level value on a module (e.g. vcs.name).
type ModuleFieldEntry struct {
	Name string `json:"name"          yaml:"name"`
	Type string `json:"type"          yaml:"type"`
	Doc  string `json:"doc,omitempty" yaml:"doc,omitempty"`
}

// ToMap is the Buzz boundary map for a field entry (magus.module's fields).
func (f ModuleFieldEntry) ToMap() map[string]any {
	return map[string]any{"name": f.Name, "type": f.Type, "doc": f.Doc}
}

// ModuleEntry is a module's summary; Fields/Methods are populated only for the detail view.
type ModuleEntry struct {
	Name    string              `json:"name"              yaml:"name"`
	Doc     string              `json:"doc,omitempty"     yaml:"doc,omitempty"`
	Fields  []ModuleFieldEntry  `json:"fields,omitempty"  yaml:"fields,omitempty"`
	Methods []ModuleMethodEntry `json:"methods,omitempty" yaml:"methods,omitempty"`
}

// ToMap is the Buzz boundary map magus.modules / magus.module return:
// {name, doc, fields, methods}. fields/methods are always present (empty in the
// summary view). The generated/hand-written bindings marshal it via host.Mapper.
func (e ModuleEntry) ToMap() map[string]any {
	fields := make([]any, len(e.Fields))
	for i, f := range e.Fields {
		fields[i] = f.ToMap()
	}
	methods := make([]any, len(e.Methods))
	for i, m := range e.Methods {
		methods[i] = m.ToMap()
	}
	return map[string]any{"name": e.Name, "doc": e.Doc, "fields": fields, "methods": methods}
}

// ModulesOutput is the top-level result for "describe modules" / "describe module <name>".
type ModulesOutput struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Modules    []ModuleEntry `json:"modules"    yaml:"modules"`
}

// EvaluatedTargetDefinition is the human-readable description of an evaluated target shown by "magus describe".
const EvaluatedTargetDefinition = "An evaluated target shows the fully-resolved " +
	"dispatch plan for a specific path:target pair: the workspace-rooted source and " +
	"output globs that feed the cache key, the spells that will fire (with " +
	"target-specific sources and effective claims after weight/add/remove resolution), " +
	"and any behavioural policy (CheckClean, TrackVolatile, Exclusive)."

// EvaluatedSpellEntry is one spell's contribution to an evaluated target.
type EvaluatedSpellEntry struct {
	Name            string   `json:"name"                        yaml:"name"`
	TargetSources   []string `json:"target_sources,omitempty"    yaml:"target_sources,omitempty"`
	EffectiveClaims []string `json:"effective_claims,omitempty"  yaml:"effective_claims,omitempty"`
	ClaimWeight     int      `json:"claim_weight,omitempty"      yaml:"claim_weight,omitempty"`
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
	CharmTrace []CharmTraceStep `json:"charm_trace,omitempty"       yaml:"charm_trace,omitempty"`
	// Conflicts lists the active charms whose edit is overridden by another active
	// charm on this command (both edit the same argument; the winner is decided by
	// sorted charm name, so the loser has no effect). Empty when the active charms
	// have disjoint edits. `magus describe target ...:a,b` surfaces it before a run.
	Conflicts []CharmConflict `json:"conflicts,omitempty"         yaml:"conflicts,omitempty"`
	// Service is set only when this spell's op is a service (a long-running process
	// magus supervises rather than runs to completion). It carries the static, pre-run
	// facts; Command above is the process itself. Nil for an ordinary command op.
	Service *ServiceView `json:"service,omitempty" yaml:"service,omitempty"`
}

// ServiceView is the static, pre-run description of a service op, shown by `magus
// describe target` when the target is a service. Every field is known without
// starting the service; live registry state (ref-count, probe status) needs the
// daemon and is not part of this static view.
type ServiceView struct {
	Readiness   []string `json:"readiness,omitempty"   yaml:"readiness,omitempty"`   // probe command polled until it exits 0, if any
	Stop        []string `json:"stop,omitempty"        yaml:"stop,omitempty"`        // graceful-shutdown command, if any
	Idle        string   `json:"idle,omitempty"        yaml:"idle,omitempty"`        // idle-timeout override (a duration), else the daemon default
	Distinct    string   `json:"distinct,omitempty"    yaml:"distinct,omitempty"`    // dedup opt-out reason; empty means the instance is shared
	Fingerprint string   `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"` // content hash that keys shared-instance dedup
}

// CharmConflict reports an active charm (Name) whose edit is overwritten by another
// active charm (OverriddenBy) on the same command, so Name has no effect there. The
// winner is decided by sorted charm name, not declared precedence.
type CharmConflict struct {
	Name         string `json:"name"                    yaml:"name"`
	OverriddenBy string `json:"overridden_by,omitempty" yaml:"overridden_by,omitempty"`
}

// CharmTraceStep is one line of a charm-application trace: the command (cmd as
// element 0) after the named charm's patch applies on top of the prior step. The
// base step (before any charm) has an empty Charm.
type CharmTraceStep struct {
	Charm   string   `json:"charm,omitempty"   yaml:"charm,omitempty"`
	Command []string `json:"command"           yaml:"command"`
}

// EvaluatedTargetEntry is the fully-resolved view of a single path:target pair.
type EvaluatedTargetEntry struct {
	Project   string                `json:"project"             yaml:"project"`
	Target    string                `json:"target"              yaml:"target"`
	Dir       string                `json:"dir"                 yaml:"dir"`
	Sources   []string              `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string              `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string              `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Charms    []string              `json:"charms,omitempty"     yaml:"charms,omitempty"`
	Spells    []EvaluatedSpellEntry `json:"spells,omitempty"     yaml:"spells,omitempty"`
	Policy    *Target               `json:"policy,omitempty"    yaml:"policy,omitempty"` // only the policy fields of Target are meaningful (SkipCache/Exclusive/FailOnDrift/RetryOnVolatile)
	Exclusive bool                  `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
}

// EvaluatedTargetsOutput is the top-level result for "describe target <ref>".
type EvaluatedTargetsOutput struct {
	Definition string                 `json:"definition" yaml:"definition"`
	Count      int                    `json:"count"      yaml:"count"`
	Targets    []EvaluatedTargetEntry `json:"targets"    yaml:"targets"`
}

// EvaluatedProjectEntry is the fully-resolved view of a project.
type EvaluatedProjectEntry struct {
	Path           string                `json:"path"                       yaml:"path"`
	Dir            string                `json:"dir"                        yaml:"dir"`
	Sources        []string              `json:"sources,omitempty"          yaml:"sources,omitempty"`
	Outputs        []string              `json:"outputs,omitempty"          yaml:"outputs,omitempty"`
	DependsOn      []string              `json:"depends_on,omitempty"       yaml:"depends_on,omitempty"`
	Spells         []EvaluatedSpellEntry `json:"spells,omitempty"           yaml:"spells,omitempty"`
	TargetPolicies map[string]Target     `json:"target_policies,omitempty"  yaml:"target_policies,omitempty"`
	Exclusive      bool                  `json:"exclusive,omitempty"        yaml:"exclusive,omitempty"`
}

// EvaluatedProjectsOutput is the top-level result for "describe projects --evaluated".
type EvaluatedProjectsOutput struct {
	Definition string                  `json:"definition" yaml:"definition"`
	Workspace  string                  `json:"workspace"  yaml:"workspace"`
	Count      int                     `json:"count"      yaml:"count"`
	Projects   []EvaluatedProjectEntry `json:"projects"   yaml:"projects"`
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

// WorkspacesOutput is the top-level result for "describe workspaces".
type WorkspacesOutput struct {
	Definition string           `json:"definition"  yaml:"definition"`
	Count      int              `json:"count"       yaml:"count"`
	Workspaces []WorkspaceEntry `json:"workspaces"  yaml:"workspaces"`
}

// WorkspaceConfig carries infrastructure details for DescribeWorkspaces
// that are not part of the WorkspaceRepository interface (cache path,
// concurrency).
type WorkspaceConfig struct {
	CacheDir    string
	Concurrency int
}

// FileDefinition is the human-readable description printed by "magus describe file".
const FileDefinition = "Describe file classifies paths against the workspace's declared " +
	"globs: the project that owns each path, whether it is a declared output (generated: " +
	"regenerate it, never hand-edit) or a declared source (it feeds cache keys and the " +
	"affected set), and which projects claim it either way. It answers \"can I disregard " +
	"this changed file\" from the workspace's own declarations."

// FileEntry classifies one workspace-relative path.
type FileEntry struct {
	Path string `json:"path" yaml:"path"`
	// Project is the owning project by directory containment (longest project
	// path prefixing the file), empty when no project dir contains it.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	// Role summarizes the strongest claim: "output" (a declared output glob
	// matches - the file is generated), "source" (a declared source glob
	// matches), or "unclaimed" (no project declares it; it invalidates no cache
	// key and affects no target).
	Role string `json:"role" yaml:"role"`
	// OutputOf and SourceOf list the projects whose declared output/source globs
	// match the path. A path can be both (a committed generated file is often a
	// source of downstream targets); Role reports output in that case because
	// the regeneration rule dominates how the file should be treated.
	OutputOf []string `json:"output_of,omitempty" yaml:"output_of,omitempty"`
	SourceOf []string `json:"source_of,omitempty" yaml:"source_of,omitempty"`
	// Hint is the one-line handling rule for the role, ready to surface to a
	// human or an agent.
	Hint string `json:"hint,omitempty" yaml:"hint,omitempty"`
}

// FilesOutput is the top-level result for "describe file <path>...".
type FilesOutput struct {
	Definition string      `json:"definition" yaml:"definition"`
	Count      int         `json:"count"      yaml:"count"`
	Files      []FileEntry `json:"files"      yaml:"files"`
}
