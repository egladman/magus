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
			{Name: "kind", Type: "string", Required: true, Description: "One of: spells, charms, targets, graph, projects, workspaces, modules, mcp_tools."},
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
		Description: "Behavioral code analysis: find where a codebase's attention and risk concentrate before diving in. VCS-history lenses (the `lens` param): hotspots (per-project churn × complexity, with authors/recency/blast-radius), files (per-file churn × complexity), affinity (projects that change together, flagging hidden undeclared coupling), ownership (author concentration, bus factor, abandonment), trend (rising vs cooling activity). One lens reads the knowledge graph instead of git: unreferenced (code symbols nothing in the workspace names). Read its answer.verdict before trusting an empty list - \"unknown\" means part of the workspace had no symbol index.",
		Params: []ParamDescriptor{
			{Name: "lens", Type: "string", Description: "One of: hotspots (default), files, affinity, ownership, trend, unreferenced."},
			{Name: "commits", Type: "number", Description: "Cap on how many recent commits to scan (default 500)."},
			{Name: "since", Type: "string", Description: "Only commits within this window, e.g. \"90d\", \"12w\", \"6mo\", \"1y\"."},
		},
	},
	{
		Name:        string(ToolRunTarget),
		Description: "Run a build target on an EXPLICIT set of projects (or the cwd project when omitted). Use this INSTEAD of raw language tools (go test, eslint, pytest, tsc): the raw tool bypasses the cache, the sandbox, and affected tracking. Target is a target like build, test, lint, format, generate, clean, ci, or a custom magusfile target. Use this when you know which projects to run; to instead run only the projects a VCS change touched, use magus_run_affected. The result reports the effective charms applied (workspace default_charms plus any charm suffix on the target).",
		Params: []ParamDescriptor{
			{Name: "target", Type: "string", Required: true, Description: "Target to run, e.g. \"build\", \"test\", \"lint\", \"format\", \"ci\", or an op-direct spell-qualified form like \"go::go-test\"."},
			{Name: "projects", Type: "string", Description: "Space-separated project paths. Use \"/\" for all. Omit for cwd-scoped selection."},
			{Name: "dry_run", Type: "boolean", Description: "Print what would run without executing (default false)."},
		},
	},
	{
		Name:        string(ToolRunAffected),
		Description: "Run a build target on ONLY the projects a VCS change touched - magus computes the affected set from the diff and its dependency graph; you do not name projects. Equivalent to `" + clihint.Affected.With("<target>") + "`. This is the CI/pre-commit tool; to instead run named projects explicitly, use magus_run_target. The result reports the effective charms applied (workspace default_charms plus any charm suffix on the target).",
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
		Description: "Emit a provider-neutral JSON shard plan for a target's VCS-affected project set. Use for CI fan-out or graph-guided work partitioning: map the matrix entries to parallel jobs, then inspect dependencies and outputs before assigning edits.",
		Params: []ParamDescriptor{
			{Name: "target", Type: "string", Description: "Target to plan (default: ci; any affected target is accepted)."},
			{Name: "base", Type: "string", Description: "Optional VCS base ref override."},
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
		Name:        string(ToolMemory),
		Description: "A user-owned per-repository handoff journal, shared across worktrees and kept outside the checkout. It is not automatic agent memory: create a named entry only for a decision, plan, or saved pointer a later person should reopen. Entries point to a query, graph node, output ref, command, or document; decision and plan entries may carry a short why. Use verify to surface malformed, stale, and broken-linked entries. The CLI (`magus memory`) and console read the same store. Legacy cursor reads remain for migration; writes are retired because one shared snapshot can erase another session's handoff.",
		Params: []ParamDescriptor{
			{Name: "op", Type: "string", Description: "One of: list (default; records plus issues), get, put (create or replace by name), delete, verify. cursor is legacy read-only."},
			{Name: "name", Type: "string", Description: "The record's kebab-slug identity. Required for get, put, delete."},
			{Name: "type", Type: "string", Description: "put only: one of pointer, decision, plan."},
			{Name: "refs", Type: "string", Description: "put only, REQUIRED: the payload, one ref per line as 'kind: target' (e.g. 'query: kind:op depends cache' or 'node: file:internal/hash/hasher.go'). Kinds: query, node, output, command, doc."},
			{Name: "body", Type: "string", Description: "put only: the one-line caption for a decision/plan (the why). Omit for a pointer - a pointer carries no prose."},
			{Name: "status", Type: "string", Description: "put only, optional: the lifecycle field (e.g. accepted, superseded, active, done, stale)."},
			{Name: "references", Type: "string", Description: "put only, optional: comma-separated names of other memory records this one links to."},
			{Name: "content", Type: "string", Description: "Legacy cursor reads only. Cursor writes are retired; use a named plan or decision with put."},
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
	{
		Name: string(ToolDiff),
		Description: "Join the review session a person already has open and pair with them on it. " +
			"op=state returns the whole session: every changed file annotated with its role (generated output vs source), " +
			"how widely its changed symbols are referenced, whether it is public API surface, observed coverage, " +
			"plus where the person is looking and what they have already read. " +
			"op=state also returns `patch` (the unified diff) and `hunks` (per file, each hunk's 0-based index and its content digest), " +
			"which are the coordinates comment and suggest take - so read state first and cite an index from it rather than guessing one. " +
			"It recomputes when the working tree has moved since the session was attached, and sets `recomputed` when it did. " +
			"op=comment attaches a remark to a hunk. op=suggest asks for their attention somewhere, with a reason. " +
			"op=resolve closes a comment. Both writing ops REFUSE a path that is not in the change and a hunk index that does not exist. " +
			"You CANNOT move their cursor or mark a hunk read: suggest, and they accept with one key. " +
			"Read state before commenting - `viewed` holds the same hunk digests, so you can skip what they have already seen.",
		Params: []ParamDescriptor{
			{Name: "op", Type: "string", Description: "One of: state (default), comment, suggest, resolve."},
			{Name: "projection", Type: "string", Description: "Shapes op=state's response only - comment, suggest, and resolve ignore it and always return the full session. One of: full (default; today's whole session), summary (id/base/as_of/recomputed/cursor plus counts of files, hunks, comments, suggestions, and viewed - no bodies), conversation (cursor, viewed, comments, suggestions, id/base/as_of - no diff, patch, or hunks), patch (id/base/as_of/recomputed plus patch and hunks - no diff, comments, or suggestions)."},
			{Name: "path", Type: "string", Description: "Workspace-relative file the comment or suggestion is about (comment, suggest)."},
			{Name: "hunk", Type: "number", Description: "0-based hunk index within the file, as reported by op=state's `hunks`; omit for the file as a whole. An index the file does not have is refused."},
			{Name: "body", Type: "string", Description: "The remark (comment)."},
			{Name: "reason", Type: "string", Description: "Why this is worth their attention - required, because a suggestion is an interruption (suggest)."},
			{Name: "id", Type: "string", Description: "Comment id (resolve)."},
			{Name: "agent_name", Type: "string", Description: "Optional label for which agent is speaking; attribution only."},
		},
	},
	{
		Name:        string(ToolVCSCheckpoint),
		Description: "Return the identity of the workspace's working state right now: head revision, branch, whether the tree is dirty, and a digest of the uncommitted patch. Record one when handing a piece of work out, so a later reader knows what that work was looking at. It resolves and records and never mints - no tag, no stash, no ref, no file, nothing changed anywhere - so calling it is free and a checkpoint nobody keeps costs nothing. Feed the revision to anything that takes a revision; compare two patch digests to learn whether two workers saw the same uncommitted tree, which the revision alone cannot say because everyone on the branch shares it. Takes no parameters.",
	},
	{
		Name:        string(ToolLedger),
		Description: "Record the orchestrating agent's declared delegation plan so humans can see it; magus never enforces it. One row per delegated unit, in the magus-delegate-multi-agent vocabulary: goal and acceptance criteria, the checkpoint the unit was handed, owned and forbidden paths, dependencies, tier, validation, and state. Owned/forbidden paths are a DECLARATION, not a boundary: nothing here blocks a write, gates a run, or derives a verdict - ownership is checked by comparing this ledger against the actual diff since each unit's checkpoint. Every row should end in pass, fail, or no_return; a read-only unit carries an abbreviated row with no paths. One plan per workspace: clear starts a fresh one and keeps no history. Re-put your row on every state change: each put re-stamps updated, and a row nobody touches goes stale, so an orchestrator reading that staleness will treat the unit as possibly dead - which is the READER's judgment, since nothing here transitions a row on its own.",
		Params: []ParamDescriptor{
			{Name: "op", Type: "string", Description: "One of: list (default; every row, plus overlaps - the pairs of live units whose owned_paths intersect, reported as unit_a/unit_b with each side's own declarations in paths_a/paths_b, derived on the read and stored nowhere, and a fact to look at rather than a verdict), put (create or replace one row by id), clear (drop every row to start a fresh plan)."},
			{Name: "id", Type: "string", Description: "put only, REQUIRED: the unit's id, which put upserts on. Use the same id you put in the worker's prompt."},
			{Name: "parent", Type: "string", Description: "put only: the id of the unit that delegated this one. Omit for a unit the root spawned."},
			{Name: "goal", Type: "string", Description: "put only: the unit's goal and its observable acceptance criteria (named tests, artifacts, diagnostics, review checks - not \"works correctly\")."},
			{Name: "checkpoint", Type: "string", Description: "put only: the working state this unit was handed, as `magus vcs checkpoint -o name` prints it (revision, plus a dirty-patch digest when the tree was not clean)."},
			{Name: "owned_paths", Type: "string", Description: "put only: space-separated paths the unit may edit. Empty on a read-only unit by design. Shrinking this set IS how a unit announces it has finished editing a path: the dropped paths are recorded on the row as releases, each with the sha256 of the file at that moment (absent when nothing is there, dir for a directory, unreadable when something is there that could not be hashed - which is deliberately not the same answer as absent), so the next agent knows WHICH version it inherits - a digest that no longer matches at verification time means it built on a tree the releaser never saw."},
			{Name: "forbidden_paths", Type: "string", Description: "put only: space-separated paths the unit must not touch. Empty on a read-only unit by design."},
			{Name: "depends_on", Type: "string", Description: "put only: space-separated ids of units that must land before this one."},
			{Name: "tier", Type: "string", Description: "put only: the effort tier the work was matched to, e.g. principal, standard, economy."},
			{Name: "validation", Type: "string", Description: "put only: the magus target or named check this unit was assigned."},
			{Name: "state", Type: "string", Description: "put only: declared, running, pass, fail, or no_return. no_return is not fail - it means the worker died, stalled, or was cancelled and returned no verdict at all."},
			{Name: "read_only", Type: "boolean", Description: "put only: mark a unit that gathers evidence and writes nothing, so empty owned/forbidden paths read as deliberate rather than forgotten."},
		},
	},
}
