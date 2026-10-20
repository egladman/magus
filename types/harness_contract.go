package types

import (
	"path"
	"slices"
)

// The harness contract's records: what a harness spell's mgs_ functions return
// (spell.HarnessContract) and what magus hands them. Each crosses under its Go name, and
// the JSON tags keep a harness descriptor's wire shape.

// SkillForm says which entries a skill installation writes. It is a user-owned
// installation choice, not a claim about a host or model: magus cannot reliably observe
// either one.
//
// It is a superset of [SkillVariant] rather than the same type, because a form may name
// two bodies: SkillFormBoth writes each skill short and its twin full.
type SkillForm string

const (
	// SkillFormBoth writes each skill's short body under its own name plus a full twin.
	// The default, and the only form that leaves a delegated reader something to ask for.
	SkillFormBoth SkillForm = "both"
	// SkillFormShort writes one short body per skill and no twins.
	SkillFormShort SkillForm = "short"
	// SkillFormFull writes one full body per skill and no twins.
	SkillFormFull SkillForm = "full"
)

// Variant returns the body this form's primary entries carry. SkillFormBoth's twins are
// full regardless.
func (f SkillForm) Variant() SkillVariant {
	if f == SkillFormFull {
		return SkillVariantFull
	}
	return SkillVariantShort
}

// ToolCapability is what a host tool can do to a workspace, in magus's own words. The shape
// of a call's input decides how the guard judges it (a command field is a command line, a
// file path is a read or a write); the capability settles what shape cannot, and says which
// tools carry nothing to judge on purpose.
type ToolCapability string

const (
	ToolRuns   ToolCapability = "runs"
	ToolReads  ToolCapability = "reads"
	ToolWrites ToolCapability = "writes"
	ToolSpawns ToolCapability = "spawns"
	// ToolNetwork reaches the network and touches no workspace file: a browser.
	ToolNetwork ToolCapability = "network"
	// ToolInert touches no workspace file and runs nothing: a plan, a todo list, a web
	// lookup, a read of a background task's output.
	ToolInert ToolCapability = "inert"
)

// McpTransport is how a host reaches magus's MCP server.
type McpTransport string

const (
	// McpStdio is a `./magus mcp` process the host starts per session, in the checkout it
	// works in. Every shipped harness declares it.
	McpStdio McpTransport = "stdio"
	// McpHTTP is the long-lived `magus server` endpoint, shared by every client and
	// answering from the checkout and build that started it. It is for the console and
	// for clients with no shell.
	McpHTTP McpTransport = "http"
)

// HarnessConfig names the workspace-local JSON document a harness's managed entries merge
// into. Path is workspace-relative; an empty Path is a skills-only harness, with nothing to
// merge and no guard coverage to claim. Defaults are keys set into that document when it
// lacks them, an object default descending into the object the document holds; a value
// already present is someone's choice and stays.
type HarnessConfig struct {
	Path     string         `json:"path"`
	Defaults map[string]any `json:"defaults,omitempty"`
}

// HarnessSkills lists the workspace-relative directories a host reads installed magus
// skills from, and the form to install.
type HarnessSkills struct {
	Paths []string  `json:"paths"`
	Form  SkillForm `json:"form"`
}

// HookCommand is one hook a harness wires: the host event and matcher it rides, the glue
// script it runs (a basename under docs/guides/integrations/agents, without .buzz), the
// argv the script takes after `--`, and how long the host waits on it. A spell renders
// each into its host's JSON through magus/harness, and magus checks every one appears in
// the managed entries. An empty Matcher is an event that takes none.
type HookCommand struct {
	Event    string   `json:"event"`
	Matcher  string   `json:"matcher,omitempty"`
	Script   string   `json:"script"`
	Flags    []string `json:"flags,omitempty"`
	TimeoutS int      `json:"timeout_s,omitempty"`
}

// ManagedEntries declares exact host-config fragments to merge under the object keys Path
// names. Each entry is opaque host JSON: magus does not rewrite a command field.
type ManagedEntries struct {
	Path    []string         `json:"path"`
	Entries []map[string]any `json:"entries"`
}

// McpServer is a harness's MCP setup guidance. magus never writes host MCP client config:
// the person owns registration, and a plan carries only the rendered hint. Command is the
// argv a stdio host starts; URL the endpoint an http host reaches. TokenRef names the
// secret an http client sends, never the secret itself. Register is an argv sketch printed
// beside Hint, never executed; Hint and Register may name __COMMAND__, __URL__ and
// __TOKEN_REF__.
type McpServer struct {
	Transport McpTransport `json:"transport"`
	Command   []string     `json:"command,omitempty"`
	URL       string       `json:"url,omitempty"`
	TokenRef  string       `json:"token_ref,omitempty"`
	Hint      string       `json:"hint,omitempty"`
	Docs      string       `json:"docs,omitempty"`
	Register  []string     `json:"register,omitempty"`
}

