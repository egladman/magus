package spell

import (
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Contract is a spell's load-time answer, one field per mgs_ function, typed as the
// Buzz value that function returns. The mgs tag names the function; ",required" marks
// the one a module must export to be a spell, and ",handler" marks a map whose values a
// spell writes as `fun(Target) T` and magus calls once to read the T.
//
// magus-utils contract generates from this declaration the decoders Resolve reads each
// export through (decode.ContractFuncs), the signatures the contract check compares a
// spell against, and docs/reference/spell-contract.md. A field's doc comment is that
// page's prose, so it addresses a spell author and does not open with the field name.
// A pointer field is a function a spell may leave out.
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
