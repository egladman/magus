package spell

import (
	_ "embed"
	"strings"
)

// TargetModulePath is the import path of the canonical magus value-types module.
// A spell does `import "magus/target";` to bring Target into scope so its
// mgs_listTargets can be typed as a map of fun(Target, fun(any)) handlers instead
// of `any`. The runtime registers the module as embedded source (see the buzz
// bindings' registerMagusModules); the built-in spell generator inlines it so each
// compiled built-in is self-contained.
const TargetModulePath = "magus/target"

// TargetModuleSource is the generated Buzz `object Target` mirror of types.Target
// (see cmd/magus-utils types), the canonical work-unit value type. It is consumed
// both at runtime (as part of the magus/target source module) and at built-in
// generation time (inlined into each built-in via SelfContainedBuiltinSource). The
// other value types (ExecResult/Commit/FileInfo/...) are appended at runtime
// registration only (see registerMagusModules), since built-ins don't use them.
//
//go:generate go run ../../cmd/magus-utils types -type Target -out gen/types/target.buzz
//go:embed gen/types/target.buzz
var TargetModuleSource string

// PatchOpSource / CharmTypeSource / CommandSource are the generated Buzz `object`
// mirrors of types.PatchOp, types.Charm, and types.Run: the {cmd, args, charms}
// command a command target's handler hands to its cb callback, down to the RFC 6902
// ops. Unlike the other record mirrors they are inlined into self-contained
// built-ins (every command spell references Run), so they ship in the magus/target
// bundle (see builtinModuleSources). Order matters in that bundle: PatchOp precedes
// Charm (Charm.ops is [PatchOp]) precedes Run (Run.charms is {str: Charm}).
//
//go:generate go run ../../cmd/magus-utils types -type PatchOp -out gen/types/patchop.buzz
//go:embed gen/types/patchop.buzz
var PatchOpSource string

//go:generate go run ../../cmd/magus-utils types -type Charm -out gen/types/charm.buzz
//go:embed gen/types/charm.buzz
var CharmTypeSource string

//go:generate go run ../../cmd/magus-utils types -type Command -out gen/types/command.buzz
//go:embed gen/types/command.buzz
var CommandSource string

// ServiceSource is the generated Buzz `object Service` mirror of types.Service: the
// {command, readiness, stop} a service op returns, each field a Command (command is the
// process; readiness/stop are optional). It ships in the magus/target bundle so a spell
// can author a service op; it must follow CommandSource there (Service's fields are
// typed Command).
//
//go:generate go run ../../cmd/magus-utils types -type Service -out gen/types/service.buzz
//go:embed gen/types/service.buzz
var ServiceSource string

// ExecResultSource is the generated Buzz `object ExecResult` mirror of
// types.ExecResult (see cmd/magus-utils types). It ships alongside the other value
// types in the magus/target module so a magusfile can annotate an os.exec /
// magus.describe / captured-op result as `> ExecResult`; the runtime value is the
// matching {stdout, stderr, code, ok} record (see run.ExecResult.Record).
//
//go:generate go run ../../cmd/magus-utils types -type ExecResult -out gen/types/execresult.buzz
//go:embed gen/types/execresult.buzz
var ExecResultSource string

// CommitAuthorSource / CommitSource are the generated Buzz mirrors of
// types.CommitAuthor and types.CommitRecord (the boundary view of a Commit). A
// magusfile annotates a vcs.commit / vcs.history result `> Commit` for
// compile-checked field access. CommitAuthor must precede Commit in the module
// source (Commit's author field is typed CommitAuthor).
//
//go:generate go run ../../cmd/magus-utils types -type CommitAuthor -out gen/types/commitauthor.buzz
//go:embed gen/types/commitauthor.buzz
var CommitAuthorSource string

//go:generate go run ../../cmd/magus-utils types -type Commit -out gen/types/commit.buzz
//go:embed gen/types/commit.buzz
var CommitSource string

// FileInfoSource / HTTPResponseSource / SemverVersionSource / URLSource are the
// generated Buzz mirrors of the remaining host-method record shapes (fs.stat,
// http.*, semver.parse, encoding.parse_url), shipped in the magus/target module
// so a magusfile can annotate those results for compile-checked field access.
//
//go:generate go run ../../cmd/magus-utils types -type FileInfo -out gen/types/fileinfo.buzz
//go:embed gen/types/fileinfo.buzz
var FileInfoSource string

