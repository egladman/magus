package spellruntime

import (
	_ "embed"
	"strings"
)

// SpellModulePath is the import path of the canonical spell-authored value-types
// module: the shapes a spell op WRITES (Target, Command, Service, Charm, PatchOp),
// as opposed to the shapes a host method RETURNS (ExecResult, Tag, Projects, ...),
// which now ship with the host module that returns them (see hosttypes.go and
// internal/interp/bindings/modules.go). A spell does `import "magus/spell";` to
// bring Target into scope so its mgs_listTargets can be typed as a map of
// fun(Target, fun(any)) handlers instead of `any`. The runtime registers the
// module as embedded source (see the buzz bindings' registerMagusModules); the
// built-in spell generator inlines it so each compiled built-in is
// self-contained.
//
// "magus/spell" does not collide with the "magus/spell/<name>" path each built-in
// spell handle is reachable under (spells.ModulePrefix): those are different exact
// map keys in the session's native-module/declaration tables, and a bare "magus/spell"
// import reads naturally as "the types a spell is built from".
const SpellModulePath = "magus/spell"

// PathSource is the generated lexical filesystem-reference type used by spell
// metadata. It belongs in magus/spell because declarations such as inputs and
// manifests are authored by spells before any host module is invoked.
//
//go:embed gen/types/path.buzz
var PathSource string

// ManifestSource is the generated mirror of spells.Manifest: the file a project's
// dependencies are declared in, plus the lockfiles its ecosystem resolves them into.
// It belongs in magus/spell for the same reason Path does - mgs_listManifests is
// authored before any host module is invoked - and it references no other mirror
// (its fields are a str and a [str]), so its position in the bundle is free.
//
//go:embed gen/types/manifest.buzz
var ManifestSource string

// TargetModuleSource is the generated Buzz `object Target` mirror of types.Target
// (see cmd/magus-utils types), the canonical work-unit value type. It is consumed
// both at runtime (as part of the magus/spell declarations) and at built-in
// generation time (inlined into each built-in via SelfContainedBuiltinSource).
//
//go:embed gen/types/target.buzz
var TargetModuleSource string

// PatchOpSource / CharmTypeSource / CommandSource are the generated Buzz `object`
// mirrors of spells.PatchOp, spells.Charm, and types.Run: the {cmd, args, charms}
// command a command target's handler hands to its cb callback, down to the RFC 6902
// ops. Unlike the other object mirrors they are inlined into self-contained
// built-ins (every command spell references Run), so they ship in the magus/spell
// bundle (see builtinModuleSources). Order matters in that bundle: PatchOp precedes
// Charm (Charm.ops is [PatchOp]) precedes Run (Run.charms is {str: Charm}).
//
//go:embed gen/types/patchop.buzz
var PatchOpSource string

//go:embed gen/types/charm.buzz
var CharmTypeSource string

// HintSource is the generated mirror of spells.Hint: one {match, then} failure
// classification a command op declares. It must PRECEDE CommandSource in the bundle -
// Command.hints is [Hint] - and it references nothing itself.
//
//go:embed gen/types/hint.buzz
var HintSource string

//go:embed gen/types/command.buzz
var CommandSource string

// SecretSource is the generated mirror of spells.Secret - what a provider spell's
// resolve_secret op returns. It references nothing, so it has no ordering constraint in
// the bundle.
//
//go:embed gen/types/secret.buzz
var SecretSource string

// VersionKeySource is the generated mirror of spells.VersionKey - what a probed tool
// contributes to the cache key - together with the VersionComponent enum its upTo
// field is typed as. It ships in the magus/spell bundle so a spell can declare
// mgs_getVersionKey, and it carries its own enum, so it has no ordering constraint
// against the others.
//
//go:embed gen/types/versionkey.buzz
var VersionKeySource string

// VersionBoundsSource is the generated mirror of spells.VersionBounds - the window of
// versions a probed tool is allowed to report, as an inclusive min and an exclusive
// below. It ships in the magus/spell bundle so a spell can declare what its ops need,
// and it references nothing, so it has no ordering constraint against the others.
//
//go:embed gen/types/versionbounds.buzz
var VersionBoundsSource string

