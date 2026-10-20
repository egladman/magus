package spells

import "github.com/egladman/magus/types"

// HarnessContract is the mgs_ functions an agent-host harness spell exports, one field
// per function typed as what it returns; a func field takes arguments. ",required" marks
// the one a spell must export to be a harness, and a function it leaves out is a
// capability its host lacks. magus-utils contract generates decode.HarnessFuncs from it;
// nothing constructs the struct.
type HarnessContract struct {
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
