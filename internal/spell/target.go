package spell

import (
	_ "embed"
	"strings"
)

// TargetModulePath is the import path of the canonical magus value-types module.
// A spell does `import "magus/target";` to bring Target into scope so its
// mgs_listTargets can be typed as a map of fun(Target, fun(any)) handlers instead
// of `any`. The runtime registers the module as embedded source (see the buzz
// bindings' registerHostModules); the built-in spell generator inlines it so each
// compiled built-in is self-contained.
const TargetModulePath = "magus/target"

// TargetModuleSource is the generated Buzz `object Target` mirror of types.Target
// (see cmd/magus-utils types) — the canonical work-unit value type. It is consumed
// both at runtime (as part of the magus/target source module) and at built-in
// generation time (inlined into each built-in via SelfContainedBuiltinSource). The
// other value types (TargetQuery/ExecResult/OpResult/TargetResult) are appended at
// runtime registration only (see registerHostModules), since built-ins don't use
// them.
//
//go:generate go run ../../cmd/magus-utils types -type Target -out buzzlib/target.gen.buzz
//go:embed buzzlib/target.gen.buzz
var TargetModuleSource string

// PatchOpSource / CharmTypeSource / RunSource are the generated Buzz `object`
// mirrors of types.PatchOp, types.Charm, and types.Run — the {cmd, args, charms}
// command a command target's handler hands to its cb callback, down to the RFC 6902
// ops. Unlike the other record mirrors they are inlined into self-contained
// built-ins (every command spell references Run), so they ship in the magus/target
// bundle (see builtinModuleSources). Order matters in that bundle: PatchOp precedes
// Charm (Charm.ops is [PatchOp]) precedes Run (Run.charms is {str: Charm}).
//
//go:generate go run ../../cmd/magus-utils types -type PatchOp -out buzzlib/patchop.gen.buzz
//go:embed buzzlib/patchop.gen.buzz
var PatchOpSource string

//go:generate go run ../../cmd/magus-utils types -type Charm -out buzzlib/charm.gen.buzz
//go:embed buzzlib/charm.gen.buzz
var CharmTypeSource string

//go:generate go run ../../cmd/magus-utils types -type Run -out buzzlib/run.gen.buzz
//go:embed buzzlib/run.gen.buzz
var RunSource string

// TargetQuerySource is the generated Buzz `object TargetQuery` mirror of
// types.TargetQuery (see cmd/magus-utils types). It ships in the magus/target source
// module so a magusfile can annotate with or construct a TargetQuery;
// magus.target.literal/glob/regex return the same field shape.
//
//go:generate go run ../../cmd/magus-utils types -type TargetQuery -out buzzlib/targetquery.gen.buzz
//go:embed buzzlib/targetquery.gen.buzz
var TargetQuerySource string

// ExecResultSource is the generated Buzz `object ExecResult` mirror of
// types.ExecResult (see cmd/magus-utils types). It ships alongside the other value
// types in the magus/target module so a magusfile can annotate an os.exec /
// magus.cmd / captured-op result as `> ExecResult`; the runtime value is the
// matching {stdout, stderr, code, ok} record (see run.ExecResult.Record).
//
//go:generate go run ../../cmd/magus-utils types -type ExecResult -out buzzlib/execresult.gen.buzz
//go:embed buzzlib/execresult.gen.buzz
var ExecResultSource string

// CommitAuthorSource / CommitSource are the generated Buzz mirrors of
// types.CommitAuthor and types.CommitRecord (the boundary view of a Commit). A
// magusfile annotates a vcs.commit / vcs.history result `> Commit` for
// compile-checked field access. CommitAuthor must precede Commit in the module
// source (Commit's author field is typed CommitAuthor).
//
//go:generate go run ../../cmd/magus-utils types -type CommitAuthor -out buzzlib/commitauthor.gen.buzz
//go:embed buzzlib/commitauthor.gen.buzz
var CommitAuthorSource string

