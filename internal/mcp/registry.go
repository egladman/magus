package mcp

// ParamDescriptor describes a single parameter on an MCP tool.
type ParamDescriptor struct {
	Name        string `json:"name"                  yaml:"name"`
	Type        string `json:"type"                  yaml:"type"`
	Required    bool   `json:"required,omitempty"    yaml:"required,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// ToolDescriptor is a static description of one MCP tool, used both to
// register the tool with the server (mcp build tag) and to populate
// "magus describe mcp-tools" output (no build tag required).
type ToolDescriptor struct {
	Name        string            `json:"name"                  yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Params      []ParamDescriptor `json:"params,omitempty"      yaml:"params,omitempty"`
}

// Registry is the canonical list of MCP tools the magus daemon exposes.
// Order matches the registration order in tools.go.
var Registry = []ToolDescriptor{
	{
		Name:        "magus_describe",
		Description: "Describe a magus concept and list every entity of that kind in the workspace: spells (language/runtime adapters), targets (targets), projects, workspaces, or mcp_tools.",
		Params: []ParamDescriptor{
			{Name: "kind", Type: "string", Required: true, Description: "One of: spells, targets, projects, workspaces, mcp_tools."},
		},
	},
	{
		Name:        "magus_where",
		Description: "Resolve a fuzzy project name to its absolute directory path. Useful for navigating to a project or passing a path to another tool.",
		Params: []ParamDescriptor{
			{Name: "filter", Type: "string", Description: "One or more space-separated tokens to AND-filter project names (case-insensitive leaf match). Omit to list all."},
		},
	},
	{
		Name:        "magus_affected_explain",
		Description: "Explain why a project is in the VCS-diff affected set: shows the changed files and dependency chains that caused it to be selected.",
		Params: []ParamDescriptor{
			{Name: "project", Type: "string", Required: true, Description: "Project path (e.g. \"api\" or \"web/studio\")."},
			{Name: "base", Type: "string", Description: "Override the VCS base ref for the diff (default: MAGUS_VCS_BASE_REF or origin/main)."},
		},
	},
	{
		Name:        "magus_insight",
		Description: "Behavioral code analysis from VCS history: find where a codebase's attention and risk concentrate before diving in. Lenses (the `lens` param): hotspots (per-project churn × complexity, with authors/recency/blast-radius), files (per-file churn × complexity), affinity (projects that change together, flagging hidden undeclared coupling), ownership (author concentration, bus factor, abandonment), trend (rising vs cooling activity).",
		Params: []ParamDescriptor{
			{Name: "lens", Type: "string", Description: "One of: hotspots (default), files, affinity, ownership, trend."},
			{Name: "commits", Type: "number", Description: "Cap on how many recent commits to scan (default 500)."},
			{Name: "since", Type: "string", Description: "Only commits within this window, e.g. \"90d\", \"12w\", \"6mo\", \"1y\"."},
		},
	},
	{
		Name:        "magus_run_target",
		Description: "Run a build target for one or more projects. Target is a target like build, test, lint, format, generate, clean, ci, or a custom magusfile target. Without projects, the cwd project (or all) is selected.",
		Params: []ParamDescriptor{
			{Name: "target", Type: "string", Required: true, Description: "Target to run, e.g. \"build\", \"test\", \"lint\", \"format\", \"ci\", or an op-direct spell-qualified form like \"go::go-test\"."},
			{Name: "projects", Type: "string", Description: "Space-separated project paths. Use \"/\" for all. Omit for cwd-scoped selection."},
			{Name: "dry_run", Type: "boolean", Description: "Print what would run without executing (default false)."},
		},
	},
	{
		Name:        "magus_run_affected",
		Description: "Run a build target on only the projects affected by VCS changes. Equivalent to `magus affected <target>`.",
		Params: []ParamDescriptor{
			{Name: "target", Type: "string", Required: true, Description: "Target to run on affected projects (e.g. \"test\", \"lint\", \"ci\")."},
			{Name: "base", Type: "string", Description: "Override VCS base ref for the diff (default: MAGUS_VCS_BASE_REF or origin/main)."},
			{Name: "dry_run", Type: "boolean", Description: "Print what would run without executing."},
		},
	},
	{
		Name:        "magus_doctor",
		Description: "Validate the workspace: config schema, cache writability, project discovery, language coverage, dependency cycles, tool availability, and VCS reachability.",
	},
	{
		Name:        "magus_status",
		Description: "Report the workspace's configured telemetry, cache settings, and live proc-server pool state (when a parent magus is running).",
	},
	{
		Name:        "magus_affected_plan",
		Description: "Emit a provider-neutral JSON shard plan for the VCS-affected project set. Use for CI fan-out: map the matrix entries to your CI system's parallel job format.",
		Params: []ParamDescriptor{
			{Name: "max_shards", Type: "number", Description: "Maximum CI shards (default: from config; -1 means unlimited)."},
		},
	},
	{
		Name:        "magus_config_get",
		Description: "Return the resolved workspace configuration as JSON. Read-only — use the magus CLI to edit config.",
	},
	{
		Name:        "magus_tail_log",
		Description: "Return the captured build log of the most recent cache entry for a project. Useful after a failed magus_run_target to inspect tool output.",
		Params: []ParamDescriptor{
			{Name: "project", Type: "string", Required: true, Description: "Project path."},
		},
	},
}