// ToolSource is the generated mirror of spells.Tool: everything a spell declares about
// one binary it drives. It must FOLLOW VersionKeySource and VersionBoundsSource in the
// bundle - Tool.key is a VersionKey and Tool.supported is a VersionBounds - and Command
// is already declared ahead of all three.
//
//go:embed gen/types/tool.buzz
var ToolSource string

// CommentBlockSource / QuoteSource / CommentSyntaxSource are the generated mirrors of
// the comment/string syntax a spell declares via mgs_getCommentSyntax. The two leaves
// must PRECEDE CommentSyntaxSource in the bundle (its fields are [CommentBlock] and
// [Quote]); nothing else references them.
//
//go:embed gen/types/commentblock.buzz
var CommentBlockSource string

//go:embed gen/types/quote.buzz
var QuoteSource string

//go:embed gen/types/commentsyntax.buzz
var CommentSyntaxSource string

//go:embed gen/types/language.buzz
var LanguageSource string

// ServiceSource is the generated Buzz `object Service` mirror of spells.Service: the
// {command, readiness, stop} a service op returns, each field a Command (command is the
// process; readiness/stop are optional). It ships in the magus/spell bundle so a spell
// can author a service op; it must follow CommandSource there (Service's fields are
// typed Command).
//
//go:embed gen/types/service.buzz
var ServiceSource string

// ProjectSource is the generated Buzz `object Project` mirror of spells.Project: one
// project a workspace-provider spell's list_projects contract returns. It ships in
// the magus/spell bundle beside the other shapes a spell AUTHORS; it references no
// other mirror, so its position in that bundle is free.
//
//go:embed gen/types/project.buzz
var ProjectSource string

// CharmModulePath is the import path of the pure-Buzz charm module.
const CharmModulePath = "magus/charm"

// CharmModuleSource is the pure-Buzz mirror of the charm host module
// (std/charm.go), shipped as the magus/charm declarations. Unlike the
// type mirrors it is hand-written (charm's constructors are logic, not a struct),
// kept in lockstep with the Go module by charm_parity_test. A self-contained
// built-in command spell imports it (`import "magus/charm"`) to build patches with
// charm.after / charm.set / ... instead of hand-written positional pointers; it is
// pure Buzz with no host calls, so it compiles into a bare built-in.
//
//go:embed charm.buzz
var CharmModuleSource string

// SpellModuleSource is the magus/spell bundle: the spell-authored value types in
// their declare-before-use order (PatchOp before Charm before Command before
// Service, each referencing the prior; Target and Project have no cross-references
// so their position is free). Shared by the runtime registration (modules.go) and
// the built-in inliner (builtinModuleSources) below, so the two can't drift apart.
var SpellModuleSource = strings.Join([]string{PathSource, ManifestSource, TargetModuleSource, PatchOpSource, CharmTypeSource, HintSource, CommandSource, ServiceSource, VersionKeySource, VersionBoundsSource, ToolSource, CommentBlockSource, QuoteSource, CommentSyntaxSource, LanguageSource, ProjectSource, SecretSource}, "\n")

// builtinModuleSources maps an import path a self-contained built-in may use to
// the module source prepended in its place (imports emit no bytecode, so an
// imported symbol would be missing when the built-in runs from .bo). magus/charm
// carries the pure-Buzz patch constructors. Any other import means the spell needs
// host bindings and is not a built-in.
var builtinModuleSources = map[string]string{
	SpellModulePath: SpellModuleSource,
	CharmModulePath: CharmModuleSource,
}

// SelfContainedBuiltinSource prepares a spell source for a bare compile into an
// embedded built-in. A built-in may import only the inlinable pure-Buzz modules
// (magus/spell, magus/charm): each such import is stripped and the
// module's source prepended, so the compiled chunk carries the symbols itself.
// Returns ok=false if the source imports any other module - such a spell needs
// host bindings a bare compile can't provide and is not a built-in. Shared by the
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
// imports magus/spell for the Charm type), expanding a module's own imports
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