//go:generate go run ../../cmd/magus-utils types -type Commit -out buzzlib/commit.gen.buzz
//go:embed buzzlib/commit.gen.buzz
var CommitSource string

// FileInfoSource / HTTPResponseSource / SemverVersionSource / URLSource are the
// generated Buzz mirrors of the remaining host-method record shapes (fs.stat,
// http.*, semver.parse, encoding.parse_url), shipped in the magus/target module
// so a magusfile can annotate those results for compile-checked field access.
//
//go:generate go run ../../cmd/magus-utils types -type FileInfo -out buzzlib/fileinfo.gen.buzz
//go:embed buzzlib/fileinfo.gen.buzz
var FileInfoSource string

//go:generate go run ../../cmd/magus-utils types -type HttpResponse -out buzzlib/httpresponse.gen.buzz
//go:embed buzzlib/httpresponse.gen.buzz
var HTTPResponseSource string

//go:generate go run ../../cmd/magus-utils types -type SemverVersion -out buzzlib/semverversion.gen.buzz
//go:embed buzzlib/semverversion.gen.buzz
var SemverVersionSource string

//go:generate go run ../../cmd/magus-utils types -type URL -out buzzlib/url.gen.buzz
//go:embed buzzlib/url.gen.buzz
var URLSource string

// CharmModulePath is the import path of the pure-Buzz charm module.
const CharmModulePath = "magus/charm"

// CharmModuleSource is the pure-Buzz mirror of the charm host module
// (std/charm.go), shipped as the magus/charm source module. Unlike the
// type mirrors it is hand-written (charm's constructors are logic, not a struct),
// kept in lockstep with the Go module by charm_parity_test. A self-contained
// built-in command spell imports it (`import "magus/charm"`) to build patches with
// charm.after / charm.set / … instead of hand-written positional pointers; it is
// pure Buzz with no host calls, so it compiles into a bare built-in.
//
//go:embed buzzlib/charm.buzz
var CharmModuleSource string

// builtinModuleSources maps an import path a self-contained built-in may use to
// the module source prepended in its place (imports emit no bytecode, so an
// imported symbol would be missing when the built-in runs from .bo). magus/target
// carries the value types; magus/charm the patch constructors. Any other import
// means the spell needs host bindings and is not a built-in.
var builtinModuleSources = map[string]string{
	// The magus/target bundle carries Target plus the command value types
	// PatchOp/Charm/Run (every command spell references Run). Order is load-bearing:
	// PatchOp before Charm before Run, since each references the prior.
	TargetModulePath: strings.Join([]string{TargetModuleSource, PatchOpSource, CharmTypeSource, RunSource}, "\n"),
	CharmModulePath:  CharmModuleSource,
}

// SelfContainedBuiltinSource prepares a spell source for a bare compile into an
// embedded built-in. A built-in may import only the inlinable pure-Buzz modules
// (magus/target, magus/charm): each such import is stripped and the module's
// source prepended, so the compiled chunk carries the symbols itself. It returns
// ok=false if the source imports any other module — such a spell needs host
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
// place. It recurses — an inlinable module may itself import another (magus/charm
// imports magus/target for the Charm type) — expanding a module's own imports
// before the module, so a dependency is always defined before its dependent. seen
// carries the dedup set across the recursion (à la filepath.WalkDir threading its
// state); ok is false if src imports a non-inlinable host module.
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
			return "", nil, false // imports a host module → not a built-in
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

// IsSelfContained reports whether src is a command spell — one that imports only the
// pure-types magus/target module (or nothing). Such a spell's op handlers run a
// declared command via the injected run-callback and carry no host side effects,
// so their specs can be extracted once at spec-resolution time (see
// resolveOps). A spell importing host modules (e.g. magus/extra/http) is a
// handler op spell whose handlers must be dispatched at invoke time instead.
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
