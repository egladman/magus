package spells

// The harness contract: reserved function names a spell exports so a workspace can
// wire an agent host the same way it wires a remote cache or a CI provider.
//
// A fifth CONTRACT beside cache, CI, secret and review, detected the same way: by
// reserved function name on a spell a magusfile selected via magus\harness.provider.
// It is not a new subsystem and not a JSON file under harnesses/.
//
// Unlike cache.remote (one provider), a workspace may use several harnesses: one
// per agent host. Each spell describes that host's config path, skill install
// form, the opaque hook fragments Magus merges into the host's JSON, and optional
// MCP client wiring (token as a secret ref, never a plaintext secret in the file).
//
// A spell may implement a SUBSET. Each op is looked up by name when apply/verify
// needs it, so a missing one is a capability that provider lacks rather than a
// broken spell.
const (
	// HarnessConfigContract returns {path, defaults?} for the workspace-local
	// host JSON document this harness maintains (e.g. ".cursor/hooks.json").
	HarnessConfigContract = "harness_config"

	// HarnessSkillsContract returns {paths, form} naming where this host reads
	// installed magus-* skills and which skill form to install.
	HarnessSkillsContract = "harness_skills"

	// HarnessEntriesContract returns the managed_entries list: [{path, entries}, ...]
	// merged into the host config. Magus does not rewrite command fields.
	HarnessEntriesContract = "harness_entries"

	// HarnessMCPContract returns MCP client wiring: token_ref (secret provider
	// ref), url, optional workspace-relative path + document to merge, or
	// register-mode hint/argv printed by apply (never a separate subcommand).
	HarnessMCPContract = "harness_mcp"

	// HarnessPromptsContract returns the host-native approval prompts a harness keeps in
	// place: [{path, content}] for a whole file, or [{path, key, value}] for one value
	// inside a JSON document. It is how a guard verdict of ask reaches the person on a
	// host whose hooks cannot prompt.
	HarnessPromptsContract = "harness_prompts"

	// HarnessAgentsContract renders the named subagents magus ships in this host's format.
	// Optional: a host without a subagent file format omits it and receives none.
	//
	// magus invokes it with Params {"agents": [...]}, each agent a map of name, description,
	// instructions, read_only and, when the workspace maps a model to this harness's
	// cheapest tier, model: that host's own alias for it. The spell returns [{path,
	// content}]: whole workspace-relative files the descriptor owns. An agent with no model
	// key is rendered without a model line, so the host's default applies.
	HarnessAgentsContract = "harness_agents"

	// HarnessSettingsContract returns plain host settings a harness keeps in place:
	// [{path, key, value}], each one value inside a JSON document the person also edits,
	// such as a plugin to enable. Optional. A value someone set differently is left alone
	// and named, never overwritten.
	HarnessSettingsContract = "harness_settings"

	// HarnessLimitsContract returns {foreground_default_ms, foreground_max_ms,
	// hook_timeout_ms}: how long the host waits on a shell command that names no timeout,
	// and the longest timeout one may name, before it moves the command to the background;
	// and how long it waits on one hook before it stops waiting for the verdict. Optional,
	// and so is each key; a host that never backgrounds a command omits the first two.
	HarnessLimitsContract = "harness_limits"

	// HarnessModelsContract returns the models this host runs as rows: [{id, window,
	// input, output, cache_read, cache_write_5m, cache_write_1h, price_step,
	// step_factor}]. Prices are USD per million tokens; a price left out is unknown, never
	// free. Optional: a host that publishes no facts omits it. A person's agents.models
	// overrides these row by row.
	HarnessModelsContract = "harness_models"

	// HarnessToolsContract returns the host's own tools as {dir, rows: [{name,
	// capability}]}: capability is one of runs, reads, writes, spawns, network or inert, and
	// dir the workspace-relative directory `magus agent harness install` writes the rows
	// to, which the spell's hook wiring names to `magus shell --tool-rows`. Optional. A
	// tool no row names is one the guard cannot judge (the unjudged-tool rule).
	HarnessToolsContract = "harness_tools"
)
