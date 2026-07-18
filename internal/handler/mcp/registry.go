package mcp

import "github.com/egladman/magus/internal/interactive/clihint"

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
		Name:        string(ToolDescribe),
		Description: "Describe a magus concept and list every entity of that kind in the workspace: spells (language/runtime adapters), targets (targets), projects, workspaces, or mcp_tools. Pass name to narrow the list to one entity's detail: for targets it returns the fully-evaluated dispatch plan (sources, outputs, spells, rendered command, charms, policy).",
		Params: []ParamDescriptor{
			{Name: "kind", Type: "string", Required: true, Description: "One of: spells, targets, projects, workspaces, mcp_tools."},
			{Name: "name", Type: "string", Description: "Optional. Narrow the list to one entity's detail (kinds spells, targets, projects). For targets, a target name optionally followed by a project path (e.g. \"build\", \"lint:rw\", or \"build api\"); omit the project to evaluate every project. For spells, a spell name; for projects, a project path. Unknown name returns the valid values."},
		},
	},
	{
		Name:        string(ToolDescribeFile),
		Description: "Classify paths against the workspace's declared globs: the owning project, whether each path is a declared output (generated: regenerate it, never hand-edit) or a declared source (feeds cache keys and the affected set), and which projects claim it. Answers \"can I disregard this changed file\" from the workspace's own declarations - run it over a whole dirty tree before reading diffs or committing.",
		Params: []ParamDescriptor{
			{Name: "paths", Type: "string", Required: true, Description: "One or more workspace-relative paths, space-separated (e.g. \"MAGUS.md web/gen/index.html cmd/api/main.go\")."},
		},
	},
	{
		Name:        string(ToolWhere),
		Description: "Resolve a fuzzy project name to its absolute directory path. Useful for navigating to a project or passing a path to another tool.",
		Params: []ParamDescriptor{
			{Name: "filter", Type: "string", Description: "One or more space-separated tokens to AND-filter project names (case-insensitive leaf match). Omit to list all."},
		},
	},
	{
		Name:        string(ToolAffectedExplain),
		Description: "Explain why a project is in the VCS-diff affected set: shows the changed files and dependency chains that caused it to be selected.",
		Params: []ParamDescriptor{
			{Name: "project", Type: "string", Required: true, Description: "Project path (e.g. \"api\" or \"web/studio\")."},
			{Name: "base", Type: "string", Description: "Override the VCS base ref for the diff (default: MAGUS_VCS_BASE_REF or origin/main)."},
		},
	},
	{
		Name:        string(ToolInsight),
		Description: "Behavioral code analysis from VCS history: find where a codebase's attention and risk concentrate before diving in. Lenses (the `lens` param): hotspots (per-project churn × complexity, with authors/recency/blast-radius), files (per-file churn × complexity), affinity (projects that change together, flagging hidden undeclared coupling), ownership (author concentration, bus factor, abandonment), trend (rising vs cooling activity).",
		Params: []ParamDescriptor{
			{Name: "lens", Type: "string", Description: "One of: hotspots (default), files, affinity, ownership, trend."},
			{Name: "commits", Type: "number", Description: "Cap on how many recent commits to scan (default 500)."},
			{Name: "since", Type: "string", Description: "Only commits within this window, e.g. \"90d\", \"12w\", \"6mo\", \"1y\"."},
		},
	},
	{
		Name:        string(ToolRunTarget),
		Description: "Run a build target for one or more projects. Target is a target like build, test, lint, format, generate, clean, ci, or a custom magusfile target. Without projects, the cwd project (or all) is selected. The result reports the effective charms applied (workspace default_charms plus any charm suffix on the target).",
		Params: []ParamDescriptor{
			{Name: "target", Type: "string", Required: true, Description: "Target to run, e.g. \"build\", \"test\", \"lint\", \"format\", \"ci\", or an op-direct spell-qualified form like \"go::go-test\"."},
			{Name: "projects", Type: "string", Description: "Space-separated project paths. Use \"/\" for all. Omit for cwd-scoped selection."},
			{Name: "dry_run", Type: "boolean", Description: "Print what would run without executing (default false)."},
		},
	},
	{
		Name:        string(ToolRunAffected),
		Description: "Run a build target on only the projects affected by VCS changes. Equivalent to `" + clihint.Affected.With("<target>") + "`. The result reports the effective charms applied (workspace default_charms plus any charm suffix on the target).",
		Params: []ParamDescriptor{
			{Name: "target", Type: "string", Required: true, Description: "Target to run on affected projects (e.g. \"test\", \"lint\", \"ci\")."},
			{Name: "base", Type: "string", Description: "Override VCS base ref for the diff (default: MAGUS_VCS_BASE_REF or origin/main)."},
			{Name: "dry_run", Type: "boolean", Description: "Print what would run without executing."},
		},
	},
	{
		Name:        string(ToolDoctor),
		Description: "Validate the workspace: config schema, cache writability, project discovery, language coverage, dependency cycles, tool availability, and VCS reachability.",
	},
	{
		Name:        string(ToolStatus),
		Description: "Report the workspace's configured telemetry, cache settings, and live proc-server pool state (when a parent magus is running).",
	},
	{
		Name:        string(ToolAffectedPlan),
		Description: "Emit a provider-neutral JSON shard plan for the VCS-affected project set. Use for CI fan-out: map the matrix entries to your CI system's parallel job format.",
		Params: []ParamDescriptor{
			{Name: "max_shards", Type: "number", Description: "Maximum CI shards (default: from config; -1 means unlimited)."},
		},
	},
	{
		Name:        string(ToolConfigGet),
		Description: "Return the resolved workspace configuration as JSON. Read-only — use the magus CLI to edit config.",
	},
	{
		Name:        string(ToolTailLog),
		Description: "Return the captured build log of the most recent cache entry for a project. Useful after a failed magus_run_target to inspect tool output.",
		Params: []ParamDescriptor{
			{Name: "project", Type: "string", Required: true, Description: "Project path."},
		},
	},
	{
		Name:        string(ToolScratchpad),
		Description: "A private, per-workspace scratch file for the agent to jot intermediate work into and read back later, instead of dumping it all into the conversation. Use it to park a plan, a running checklist, partial findings, or notes across several tool calls, then read them back on demand. It is NOT shown to the user unless they open the file themselves. One file per workspace; write and append overwrite/extend the same file.",
		Params: []ParamDescriptor{
			{Name: "op", Type: "string", Description: "One of: read (default; returns current contents, empty if never written), write (overwrite with content), append (add content on a new line), clear (empty the scratchpad)."},
			{Name: "content", Type: "string", Description: "The text to write or append. Required for write and append; ignored for read and clear."},
		},
	},
	{
		Name:        string(ToolMemory),
		Description: "Durable per-repository memory shared across sessions, models, and agent hosts: three plain-markdown files kept OUTSIDE the repo in the user state directory (worktrees of one repo share them). Files: status (the current snapshot - where work stands, next action, blockers; overwrite with op=write), progress (dated work journal; op=append), decisions (dated log of decisions made and WHY; op=append). Read status and decisions at the start of a session to ramp on what earlier sessions - possibly a different model - established; append as you work. Appends to progress/decisions are date-stamped automatically. Reads are WINDOWED to keep session-start cheap: status returns in full, but a read of progress or decisions returns a table of contents of all entry headings plus the last 5 entries in full; pass op=read_all to get the entire journal when you need older entries. For intra-session scratch notes use magus_scratchpad instead.",
		Params: []ParamDescriptor{
			{Name: "file", Type: "string", Required: true, Description: "One of: status, progress, decisions."},
			{Name: "op", Type: "string", Description: "One of: read (default; windows progress/decisions to a table of contents plus the last 5 entries), read_all (the full journal), write (overwrite), append, clear."},
			{Name: "content", Type: "string", Description: "The text to write or append. Required for write and append."},
			{Name: "title", Type: "string", Description: "Optional, append only: a few-word summary folded into the entry's dated heading, so scanning the headings reads as a table of contents. Title every decisions entry."},
		},
	},
	{
		Name:        string(ToolQuery),
		Description: "Search the knowledge graph and return ranked node matches plus their surrounding neighborhood (the induced subgraph). Prefer this over grep to find and relate magus-domain entities: projects, targets, spells, ops, charms, modules, diagnostics. Ingested code symbols are lazily loaded: to match them, scope the query with kind:symbol (or use magus_refs) - a bare free-text query stays in the domain graph. For a large match set, pass limit to page the matches and echo the returned next_cursor to fetch the following page. To fetch a target execution's captured output by its reference id (out1a2b3c), use magus_output.",
		Params: []ParamDescriptor{
			{Name: "query", Type: "string", Required: true, Description: "Search terms: free text plus field filters like kind:spell, project:pkg/foo, relation:uses, id:build, and negation -kind:op."},
			{Name: "budget", Type: "number", Description: "Max nodes in the returned neighborhood (default 50)."},
			{Name: "limit", Type: "number", Description: "Page size: max matches to return. Omit or 0 for all matches (no paging)."},
			{Name: "cursor", Type: "string", Description: "Opaque cursor from a prior response's next_cursor, to fetch the next page. Only valid for the same query and an unchanged graph."},
		},
	},
	{
		Name:        string(ToolOutput),
		Description: "Return one target execution's exact captured output by its reference id (out1a2b3c, shown on each target's line in a run), plus the run's descriptor (project, target, pass/fail, duration). Fetch a failing target's full log by ref instead of re-reading a wall of text or asking the user to paste it. Accepts a unique ref prefix, like a git short hash.",
		Params: []ParamDescriptor{
			{Name: "ref", Type: "string", Required: true, Description: "A target-output reference id (out1a2b3c) or a unique prefix of one, as printed on each target's result line."},
		},
	},
	{
		Name:        string(ToolExplain),
		Description: "Show one knowledge-graph node's context: its data, its incoming and outgoing edges with provenance, and how many nodes reach it. The argument is a node ID (target:pkg/foo:build) or a name that resolves to one.",
		Params: []ParamDescriptor{
			{Name: "node", Type: "string", Required: true, Description: "A node ID or a name that resolves to one."},
		},
	},
	{
		Name:        string(ToolRefs),
		Description: "List where an ingested code symbol is defined and every file that references it, as file:line rows drawn from a SCIP index. The occurrence-shaped answer for a symbol's fan-in (magus_query renders that poorly). Symbols come from a declared knowledge.symbols index. For a large fan-in, pass limit and echo the returned next_cursor.",
		Params: []ParamDescriptor{
			{Name: "symbol", Type: "string", Required: true, Description: "A symbol node ID (symbol:...) or a name that resolves to one."},
			{Name: "limit", Type: "number", Description: "Page size: max referencing files to return. Omit or 0 for all."},
			{Name: "cursor", Type: "string", Description: "Opaque cursor from a prior response's next_cursor. Only valid for the same symbol and an unchanged graph."},
		},
	},
	{
		Name:        string(ToolPath),
		Description: "Show the shortest path between two knowledge-graph nodes: the chain of edges connecting them, with each hop's relation. Answers how two entities relate.",
		Params: []ParamDescriptor{
			{Name: "from", Type: "string", Required: true, Description: "Start node ID or a name that resolves to one."},
			{Name: "to", Type: "string", Required: true, Description: "End node ID or a name that resolves to one."},
		},
	},
	{
		Name:        string(ToolStats),
		Description: "Report the knowledge graph's shape - where the workspace concentrates and neglects. Returns god nodes (the most connected spells, targets, modules, where structural risk concentrates), orphans (docs that document nothing, spells no target uses), and doc coverage. Answers \"where is risk concentrated\" without shelling out.",
		Params: []ParamDescriptor{
			{Name: "kind", Type: "string", Description: "Scope every section to one node kind (e.g. spell, target, doc, diagnostic). Omit for the whole graph."},
		},
	},
}
