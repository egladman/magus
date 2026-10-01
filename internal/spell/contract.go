package spell

import (
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// contractEntry describes one optional entry in the mgs_ spell contract. The
// resolver (resolve.go) iterates OptionalContract, so the optional functions and
// the decoder keys they map to live in one canonical list rather than being
// spelled out at each call site.
type contractEntry struct {
	Name  string // exported mgs_ function name
	Field string // decoder field key the resolved value is stored under
	// Shape is what the entry's Buzz value is made of, and therefore which reduction
	// the resolver runs before storing it.
	//
	// It lives HERE, on the entry, rather than in a switch over field names in
	// resolve.go, because a field name spelled in two places is a field name that can
	// be renamed in one. That is not hypothetical: renaming mgs_listManifests to
	// mgs_listVersionFiles updated this list, the decoder and all four spell sources,
	// and the compiled spells exported the new name, but the
	// switch still said "manifests", so pathValues quietly stopped running, the Path
	// objects were never reduced to strings, and the decoded field came back EMPTY with
	// nothing pointing at the cause. The rename was reverted over it. One list means the
	// next rename carries this behavior along with it.
	//
	// An enum rather than a bool per shape: with three of them, a second bool beside the
	// first encodes "both set" as a reachable state that means nothing.
	Shape contractShape
}

// contractShape is the element type of a contract entry's Buzz list value.
type contractShape uint8

const (
	// ShapeStrs is the zero value: a [str] stored as-is.
	ShapeStrs contractShape = iota
	// ShapePaths is a [Path], reduced to a [str] by pathValues. The Path object's
	// other fields (base, isDir) are DISCARDED: the cache descriptor wants the
	// lexical value, because glob matching does not resolve filesystem paths.
	ShapePaths
	// ShapeManifests is a [Manifest], kept structured because its lockCandidates
	// field is data the descriptor needs, not decoration. A spell still returning
	// [Path] here decodes as manifests declaring no lockfile: both objects carry a
	// .value and the reduction reads keys structurally, so the pre-Manifest contract
	// keeps loading rather than failing.
	ShapeManifests
)

// OptionalContract is the canonical list of optional mgs_ functions a spell
// module may export (mgs_getName is required and handled separately by the
// resolver). Resolve calls each present function and stores its result under
// Field. Treat as read-only.
//
// MGS functions take no arguments. They run while Magus is discovering a spell,
// before there is a selected target or execution context; a target's magus.Context
// would therefore be fabricated data at this boundary. Per-invocation typed inputs
// belong on ordinary exported spell functions instead. Every scalar and list
// contribution (needs, provides, version_cmd, opaque) resolves uniformly.
// The "ops" entry (mgs_listTargets) is the exception:
// resolveOps post-processes it to extract function-valued op handlers into
// command records (the form the built-in spells use). Record-shaped ops pass
// through unchanged. See docs/engines.md.
var OptionalContract = []contractEntry{
	{Name: "mgs_listRequiredGlobs", Field: "needs", Shape: ShapePaths},
	{Name: "mgs_listProvidedGlobs", Field: "provides", Shape: ShapePaths},
	{Name: "mgs_listClaimedGlobs", Field: "claims", Shape: ShapePaths},
	{Name: "mgs_listIgnoreDirs", Field: "ignore_dirs", Shape: ShapePaths},
	{Name: "mgs_listManifests", Field: "manifests", Shape: ShapeManifests},
	{Name: "mgs_listScriptRunners", Field: "script_runners"},
	{Name: "mgs_getTools", Field: "tools"},
	{Name: "mgs_getLanguage", Field: "language"},
	{Name: "mgs_getSymbolIndexer", Field: "symbol_indexer"},
	{Name: "mgs_getSandbox", Field: "sandbox"},
	{Name: "mgs_isOpaque", Field: "opaque"},
	{Name: "mgs_getModeArgs", Field: "mode_args"},
	{Name: "mgs_listTargets", Field: "ops"},
}

// Contract is a spell's load-time answer, one field per mgs_ function, typed as the
// Buzz value that function returns. The mgs tag names the function; ",required" marks
// the one a module must export to be a spell, and ",handler" marks a map whose values a
// spell writes as `fun(Target) T` and magus calls once to read the T.
//
// magus-utils contract reads this declaration from source and generates the signature
// table the contract check compares each spell against (decode.ContractFuncs) and the
// reference page docs/reference/spell-contract.md. Each field's doc comment is that
// page's prose, so it addresses a spell author and does not open with the field name.
//
// A pointer field is a function a spell may leave out: its return type is the element
// type, and absence is not exporting the function.
type Contract struct {
	// The spell's name, the one a magusfile imports it by.
	Name string `mgs:"mgs_getName,required"`
	// The files every op of the spell reads. They key its cache and pull its project
	// into the affected set.
	RequiredGlobs []types.Path `mgs:"mgs_listRequiredGlobs"`
	// The files the spell's ops write.
	ProvidedGlobs []types.Path `mgs:"mgs_listProvidedGlobs"`
	// Directories the spell's ecosystem generates (vendor, node_modules, target), which
	// the input-hashing walk prunes. Each Path sets isDir = true.
	IgnoreDirs []types.Path `mgs:"mgs_listIgnoreDirs"`
	// The candidate manifests of the spell's ecosystem, in order: the first one present
	// in a project directory is its manifest. Each names the lockfiles it may carry.
	Manifests []spells.Manifest `mgs:"mgs_listManifests"`
	// The argv prefixes that run a script a manifest defines, which doctor reports as
	// MGS1049.
	ScriptRunners []spells.Command `mgs:"mgs_listScriptRunners"`
	// Every binary the spell's ops run, keyed by the bin name a Command names, each with
	// the probe that reads its version into the cache key.
	Tools map[string]spells.Tool `mgs:"mgs_getTools"`
	// The source language the spell adapts: its name, its file extensions and, when the
	// spell can declare it honestly, its comment and string syntax.
	Language *spells.Language `mgs:"mgs_getLanguage"`
	// The command that writes the spell's SCIP symbol index. magus runs it as the
	// reserved index op.
	SymbolIndexer *spells.SymbolIndexer `mgs:"mgs_getSymbolIndexer"`
	// What the spell's tools need from the host beyond the sandbox's own grants.
	Sandbox *spells.Sandbox `mgs:"mgs_getSandbox"`
	// Whether the spell delegates to a foreign process that manages its own dependency
	// graph, so magus treats the project as a black box.
	Opaque bool `mgs:"mgs_isOpaque"`
	// The args that tell an op apart from other uses of a program with no subcommand,
	// keyed by op name: node runs scripts too, and only --test is node-test.
	ModeArgs map[string][]string `mgs:"mgs_getModeArgs"`
	// The spell's ops, keyed by op name. magus calls each handler once at load, with a
	// null Target, to read the Command it declares, so a handler must not read its Target.
	Targets map[string]spells.Command `mgs:"mgs_listTargets,handler"`
	// The spell's long-running ops, keyed by op name, each read the way a target handler
	// is.
	Services map[string]spells.Service `mgs:"mgs_listServices,handler"`
}