//go:generate go run ../../cmd/magus-utils types -type HttpResponse -out gen/types/httpresponse.buzz
//go:embed gen/types/httpresponse.buzz
var HTTPResponseSource string

//go:generate go run ../../cmd/magus-utils types -type SemverVersion -out gen/types/semverversion.buzz
//go:embed gen/types/semverversion.buzz
var SemverVersionSource string

//go:generate go run ../../cmd/magus-utils types -type URL -out gen/types/url.buzz
//go:embed gen/types/url.buzz
var URLSource string

// ProjectEntrySource / ProjectsSource are the generated Buzz mirrors of
// types.ProjectEntry and types.ProjectsOutput: what magus.ls returns. They close
// a documented gap - magus.ls's own doc told readers to annotate `> Projects`
// while no such type existed, so the annotation it recommended did not compile.
// ProjectEntry must precede Projects in the module source (Projects.projects is
// [ProjectEntry]).
//
//go:generate go run ../../cmd/magus-utils types -type ProjectEntry -out gen/types/projectentry.buzz
//go:embed gen/types/projectentry.buzz
var ProjectEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Projects -out gen/types/projects.buzz
//go:embed gen/types/projects.buzz
var ProjectsSource string

// TagSource / AffectedSource / GraphSource and the Module trio complete the set:
// every Go boundary type with a ToMap (the marker for "this crosses into Buzz")
// now has a mirror, so a magusfile can name the type of any host result rather
// than indexing an untyped map. Affected and Graph are magus.affected's and
// magus.graph's returns - the in-process verbs beside ls, which had the same gap
// Projects did.
//
//go:generate go run ../../cmd/magus-utils types -type Tag -out gen/types/tag.buzz
//go:embed gen/types/tag.buzz
var TagSource string

//go:generate go run ../../cmd/magus-utils types -type Affected -out gen/types/affected.buzz
//go:embed gen/types/affected.buzz
var AffectedSource string

//go:generate go run ../../cmd/magus-utils types -type Graph -out gen/types/graph.buzz
//go:embed gen/types/graph.buzz
var GraphSource string

// The magus.targets() result: every project's TARGET graph. The nested entries
// precede their parents, because each parent's fields name them.
//
//go:generate go run ../../cmd/magus-utils types -type TargetSpellUse -out gen/types/targetspelluse.buzz
//go:embed gen/types/targetspelluse.buzz
var TargetSpellUseSource string

//go:generate go run ../../cmd/magus-utils types -type CrossTargetRef -out gen/types/crosstargetref.buzz
//go:embed gen/types/crosstargetref.buzz
var CrossTargetRefSource string

//go:generate go run ../../cmd/magus-utils types -type InputRef -out gen/types/inputref.buzz
//go:embed gen/types/inputref.buzz
var InputRefSource string

//go:generate go run ../../cmd/magus-utils types -type OutputRef -out gen/types/outputref.buzz
//go:embed gen/types/outputref.buzz
var OutputRefSource string

//go:generate go run ../../cmd/magus-utils types -type TargetGraphNode -out gen/types/targetgraphnode.buzz
//go:embed gen/types/targetgraphnode.buzz
var TargetGraphNodeSource string

//go:generate go run ../../cmd/magus-utils types -type TargetGraphProject -out gen/types/targetgraphproject.buzz
//go:embed gen/types/targetgraphproject.buzz
var TargetGraphProjectSource string

//go:generate go run ../../cmd/magus-utils types -type TargetGraph -out gen/types/targetgraph.buzz
//go:embed gen/types/targetgraph.buzz
var TargetGraphSource string

// ModuleFieldEntry and ModuleMethodEntry must precede Module (its fields/methods
// are lists of them).
//
//go:generate go run ../../cmd/magus-utils types -type ModuleFieldEntry -out gen/types/modulefieldentry.buzz
//go:embed gen/types/modulefieldentry.buzz
var ModuleFieldEntrySource string

//go:generate go run ../../cmd/magus-utils types -type ModuleMethodEntry -out gen/types/modulemethodentry.buzz
//go:embed gen/types/modulemethodentry.buzz
var ModuleMethodEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Module -out gen/types/module.buzz
//go:embed gen/types/module.buzz
var ModuleSource string

