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

	// HarnessPromptsContract returns the host-native approval prompts apply keeps in
	// place: [{path, content}] for a whole file, or [{path, key, value}] for one value
	// inside a JSON document. It is how a guard verdict of ask reaches the person on a
	// host whose hooks cannot prompt.
	HarnessPromptsContract = "harness_prompts"
)