// Price is what one model charges, in USD per million tokens. A price left at zero is
// unknown, never free.
type Price struct {
	Input        float64 `json:"input,omitzero"`
	CacheWrite5m float64 `json:"cache_write_5m,omitzero"`
	CacheWrite1h float64 `json:"cache_write_1h,omitzero"`
	CacheRead    float64 `json:"cache_read,omitzero"`
	Output       float64 `json:"output,omitzero"`
}

// ModelRow is one model a host runs: its exact id, the model-tier case it runs ("" for
// none), the short alias the host takes for it in a spawn ("" for none), its context window
// in tokens, its price, and its long-context step, whose zero Above means none.
type ModelRow struct {
	ID     string    `json:"id"`
	Tier   string    `json:"tier,omitzero"`
	Alias  string    `json:"alias,omitzero"`
	Window int64     `json:"window,omitzero"`
	Price  Price     `json:"price"`
	Step   PriceStep `json:"step,omitzero"`
}

// HarnessTool is one row of a host's tool table: a tool by the host's own name, and what
// it can do.
type HarnessTool struct {
	Name       string         `json:"name"`
	Capability ToolCapability `json:"capability"`
}

// HarnessTools is the table of a host's own tools: Dir is the workspace-relative directory
// `magus agent harness install` writes the rows to, and the one the spell's hook wiring
// names to `magus shell --tool-rows`. A tool no row names is one the guard cannot judge.
type HarnessTools struct {
	Dir  string        `json:"dir"`
	Rows []HarnessTool `json:"rows"`
}

// The files a tool rows directory holds beside one file per capability: the layout
// version, the harness's model rows, and the workspace's model-tier declaration.
const (
	ToolRowsVersionFile = "version"
	ToolModelRowsFile   = "models"
	ToolTiersFile       = "tiers"
)

// Holds reports whether rel, a slash-separated workspace-relative path, is a file install
// writes into the rows directory: every name an install can write, whether or not this
// table currently has rows for it.
func (t HarnessTools) Holds(rel string) bool {
	dir, name := path.Split(rel)
	if path.Clean(dir) != path.Clean(t.Dir) {
		return false
	}
	return name == ToolModelRowsFile || name == ToolTiersFile || name == ToolRowsVersionFile ||
		slices.Contains(toolCapabilities, ToolCapability(name))
}

// Capability is the capability the table gives tool, "" for a tool no row names.
func (t HarnessTools) Capability(tool string) ToolCapability {
	for _, r := range t.Rows {
		if r.Name == tool {
			return r.Capability
		}
	}
	return ""
}

// AgentFile is one whole file a harness renders in its host's format, such as a subagent.
// The descriptor owns every byte, so a plan rewrites it whenever the bytes differ. Hint is
// the person-owned setting that picks the agent's model, printed with the plan when the
// spell could not pick one itself.
type AgentFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hint    string `json:"hint,omitempty"`
}

// Prompt is one host-native approval setting a harness keeps in place, so a guard verdict
// of ask reaches the person through the host's own prompt. Exactly one body: Content is a
// whole file the descriptor owns, or Key and Value set one value inside a JSON document the
// person also edits.
type Prompt struct {
	Path    string   `json:"path"`
	Content string   `json:"content,omitempty"`
	Key     []string `json:"key,omitempty"`
	Value   any      `json:"value,omitempty"`
}

// Setting is one plain value a harness keeps in a host's JSON config, read by the host as
// configuration rather than run as a hook: a plugin to enable, a marketplace to know.
type Setting struct {
	Path  string   `json:"path"`
	Key   []string `json:"key"`
	Value any      `json:"value"`
}

// Limits are how long the host waits, in milliseconds: on a shell command in the
// foreground, ForegroundDefaultMs for one that names no timeout and ForegroundMaxMs at
// most, past which it moves the command to the background; and on one hook, HookTimeoutMs,
// past which it stops waiting for a verdict. Zero is a limit the host does not declare.
type Limits struct {
	ForegroundDefaultMs int64 `json:"foreground_default_ms,omitempty"`
	ForegroundMaxMs     int64 `json:"foreground_max_ms,omitempty"`
	HookTimeoutMs       int64 `json:"hook_timeout_ms,omitempty"`
}

// ShippedAgent is one subagent magus ships, as mgs_renderAgents receives it. Model is the
// host's own alias for the model the workspace maps to its cheapest tier, "" to leave the
// host's default.
type ShippedAgent struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	Model        string `json:"model,omitempty"`
	ReadOnly     bool   `json:"read_only"`
}