// CharmModulePath is the import path of the pure-Buzz charm module.
const CharmModulePath = "magus/charm"

// CharmModuleSource is the pure-Buzz mirror of the charm host module
// (std/charm.go), shipped as the magus/charm source module. Unlike the
// type mirrors it is hand-written (charm's constructors are logic, not a struct),
// kept in lockstep with the Go module by charm_parity_test. A self-contained
// built-in command spell imports it (`import "magus/charm"`) to build patches with
// charm.after / charm.set / ... instead of hand-written positional pointers; it is
// pure Buzz with no host calls, so it compiles into a bare built-in.
//
//go:embed charm.buzz
var CharmModuleSource string

// builtinModuleSources maps an import path a self-contained built-in may use to
// the module source prepended in its place (imports emit no bytecode, so an
// imported symbol would be missing when the built-in runs from .bo). magus/target
// carries the value types; magus/charm the patch constructors. Any other import
// means the spell needs host bindings and is not a built-in.
var builtinModuleSources = map[string]string{
	// The magus/target bundle carries Target plus the op value types
	// PatchOp/Charm/Command/Service (a command op returns a Command, a service op a
	// Service). Order is load-bearing: PatchOp before Charm before Command before
	// Service, since each references the prior.
	TargetModulePath: strings.Join([]string{TargetModuleSource, PatchOpSource, CharmTypeSource, CommandSource, ServiceSource}, "\n"),
	CharmModulePath:  CharmModuleSource,
}

// SelfContainedBuiltinSource prepares a spell source for a bare compile into an
// embedded built-in. A built-in may import only the inlinable pure-Buzz modules
// (magus/target, magus/charm): each such import is stripped and the module's
// source prepended, so the compiled chunk carries the symbols itself. Returns
// ok=false if the source imports any other module - such a spell needs host
// bindings a bare compile can't provide and is not a built-in. Shared by the
// built-in generator and the bytecode-parity test so both compile built-ins
// identically.
func SelfContainedBuiltinSource(src string) (string, bool) {
	body, prepend, ok := inlineBuiltinImports(src, map[string]bool{})
	if !ok {
		return "", false
	}
	if len(prepend) > 0 {
		return strings.Join(prepend, "\n") + "\n" + body, true
	}
	return body, true
}

// inlineBuiltinImports strips every inlinable import from src and returns src's
// remaining body plus the ordered, deduped module sources to prepend in their
// place. It recurses (an inlinable module may itself import another: magus/charm
// imports magus/target for the Charm type), expanding a module's own imports
// before the module, so a dependency is always defined before its dependent. seen
// carries the dedup set across the recursion; ok is false if src imports a
// non-inlinable host module.
func inlineBuiltinImports(src string, seen map[string]bool) (body string, prepend []string, ok bool) {
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "import ") {
			kept = append(kept, line)
			continue
		}
		path := importPath(line)
		modSrc, inlinable := builtinModuleSources[path]
		if !inlinable {
			return "", nil, false // imports a host module, not a built-in
		}
		if seen[path] {
			continue // already prepended; strip the duplicate import
		}
		seen[path] = true
		innerBody, innerPrepend, ok := inlineBuiltinImports(modSrc, seen)
		if !ok {
			return "", nil, false
		}
		prepend = append(prepend, innerPrepend...) // the module's deps, first
		prepend = append(prepend, innerBody)       // then the module itself
	}
	return strings.Join(kept, "\n"), prepend, true
}

// IsSelfContained reports whether src is a command spell: one that imports only the
// pure-types magus/target module (or nothing). Such a spell's op handlers run a
// declared command via the injected run-callback and carry no host side effects,
// so their specs can be extracted once at spec-resolution time (see resolveOps). A
// spell importing host modules (e.g. http) is a handler op spell whose handlers must
// be dispatched at invoke time instead.
func IsSelfContained(src string) bool {
	_, ok := SelfContainedBuiltinSource(src)
	return ok
}

// importPath extracts the quoted module path from an import line, or "" if none.
func importPath(line string) string {
	i := strings.IndexByte(line, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(line[i+1:], '"')
	if j < 0 {
		return ""
	}
	return line[i+1 : i+1+j]
}
