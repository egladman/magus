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

// harnessContract is the mgs_ functions a harness spell exports, one field per function
// typed as what it returns. ",required" marks the one a harness must export; a function
// it leaves out is a capability its host lacks. magus-utils contract parses it into
// decode.HarnessFuncs, and nothing constructs it.
type harnessContract struct {
	// The workspace-local host JSON document the harness maintains, such as
	// .cursor/hooks.json, and the defaults it sets there.
	Config types.HarnessConfig `mgs:"mgs_getHarnessConfig,required"`
	// Where the host reads installed magus skills, and which form to install.
	Skills types.HarnessSkills `mgs:"mgs_getSkills"`
	// The host JSON fragments merged into the config, verbatim: magus does not rewrite a
	// command field.
	Entries []types.ManagedEntries `mgs:"mgs_listEntries"`
	// The hooks the entries run, as data. magus checks each one appears in the entries,
	// so a hook declared here and missing from the rendered JSON is drift.
	Hooks []types.HookCommand `mgs:"mgs_listHooks"`
	// The MCP setup guidance describe prints. magus never writes host MCP client config.
	MCP types.McpServer `mgs:"mgs_getMcp"`
	// The host's own tools and what each can do. A tool no row names is one the guard
	// cannot judge.
	Tools types.HarnessTools `mgs:"mgs_listTools"`
	// How long the host waits on a foreground shell command and on one hook.
	Limits types.Limits `mgs:"mgs_getLimits"`
	// The models the host runs, with prices in USD per million tokens. A person's
	// agents.models overrides these row by row.
	Models []types.ModelRow `mgs:"mgs_listModels"`
	// Plain host settings kept in place, such as a plugin to enable. A value someone set
	// differently is left alone and named, never overwritten.
	Settings []types.Setting `mgs:"mgs_listSettings"`
	// The subagents magus ships, rendered in the host's format as whole files.
	RenderAgents func(agents []types.ShippedAgent) []types.AgentFile `mgs:"mgs_renderAgents"`
	// The host-native approval prompts that carry a guard verdict of ask to the person on
	// a host whose hooks cannot prompt. humanOnly maps each target the workspace declares
	// human_only to its reason.
	RenderPrompts func(humanOnly map[string]string) []types.Prompt `mgs:"mgs_renderPrompts"`
}
