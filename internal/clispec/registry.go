package clispec

import "time"

// All is the ordered list of magus top-level commands consumed by the
// man-page generator (cmd/magus-manpage).
var All = []Command{
	listCommand,
	describeCommand,
	runCommand,
	xCommand,
	whereCommand,
	affectedCommand,
	graphCommand,
	queryCommand,
	explainCommand,
	pathCommand,
	refsCommand,
	watchCommand,
	statusCommand,
	cleanCommand,
	vcsCommand,
	doctorCommand,
	configCommand,
	memoryCommand,
	notesCommand,
	diffCommand,
	serverCommand,
	buzzCommand,
	completionCommand,
	manCommand,
	initCommand,
	agentCommand,
	hookCommand,
	notifyCommand,
	selfCommand,
	versionCommand,
}

// CommonTargets is the canonical set of project-scoped targets shared by
// the "run" and "affected" commands.
var CommonTargets = []Target{
	{Name: "ls", Short: "Print selected projects without executing anything"},
	{Name: "build", Short: "Build selected projects"},
	{Name: "test", Short: "Test selected projects"},
	{Name: "lint", Short: "Lint selected projects (read-only)"},
	{Name: "format", Short: "Format source files in selected projects"},
	{Name: "clean", Short: "Remove build artifacts from selected projects"},
	{Name: "generate", Short: "Run code generation for selected projects"},
	{Name: "ci", Short: "Run the magusfile's ci target read-only (affected-set anchor)"},
}

var listCommand = Command{
	Name:        "ls",
	Short:       "List all discovered projects",
	Description: "List every discovered project in the workspace with its language pack, source files, outputs, dependencies, and tool requirements.",
	Tags:        []string{"cli", "magus ls", "list", "projects", "discovery", "workspace"},
	Long: `Print every discovered project in the workspace along with its language
pack, source files, outputs, dependencies, and tool requirements.

Output defaults to a human-readable text format. Use the global -o flag with
json or yaml for structured output suitable for scripting. -o name prints one
project path per line. -o template accepts a Go text/template evaluated
against the value -o json emits, so its field names are the json keys.`,
	Usage: "magus ls [flags]",
	Examples: []Example{
		{"List all projects", "magus ls"},
		{"Pipe-friendly: one path per line", "magus ls -o name"},
		{"JSON output", "magus ls -o json"},
		{"Custom Go template", `magus ls -o template='{{range .projects}}{{.path}}{{"\n"}}{{end}}'`},
	},
}

var describeCommand = Command{
	Name:        "describe",
	Short:       "Define a magus concept and list its entities",
	Description: "Define a magus concept (spell, charm, target, project, workspace, module, mcp-tool) and list every entity of that kind, or detail one when a name is given.",
	Tags:        []string{"cli", "magus describe", "spell", "charm", "target", "project", "workspace", "introspection"},
	Long: `Define a magus concept and list every entity of that kind. The noun is
one of spell, charm, target, project, workspace, module, or mcp-tool; singular
and plural are interchangeable. Pass a name after the noun to detail a single
entity instead of listing them all. (The knowledge graph lives under magus
graph: export for the merged graph, stats for its shape.)

The charm noun is the inverse of a target ref: "describe charm rw" lists every
target that declares the rw charm and the argv edit each one makes, the transpose
of the charms a single "describe target" lists.

For a target ref (e.g. "api:build", or ":test" for all projects) magus prints the
fully-evaluated dispatch plan: the workspace-rooted source and output globs, the
spells that fire, the charm-applied command, and any per-target policy. Add a charm
and --explain (e.g. "lint:rw --explain") to see each charm reshape the command one
step at a time.`,
	Usage: "magus describe <noun> [<name>] [flags]",
	// The nouns are separate commands, each with its own flags. They were one
	// merged list on the parent, which is why -e had to be documented as "short
	// for --explain on a target ref, and for --evaluated on a project listing":
	// two different flags sharing a spelling because there was one namespace for
	// all the nouns. Split, each -e is an ordinary alias of the flag beside it.
	Children: []Command{
		{Name: "targets", Short: "List every target the workspace defines"},
		{
			Name:  "target",
			Short: "Detail one target ref: its dispatch plan, globs, spells and policy",
			Flags: []Flag{
				{Name: "explain", Kind: FlagBool, Doc: "For a ref with charms: show the per-charm argv trace (base then each charm)"},
				{Name: "e", Kind: FlagBool, AliasOf: "explain", Doc: "Short for --explain"},
				{Name: "cache", Kind: FlagBool, Doc: "Show the live cache key, the ref a run would print, and the component classes behind it"},
				{Name: "against", Kind: FlagString, Doc: "With --cache: diff the live key inputs against the stored lines behind an output `ref`"},
				{Name: "no-default-charms", Kind: FlagBool, Doc: "With --cache: ignore magus.yaml default_charms when keying, matching a run made the same way (CI)"},
			},
		},
		{
			Name:  "projects",
			Short: "List the workspace's projects",
			Flags: []Flag{
				{Name: "evaluated", Kind: FlagBool, Doc: "Print workspace-rooted globs, effective claims, and per-target policies"},
				{Name: "e", Kind: FlagBool, AliasOf: "evaluated", Doc: "Short for --evaluated"},
			},
		},
		{
			Name:  "spells",
			Short: "List the spells the workspace resolves",
			Flags: []Flag{
				// Bound by the command and declared nowhere until now, so no man page
				// listed it.
				{Name: "versions", Kind: FlagBool, Doc: "Probe each spell's tools and report the versions that would key a run"},
			},
		},
		{Name: "charms", Short: "List charms and the targets that declare them"},
		{Name: "workspaces", Short: "List the workspaces registered in config"},
		{Name: "modules", Short: "List the Buzz host modules a magusfile can import"},
		{Name: "mcp-tools", Short: "List the tools the MCP server exposes"},
		{Name: "tools", Short: "List the external tools the workspace's spells require"},
		{Name: "file", Short: "Classify paths as generated output, declared source, maintained, or unclaimed"},
		{Name: "graph", Short: "Emit the target catalog and dependency graph"},
	},
	Examples: []Example{
		{"List every target", "magus describe targets"},
		{"List a charm's declaring targets", "magus describe charm rw"},
		{"Detail one project", "magus describe project api"},
		{"Preview a charm-applied command", "magus describe target lint:rw"},
		{"Trace how each charm reshapes the command", "magus describe target --explain lint:rw,debug"},
	},
}

var runCommand = Command{
	Name:        "run",
	Short:       "Run a target for selected projects",
	Description: "Run a named target (build, test, lint, format, ci, etc.) for the selected projects, defaulting to the cwd project when no arguments are given.",
	Tags:        []string{"cli", "magus run", "run", "target", "build", "test", "ci"},
	Long: `Run a named target for the selected projects. With no project
arguments, selects the project containing the current directory, or all projects
if the current directory is not inside a project. Explicit project paths on the
command line select exactly those projects.

The target ci is an ordinary magusfile-defined target - magus does not hardcode
its steps; your magusfile composes them with magus.needs. magus keeps ci as
the anchor that the affected set keys off, and always runs it read-only; apply
the rw charm (e.g. 'magus run format:rw') to mutate files.`,
	Usage: "magus run <target> [flags] [project...]",
	Flags: []Flag{
		{Name: "graph", Kind: FlagBool, Doc: "Render the dependency graph for the selected scope instead of executing"},
		{Name: "upstream", Kind: FlagBool, Doc: "With --graph: show dependents instead of dependencies"},
		{Name: "depth", Kind: FlagInt, Doc: "With --graph: cap displayed depth (0 = unlimited)"},
		{Name: "no-cache", Kind: FlagBool, Doc: "Force a fresh run even on a cache hit; still refreshes the entry"},
		{Name: "no-default-charms", Kind: FlagBool, Doc: "Ignore magus.yaml default_charms for this run"},
		{Name: "detach", Kind: FlagBool, Doc: "Hand the run to the daemon and return immediately; follow it with magus status --watch"},
		{Name: "wait", Kind: FlagBool, Doc: "With --detach, block until the run finishes and exit with its status"},
		{Name: "open", Kind: FlagBool, Doc: "Open this run in the browser log viewer and stream to it as it goes (loopback; never leaves your machine)"},
		{Name: "step", Kind: FlagBool, Doc: "Pause before each subprocess for interactive stepping (needs a TTY; implies --concurrency=1)"},
		{Name: "race", Kind: FlagString, Doc: "Race-condition diagnostics (watch|replay, comma-combinable); omit to disable. watch: attribution-gated fsnotify detection (MGS4001/4002/4004), emitting only when >=2 projects' output snapshots confirm a shared write. replay: re-runs cacheable output-declaring projects sequentially to content-hash outputs for non-determinism (MGS4003); roughly doubles wall-clock."},
		{Name: "timeout", Kind: FlagDuration, Doc: "Abort if the run has not finished within this duration (e.g. 5m, 1h30m)"},
		// A STRING, not an int: the shard id is passed through as a label (it reaches
		// RecordShardTotal as text and defaults from MAGUS_SHARD), and the man page
		// documented it as an int for as long as the two lists were written apart.
		// The drift test compared names only, so a type could disagree indefinitely.
		{Name: "shard", Kind: FlagString, Doc: "This run's shard index within a CI matrix; paired with --n-shards"},
		{Name: "n-shards", Kind: FlagInt, Doc: "Total shard count for this CI matrix run; paired with --shard"},
		{Name: "no-volatility-retry", Kind: FlagBool, Doc: "Disable volatility auto-retry for this run"},
	},
	Targets: CommonTargets,
	Examples: []Example{
		{"Build everything", "magus run build"},
		{"Test one project", "magus run test api/gateway"},
		{"Build two specific projects", "magus run build api/gateway web/studio"},
		{"Dry-run: show what would run", "magus run build --dry-run"},
		{"Force a fresh rebuild past a cache hit", "magus run build --no-cache"},
		{"Full CI pipeline", "magus run ci"},
		{"Show dependency graph for build target", "magus run build --graph"},
		{"Graph in Mermaid format", "magus run build --graph -o mermaid"},
		{"Graph dependents of api/gateway", "magus run build api/gateway --graph --upstream"},
		{"Stream JSONL target events to a file", "magus run build -o jsonl --tee build.jsonl"},
	},
}

var whereCommand = Command{
	Name:        "where",
	Short:       "Print the absolute path of a project",
	Description: `Fuzzy-match a project by leaf-anchored substring and print its absolute path, designed for shell substitution like cd "$(magus where api)".`,
	Tags:        []string{"cli", "magus where", "where", "project", "path", "fuzzy match", "navigation"},
	Long: `Fuzzy-match a project by leaf-anchored substring and print its
absolute path to stdout. Designed for shell substitution:

  cd "$(magus where api)"
  code "$(magus where dash)"

Filters are AND-combined substrings. On a unique top score the path is
printed and the command exits 0. On ambiguity, candidates are listed on
stderr and the command exits 2. No interactive picker - use magus x for
that.`,
	Usage: "magus where [filter...]",
	Examples: []Example{
		{"Navigate to a project", `cd "$(magus where api)"`},
		{"Open in editor", `code "$(magus where dash)"`},
		{"AND-filter: must match both tokens", "magus where api gateway"},
	},
}

var xCommand = Command{
	Name:        "x",
	Short:       "Reproduce an output ref, or pick project + target",
	Description: "Reproduces the invocation an output ref recorded, or opens a TTY picker for project and target when given filters instead.",
	Tags:        []string{"cli", "magus x", "interactive", "picker", "shorthand", "run", "tty", "reproduce", "output ref"},
	Long: `Two forms, chosen by the argument.

Given an output ref (out1a2b3c), x reproduces the invocation that minted it:
the descriptor records the project, the target and its charms, so nothing is
picked and no terminal is required. A ref copied from a CI log therefore runs
here as the same invocation - replaying from cache when the inputs match, and
running the target when they do not. Before starting it compares the recorded
cache key with the one this workspace computes and says which of the two will
happen, and it reports a dirty working tree or a differing revision rather than
implying an exactness it cannot deliver. It changes no VCS state: a differing
commit is named, never checked out.

Given filters (or nothing), x is the interactive shorthand for magus run.
Filters are AND-combined
substrings matched against project paths; ranking is leaf-anchored
longest-match-wins, so "magus x dash" prefers a project named "dashboard"
over one named "dashboards-deprecated/foo". Additional filter args narrow
the candidate set: "magus x dash mobile" requires both substrings.

When the filtered set is unique, the project picker is skipped. Otherwise
a TTY picker opens, seeded with the survivors, sorted by score. After a
project is chosen, a second picker offers the target set
(build/test/lint/format/clean/generate/ci); the last target used for that
project (persisted in $XDG_STATE_HOME/magus/x-state.json, defaulting to
$HOME/.local/state/magus/) is pre-highlighted.

x refuses to run when stdin or stderr is not a terminal: shorthand is for
humans. Scripts should call magus run directly.`,
	Usage: "magus x <ref> | magus x [filter...]",
	Examples: []Example{
		{"Reproduce the invocation an output ref recorded", "magus x out1a2b3c"},
		{"Browse all projects in a picker", "magus x"},
		{"Resolve by leaf substring", "magus x dash"},
		{"AND-narrow with a second filter", "magus x dash mobile"},
	},
}

var affectedCommand = Command{
	Name:        "affected",
	Short:       "Run a target for VCS-diff affected projects",
	Description: "Run a target for every project affected by a VCS diff, with forensic modes for explain, graph, CI shard plan, and regression bisect.",
	Tags:        []string{"cli", "magus affected", "affected", "changed files", "vcs", "git", "bisect", "ci"},
	Long: `Run a named target for every project that is affected by changes in
version control. The active VCS adapter is picked by autodetect from .git, .hg,
or .jj at the workspace root, or pinned with MAGUS_VCS_COMMAND_NAME /
vcs.command_name. MAGUS_VCS_COMMAND overrides the command entirely. When
MAGUS_VCS_ENABLED=false (or vcs.enabled: false) affected detection
short-circuits and falls back to the full project set with the source label
"vcs disabled".

A project is affected if any of its source files changed directly, or if a
project it depends on is affected (transitive closure over the dependency graph).

Use --stdin to read changed paths from a pipe instead of running a VCS diff.
This pairs with magus watch for continuous-build workflows:

  magus watch | magus affected --stdin build

Forensic modes reason about the affected set instead of executing a target.
--explain shows why a project is in the set. --plan emits a provider-neutral
JSON shard plan for the named target. Combine --plan with --stdin for a one-shot
plan of proposed paths before editing. --bisect drives VCS bisect using run
history to find the commit that introduced a regression.`,
	Usage: "magus affected <target> [flags]",
	Flags: []Flag{
		{Name: "impact", Kind: FlagBool, Modes: []string{"impact"}, Doc: "Report the blast radius of the changeset (read-only; runs nothing)"},
		{Name: "base", Kind: FlagString, Modes: []string{"", "plan", "impact"}, Doc: "Override base ref for the VCS diff (default: MAGUS_VCS_BASE_REF or per-VCS built-in)"},
		{Name: "stdin", Kind: FlagBool, Modes: []string{"", "plan"}, Doc: "Read changed file paths from stdin instead of running a VCS diff"},
		{Name: "null", Kind: FlagBool, Modes: []string{"", "plan"}, Doc: "With --stdin: expect NUL-separated paths and double-NUL between batches"},
		{Name: "b", Kind: FlagString, AliasOf: "base", Modes: []string{"", "plan", "impact"}, Doc: "Short for --base"},
		{Name: "no-cache", Kind: FlagBool, Doc: "Force a fresh run even on a cache hit; still refreshes the entry"},
		{Name: "no-default-charms", Kind: FlagBool, Doc: "Ignore magus.yaml default_charms for this run"},
		{Name: "detach", Kind: FlagBool, Doc: "Hand the run to the daemon and return immediately; follow it with magus status --watch"},
		{Name: "wait", Kind: FlagBool, Doc: "With --detach, block until the run finishes and exit with its status"},
		{Name: "open", Kind: FlagBool, Doc: "Open this run in the browser log viewer and stream to it as it goes (loopback; never leaves your machine)"},
		{Name: "step", Kind: FlagBool, Doc: "Pause before each subprocess for interactive stepping (needs a TTY; implies --concurrency=1)"},
		{Name: "race", Kind: FlagString, Doc: "Race-condition diagnostics (watch|replay, comma-combinable); omit to disable. watch: attribution-gated fsnotify detection (MGS4001/4002/4004), emitting only when >=2 projects' output snapshots confirm a shared write. replay: re-runs cacheable output-declaring projects sequentially to content-hash outputs for non-determinism (MGS4003); roughly doubles wall-clock."},
		{Name: "timeout", Kind: FlagDuration, Doc: "Abort if the run has not finished within this duration (e.g. 5m, 1h30m)"},
		{Name: "graph", Kind: FlagBool, Doc: "Render the dependency graph for the affected scope instead of executing"},
		{Name: "upstream", Kind: FlagBool, Doc: "With --graph: show dependents instead of dependencies"},
		{Name: "depth", Kind: FlagInt, Doc: "With --graph: cap displayed depth (0 = unlimited)"},
		{Name: "explain", Kind: FlagCustom, Doc: "Show why <project> is in the affected set instead of executing"},
		{Name: "plan", Kind: FlagCustom, Doc: "Emit a provider-neutral JSON CI shard plan for the affected set"},
		{Name: "max-shards", Kind: FlagInt, Default: 8, DefaultAtBind: true, Modes: []string{"plan"}, Doc: "With --plan: maximum CI shards (-1 = unlimited)"},
		{Name: "max-parallel-budget", Kind: FlagInt, DefaultAtBind: true, Modes: []string{"plan"}, Doc: "With --plan: cross-shard concurrency cap; 0 = unlimited"},
		{Name: "detail", Kind: FlagBool, Modes: []string{"plan"}, Doc: "With --plan: add per-shard detail - the invocation, its spells, the files it declares it writes, and the skills its work routes to"},
		{Name: "bisect", Kind: FlagString, Modes: []string{"bisect"}, Doc: "Drive VCS bisect to find the commit that broke <project>"},
		{Name: "good", Kind: FlagString, Modes: []string{"bisect"}, Doc: "With --bisect: known-good commit SHA (auto-detected from history when empty)"},
		{Name: "target", Kind: FlagString, Default: "test", Modes: []string{"bisect"}, Doc: "With --bisect: magus target to bisect"},
	},
	Targets: CommonTargets,
	Examples: []Example{
		{"Build projects changed since the default base ref", "magus affected build"},
		{"Use a different base ref", "magus affected build --base main"},
		{"Pipe from watch for continuous builds", "magus watch | magus affected --stdin build"},
		{"List affected projects without building", "magus affected list"},
		{"Show dependency graph for the affected scope", "magus affected build --graph"},
		{"Graph as DOT for piping to Graphviz", "magus affected build --graph -o dot | dot -Tsvg > graph.svg"},
		{"Emit a CI shard plan for the affected set", "magus affected ci --plan"},
		{"Shard a test plan across at most four workers", "magus affected test --plan --max-shards 4"},
		{"Bisect a regression in myapp", "magus affected --bisect ./apps/myapp"},
	},
}

var queryCommand = Command{
	Name:        "query",
	Short:       "Search the knowledge graph, and retrieve a run's output or journal by id",
	Description: "Resolve search terms to knowledge-graph nodes and return the ranked matches plus their neighborhood; also retrieves a target's captured output by ref and a run's journal by invocation id.",
	Tags:        []string{"cli", "magus query", "query", "knowledge graph", "search", "output", "reference", "invocation", "journal", "audit"},
	Long: `Ask the workspace what it knows. query resolves terms to nodes in the
knowledge graph and returns the ranked matches plus the induced subgraph around
them, collected up to a node budget. Its siblings explain and path read the same
graph: explain shows one node's context, path connects two nodes.

Terms are free text plus field filters, and they compose: kind:spell, project:pkg/foo,
relation:uses, id:build, and negation with a leading dash (-kind:op). A bare word
matches names and documentation.

query is also the retrieval verb for the two ids magus prints, each an EXPLICIT
subcommand rather than a shape-routed positional, so a search term can never collide
with an id:

  output <ref>       One target execution's captured output, by the reference id
                     shown when the target ran (out1a2b3c). The default prints the
                     exact bytes, so it pipes anywhere. --meta shows the run's
                     identity instead - descriptor, lineage, cache key, and the
                     digests of the key's component classes, which is the
                     machine-comparable half of a works-on-my-machine report.
                     --attempts lists the ref's stored executions, --publish uploads
                     the output to the remote cache as a signed bundle, and --open
                     hands the bytes to the browser log viewer in a URL fragment
                     (delivered privately; never uploaded).
  invocation <id>    One run's journal, by the invocation id shown as inv: in
                     query output <ref> --meta. --secrets narrows it to the
                     credential reads - which references the run reached for and
                     through which provider, never the value - which is how an audit
                     answers "what did this run touch". Run logs are trimmed to a cap
                     by the daemon's RotateLogs job, so this answers for recent runs
                     rather than forever.

The graph is cache-backed under <cache>/knowledge and only shards whose sources
changed are rebuilt, so a query is cheap to repeat; --refresh forces a full rebuild.`,
	Usage: "magus query <terms> [flags]",
	Flags: []Flag{
		{Name: "budget", Kind: FlagInt, Doc: "Max nodes in the returned neighborhood (default 50)"},
		{Name: "kind", Kind: FlagString, Doc: "Restrict matches to these node kinds (comma-separated)"},
		{Name: "refresh", Kind: FlagBool, Doc: "Force a full graph rebuild before querying"},
		{Name: "global", Kind: FlagBool, Doc: "Query across the workspaces registered in config (knowledge.workspaces); IDs are namespaced by workspace"},
		{Name: "meta", Kind: FlagBool, Doc: "output <ref>: show the run's identity - descriptor, lineage, cache key, component digests"},
		{Name: "attempts", Kind: FlagBool, Doc: "output <ref>: list the ref's stored executions (newest first)"},
		{Name: "publish", Kind: FlagBool, Doc: "output <ref>: upload this run's output to the remote cache as a signed bundle"},
		{Name: "open", Kind: FlagBool, Doc: "output <ref>: open the captured output in the browser log viewer (delivered privately)"},
		{Name: "print", Kind: FlagBool, Doc: "With --open, print the viewer URL instead of launching a browser"},
		{Name: "url", Kind: FlagString, Default: "https://eli.gladman.cc/magus/console/logs/", DefaultAtBind: true, Doc: "With --open, base URL of the log viewer page (override for a self-hosted mirror)"},
		{Name: "secrets", Kind: FlagBool, Doc: "invocation <id>: list only the credential reads (reference and provider, never the value)"},
	},
	Children: []Command{
		{Name: "output", Short: "Retrieve one target execution's captured output by reference id"},
		{Name: "invocation", Short: "Read one run's journal by invocation id (--secrets for the credential reads)"},
	},
	Examples: []Example{
		{"Find a spell by name", "magus query kind:spell go"},
		{"What uses this target", "magus query relation:uses id:build"},
		{"Everything but ops", "magus query docker -kind:op"},
		{"Print a run's captured output", "magus query output out1a2b3c"},
		{"Compare a run's cache key", "magus query output out1a2b3c --meta"},
		{"Audit a run's credential reads", "magus query invocation invmsm3vcou1 --secrets"},
	},
}

var explainCommand = Command{
	Name:        "explain",
	Short:       "Show one knowledge-graph node's context: data, edges, and reach",
	Description: "Show a single knowledge-graph node in context: its data, its incoming and outgoing edges with provenance, and how many nodes reach it.",
	Tags:        []string{"cli", "magus explain", "explain", "knowledge graph", "node", "edges", "provenance", "impact"},
	Long: `explain shows one node's context: its data, its incoming and outgoing
edges with provenance, and how many nodes reach it. Where query finds candidates,
explain is what you run on the one you picked.

The argument is a node ID (target:pkg/foo:build) or a name that resolves to one
(build). Names are convenient and IDs are unambiguous; when a name matches more than
one node, resolve it with query first and pass the ID.

Two parts of the output are worth knowing about. Every edge carries its PROVENANCE -
what declared it, so a surprising relationship can be traced to the file that created
it rather than taken on faith. And the reach count answers the blast-radius question
directly: how many nodes can arrive at this one.

The graph is cache-backed under <cache>/knowledge and only shards whose sources
changed are rebuilt; --refresh forces a full rebuild.`,
	Usage: "magus explain <node-id-or-name> [flags]",
	Flags: []Flag{
		{Name: "refresh", Kind: FlagBool, Doc: "Force a full graph rebuild before explaining"},
		{Name: "global", Kind: FlagBool, Doc: "Resolve across the workspaces registered in config (knowledge.workspaces)"},
	},
	Examples: []Example{
		{"A target in full", "magus explain target:pkg/api:build"},
		{"Resolve by bare name", "magus explain build"},
		{"A spell's context", "magus explain spell:go"},
		{"As a record", "magus explain build -o json"},
	},
}

var pathCommand = Command{
	Name:        "path",
	Short:       "Connect two knowledge-graph nodes: the shortest chain of edges between them",
	Description: "Find the shortest chain of edges connecting two knowledge-graph nodes, walking edges in either direction, with each hop's relation named.",
	Tags:        []string{"cli", "magus path", "path", "knowledge graph", "shortest path", "relationship", "coupling"},
	Long: `path connects two nodes: the shortest chain of edges between them (edges
walked in either direction), with each hop's relation.

It answers the question the other two verbs cannot: not "what is this" or "what does
this touch", but "how are these two related at all". That is the question worth asking
when a change to one thing breaks a seemingly unrelated other, or when you suspect two
projects are coupled and want the chain rather than a hunch. Each hop names its
relation, so the answer is a mechanism you can check and not just a claim that a
connection exists.

Edges are walked in EITHER direction, which is what makes it useful and is also its
main caveat: a returned chain proves the two nodes are connected in the graph, not that
influence flows from one to the other. Read the hop relations to see which way each
link actually points.

Each argument is a node ID (target:pkg/foo:build) or a name that resolves to one.
The graph is cache-backed under <cache>/knowledge; --refresh forces a full rebuild.`,
	Usage: "magus path <a> <b> [flags]",
	Flags: []Flag{
		{Name: "refresh", Kind: FlagBool, Doc: "Force a full graph rebuild before pathfinding"},
		{Name: "global", Kind: FlagBool, Doc: "Resolve endpoints across the workspaces registered in config (knowledge.workspaces)"},
	},
	Examples: []Example{
		{"How are two projects related", "magus path pkg/api pkg/web"},
		{"From a spell to a diagnostic", "magus path spell:go MGS3003"},
		{"Between two targets by ID", "magus path target:pkg/api:build target:pkg/web:test"},
		{"As a record", "magus path pkg/api pkg/web -o json"},
	},
}

var graphCommand = Command{
	Name:        "graph",
	Short:       "The workspace's graphs as objects: deps, export, stats",
	Description: "Emit the project dependency DAG, export the knowledge graph for external graph tools, and report the graph's shape (god nodes, orphans, doc coverage).",
	Tags:        []string{"cli", "magus graph", "graph", "knowledge graph", "dependency graph", "export", "graphml"},
	Long: `The workspace's graphs as objects: emit, export, and measure them. The
query, explain, and path verbs read the knowledge graph; magus graph is the
home of the graph itself.

Subcommands (the first argument):

  deps     The project dependency DAG. A trailing list of project paths roots
           the graph; -o selects text, json, yaml, dot, mermaid, or tree. The
           same view scoped to a run is available as magus run <target> --graph
           and magus affected <target> --graph.
  export   The merged knowledge graph: the deterministic, cache-backed graph of
           the magus domain (projects, targets, spells, ops, charms, modules,
           methods, diagnostics, docs, buzz sources). -o json emits the
           node-link form; -o graphml emits GraphML. External graph viewers
           (Gephi, yEd) read both directly. --select "<terms>" narrows the
           export to a query's neighborhood (same engine as magus query); -o dot
           and -o mermaid render only with --select, since the full graph has too
           many nodes to lay out. The graph is cache-backed under
           <cache>/knowledge; only shards whose sources changed are rebuilt.
  stats    The graph's shape: god nodes (the most connected spells, modules,
           targets - where structural risk concentrates), orphans (docs that
           document nothing, spells no target uses), and doc coverage (the
           share of diagnostics, spells, and modules with a doc). --kind scopes
           every section to one node kind. insight report embeds this section.
  open     Open the workspace's knowledge graph (or target dependency graph with
           --targets) in the hosted, interactive Graph Explorer. The graph is
           delivered privately: by default it rides in the URL fragment
           (#data=...), which browsers never send to a server; --serve instead
           hands it from an ephemeral 127.0.0.1 loopback server (no size limit).`,
	Usage: "magus graph <deps|export|stats|open> [flags]",
	Children: []Command{
		{Name: "deps", Short: "Emit the project dependency DAG (text, json, yaml, dot, mermaid, tree)", Flags: []Flag{
			{Name: "upstream", Kind: FlagBool, Doc: "Show dependents instead of dependencies"},
			{Name: "depth", Kind: FlagInt, Doc: "Cap displayed depth (0 = unlimited)"},
			{Name: "spell", Kind: FlagString, Doc: "Only projects driven by this spell"},
			{Name: "target", Kind: FlagString, Doc: "Target whose duration history annotates nodes (default: build)"},
		}},
		{Name: "export", Short: "Export the merged knowledge graph (json node-link or graphml)", Flags: []Flag{
			{Name: "refresh", Kind: FlagBool, Doc: "Force a full graph rebuild before exporting"},
			{Name: "global", Kind: FlagBool, Doc: "Union the workspaces registered in config (knowledge.workspaces); node IDs are namespaced by workspace"},
			{Name: "reproducible", Kind: FlagBool, Doc: "Omit everything that is not a function of the source tree (locally observed runtime attrs, git history), so two checkouts of one commit export identical bytes"},
			{Name: "open", Kind: FlagBool, Doc: "Deliver the graph to the hosted Graph Explorer instead of stdout; it never leaves your machine"},
			{Name: "follow", Kind: FlagBool, Doc: "With --open: keep the explorer updating from the running daemon instead of showing a snapshot (needs magus server start)"},
			{Name: "targets", Kind: FlagBool, Doc: "With --open: open the target dependency graph instead of the knowledge graph; pass a project path to scope it"},
			{Name: "serve", Kind: FlagBool, Doc: "With --open: hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; incompatible with --targets)"},
			{Name: "print", Kind: FlagBool, Doc: "With --open: print the explorer URL to stdout instead of launching a browser"},
			{Name: "url", Kind: FlagString, Default: "https://eli.gladman.cc/magus/console/graph/", DefaultAtBind: true, Doc: "With --open: base URL of the Graph Explorer page (override for a self-hosted mirror)"},
			{Name: "static", Kind: FlagBool, Doc: "Deprecated alias for --reproducible"},
			{Name: "select", Kind: FlagString, Doc: "Export only the neighborhood of a query (same grammar as magus query); required for -o dot and -o mermaid"},
			{Name: "budget", Kind: FlagInt, Default: 50, DefaultAtBind: true, Doc: "Node budget for --select (how many nodes the neighborhood may collect)"},
		}},
		{Name: "stats", Short: "Report the knowledge graph's shape: god nodes, orphans, doc coverage", Flags: []Flag{
			{Name: "kind", Kind: FlagString, Doc: "Scope every section to one node kind (spell, target, doc, ...)"},
			{Name: "refresh", Kind: FlagBool, Doc: "Force a full graph rebuild first"},
			{Name: "global", Kind: FlagBool, Doc: "Union the workspaces registered in config (knowledge.workspaces) before computing stats"},
			{Name: "symbols", Kind: FlagBool, Doc: "Include the lazily-loaded symbol shards in the stats; excluded by default because they can dwarf the domain graph"},
		}},
	},
	Examples: []Example{
		{"Project DAG as Mermaid", "magus graph deps -o mermaid"},
		{"DAG rooted at one project, dependents up", "magus graph deps pkg/api --upstream"},
		{"Knowledge graph for an external viewer", "magus graph export -o json > graph.json"},
		{"GraphML for Gephi or yEd", "magus graph export -o graphml > graph.graphml"},
		{"A query's neighborhood as Mermaid", "magus graph export --select 'kind:spell go' -o mermaid"},
		{"Where structural risk concentrates", "magus graph stats"},
		{"Doc coverage for spells only", "magus graph stats --kind spell"},
		{"Open knowledge graph in browser", "magus graph export --open"},
		{"Open target dependency graph", "magus graph export --open --targets"},
		{"Scope target graph to one project", "magus graph export --open --targets docs"},
		{"Print the URL instead of opening", "magus graph export --open --targets --print"},
	},
}

var watchCommand = Command{
	Name:        "watch",
	Short:       "Emit changed file paths to stdout",
	Description: "Watch the workspace for file-system changes and emit batches of changed paths to stdout, compatible with git diff and magus affected --stdin.",
	Tags:        []string{"cli", "magus watch", "watch", "filesystem", "fsnotify", "continuous build"},
	Long: `Watch the workspace for file-system changes and emit batches of changed
repo-relative paths to stdout. Each path is on its own line; a blank line
separates batches. This output format is compatible with git diff --name-only
so the two are interchangeable on either side of a pipe.

Use --null for binary-safe output: paths are NUL-separated and batches end
with a double-NUL, matching the --null flag of magus affected --stdin.

On startup an --all sentinel batch is emitted (unless --initial=false) to
trigger a full initial build in the downstream magus affected --stdin.`,
	Usage: "magus watch [flags]",
	Flags: []Flag{
		{Name: "debounce", Kind: FlagDuration, Default: 200 * time.Millisecond, Doc: "Quiet window before emitting a batch"},
		{Name: "initial", Kind: FlagBool, Default: true, Doc: "Emit an --all batch on startup before watching"},
		{Name: "null", Kind: FlagBool, Doc: "NUL-separate paths; double-NUL between batches"},
		{Name: "backend", Kind: FlagString, Default: "fsnotify", Doc: "Notification backend: fsnotify or poll"},
		{Name: "ignore", Kind: FlagCustom, Doc: "Ignore pattern; repeatable. Form: type=<glob|regex|literal>,pattern=<value>"},
	},
	Examples: []Example{
		{"Continuous build pipeline", "magus watch | magus affected --stdin build"},
		{"Increase debounce for slow editors", "magus watch --debounce 500ms | magus affected --stdin test"},
		{"Polling backend (when inotify is unavailable)", "magus watch --backend poll | magus affected --stdin build"},
	},
}

var statusCommand = Command{
	Name:        "status",
	Short:       "Inspect concurrency pool and configuration",
	Description: "Show effective config plus the live concurrency pool state of any running parent magus process, with optional --watch polling and --compact output.",
	Tags:        []string{"cli", "magus status", "status", "concurrency", "pool", "daemon", "monitoring"},
	Long: `Show the magus configuration that affects this process - telemetry, cache
settings - and, when a parent magus process is running, the live state of its
concurrency pool (current slot usage, queued waiters).

When --watch is non-zero, status polls and reprints at that interval. On a
TTY the screen is cleared between reprints; piped output appends each
snapshot on its own line for log capture.`,
	Usage: "magus status [flags]",
	Flags: []Flag{
		{Name: "watch", Kind: FlagDuration, Doc: "Poll and reprint at this interval (minimum 15s; 0 means one-shot)"},
		{Name: "W", Kind: FlagDuration, AliasOf: "watch", Doc: "Short for --watch"},
		{Name: "compact", Kind: FlagBool, Doc: "Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)"},
		{Name: "c", Kind: FlagBool, AliasOf: "compact", Doc: "Short for --compact"},
		{Name: "symbols", Kind: FlagBool, Doc: "Include the expensive symbol-index freshness scan"},
		{Name: "socket", Kind: FlagString, Doc: "Adopt server address as unix:// URL or bare path; default: auto-detect from MAGUS_DAEMON_SOCKET or scan sock dir"},
		{Name: "probe", Kind: FlagString, Doc: "Exec-probe mode: liveness or readiness (exit 0 healthy, 1 unhealthy; ignores --watch/--compact)"},
		{Name: "workspace", Kind: FlagString, Doc: "Workspace root to check for readiness with --probe=readiness (default: any loaded workspace)"},
	},
	Examples: []Example{
		{"One-shot status snapshot", "magus status"},
		{"Live updates every 15 seconds", "magus status --watch=15s"},
		{"Single-line snapshot for a multiplexer sidebar", "magus status --compact --watch=15s"},
		{"Inspect a specific running parent", "magus status --socket=unix:///run/user/1000/magus/daemon.sock"},
	},
}

var doctorCommand = Command{
	Name:        "doctor",
	Short:       "Validate the workspace",
	Description: "Run diagnostic checks on the workspace covering project discovery, magusfile syntax, graph cycles, symlinks, env vars, and VCS reachability.",
	Tags:        []string{"cli", "magus doctor", "diagnostics", "troubleshooting", "validation", "workspace"},
	Long: `Run a suite of diagnostic checks against the workspace and report the
results. Checks include:

  - Project discoverability and language coverage
  - A defined ci target and clean magusfile syntax
  - Dependency graph cycles
  - Workspace-escaping symlinks
  - Recognized MAGUS_* environment variables (typo detection)
  - Charm/target name collisions
  - Consistent target naming convention (any casing, but pick one)
  - VCS base-ref reachability

Findings come at two levels. [fail] is a workspace that is wrong regardless
of how you like to work - a dependency graph cycle, an unparsable magusfile,
two targets claiming one output - and exits non-zero. [advice] is a
convention magus recommends, such as target naming or language coverage; it
is reported and exits zero, because ci is the one target name magus reserves
and the rest of the layout is yours. No flag promotes advice to failure.`,
	Usage: "magus doctor [flags]",
	Examples: []Example{
		{"Run all checks", "magus doctor"},
		{"JSON report", "magus doctor -o json"},
	},
}

var configCommand = Command{
	Name:        "config",
	Short:       "View or update magus configuration",
	Description: "Inspect the effective merged configuration or write keys to the local or global magus.yaml, with subcommands for view, set, history, cache, and mcp.",
	Tags:        []string{"cli", "magus config", "configuration", "magus.yaml", "settings", "cache"},
	Long: `Inspect or modify the magus configuration. Configuration is loaded in
priority order: built-in defaults → user-global file → workspace file →
project-local file → MAGUS_* environment variables → CLI flags.

The view sub-command prints the effective merged configuration. The set
sub-command writes a key-value pair to the local (or global) config file.
The init sub-command materializes the built-in defaults to a magus.yaml so
they can be edited by hand.

Configuration is stored in magus.yaml (or .magus.yaml). The canonical
locations are the workspace root and $XDG_CONFIG_HOME/magus/.`,
	Usage: "magus config <view|set|history|cache|mcp> [flags]",
	// Nested to match the real tree. These were five flat children with no flags,
	// while the CLI parses four levels deep and binds fourteen flags across six of
	// them - every one of which was documented nowhere, because a child could not
	// carry flags and nothing walked past the first level.
	Children: []Command{
		{Name: "view", Short: "Print the effective configuration (defaults + file + env)"},
		{
			Name:  "set",
			Short: "Write a key to the local (or global) config file",
			Flags: []Flag{
				{Name: "global", Kind: FlagBool, Doc: "Write to the global config ($XDG_CONFIG_HOME/magus/magus.yaml)"},
			},
		},
		{
			Name:  "history",
			Short: "Manage forecaster runtime history",
			Children: []Command{
				{
					Name:  "passed",
					Short: "Record a run outcome in the forecaster history",
					Flags: []Flag{
						{Name: "history", Kind: FlagString, DefaultAtBind: true, Doc: "Path to the history JSON to write (default: configured history_path)"},
						{Name: "ref", Kind: FlagString, Doc: "Ref the run was on (git branch, hg named branch, jj bookmark)"},
						{Name: "commit", Kind: FlagString, Doc: "Commit the run was at"},
						{Name: "target", Kind: FlagString, Default: "ci", Doc: "Target that ran"},
						{Name: "status", Kind: FlagString, Default: "passed", DefaultAtBind: true, Doc: "How the run came out: passed or failed"},
					},
				},
				{
					Name:  "import",
					Short: "Merge another history file into this one",
					Flags: []Flag{
						{Name: "history", Kind: FlagString, DefaultAtBind: true, Doc: "Path to the history JSON to write (default: configured history_path)"},
					},
				},
				{Name: "dedup", Short: "Collapse duplicate runs in the history file"},
			},
		},
		{
			Name:  "cache",
			Short: "Manage the build cache (prune)",
			Children: []Command{
				{
					Name:  "prune",
					Short: "Evict cache entries by age or count",
					Flags: []Flag{
						{Name: "older-than", Kind: FlagDuration, Doc: "Remove entries older than this duration (e.g. 168h = 7 days)"},
						{Name: "keep-last", Kind: FlagInt, Doc: "Keep only the newest N entries, evict the rest (--remote only)"},
						{Name: "remote", Kind: FlagBool, Doc: "Prune the configured remote backend instead of the local cache"},
						{Name: "dry-run", Kind: FlagBool, Doc: "Print what would be removed without deleting anything"},
					},
				},
				{
					Name:  "export",
					Short: "Write cache entries to an archive",
					Flags: []Flag{
						{Name: "to", Kind: FlagString, Doc: "Write the archive to this file (default: stdout)"},
					},
				},
				{Name: "import", Short: "Load cache entries from an archive"},
				{Name: "key", Short: "Manage the remote cache signing key"},
			},
		},
		{
			Name:  "mcp",
			Short: "Manage the MCP server auth token",
			Children: []Command{
				{
					Name:  "token",
					Short: "Manage the CLI's own MCP token",
					Children: []Command{
						{
							Name:  "generate",
							Short: "Create the CLI token",
							Flags: []Flag{
								{Name: "force", Kind: FlagBool, Doc: "Overwrite an existing token (rotation)"},
							},
						},
					},
				},
				{
					Name:  "connector",
					Short: "Manage connector tokens",
					Children: []Command{
						{
							Name:  "create",
							Short: "Mint a connector token",
							Flags: []Flag{
								{Name: "name", Kind: FlagString, Doc: "Name for this connector token (default: connector-N)"},
								{Name: "expires", Kind: FlagString, Doc: "Lifetime: a duration like 90d or 48h, or \"never\" (default 90d)"},
							},
						},
						{Name: "revoke", Short: "Revoke a connector token"},
					},
				},
			},
		},
	},
	Examples: []Example{
		{"Show effective config", "magus config view"},
		{"Show config as JSON", "magus config view -o json"},
		{"Set cache to read-only", "magus config set key=cache.immutable,value=true"},
		{"Prune local cache entries older than a week", "magus config cache prune --older-than 168h"},
	},
}

var serverCommand = Command{
	Name:        "server",
	Short:       "Manage the persistent magus daemon",
	Description: "Start, stop, or check liveness of the persistent magus daemon that keeps workspace discovery, config, and cache warm across invocations.",
	Tags:        []string{"cli", "magus server", "daemon", "server", "socket", "persistent"},
	Long: `Start, stop, or check the liveness of a persistent magus daemon.

By default every magus invocation starts a short-lived proc server that dies
when the command exits. The persistent daemon keeps the server alive across
invocations so workspace discovery, config loading, and the content-addressed
cache are paid for once. Nested magus calls (from build scripts, editor
integrations, etc.) forward work to the daemon automatically.

The socket address is resolved in priority order:
  --socket flag  >  MAGUS_DAEMON_ADDRESS env  >  daemon.address in magus.yaml  >
  stable default ($XDG_RUNTIME_DIR/magus/magus-daemon.sock)

The socket file acts as the lock: present means a daemon is running, absent
means none. Shell init hooks (e.g. Nix-injected .profile lines) typically
check for the file with [ -S "$socket" ] before starting one.`,
	Usage: "magus server <start|stop> [flags]",
	// Each subcommand carries its own flags. --foreground sat on the parent with
	// "(server start)" in its doc, and stop's --socket and --services were not
	// declared at all - bound by the command, absent from every man page.
	Children: []Command{
		{
			Name:  "start",
			Short: "Start a persistent daemon (auto-backgrounds by default; --foreground blocks)",
			Flags: []Flag{
				{Name: "foreground", Kind: FlagBool, Doc: "Run in the foreground and block, instead of auto-backgrounding"},
			},
		},
		{
			Name:  "stop",
			Short: "Send a graceful shutdown request to a running daemon",
			Flags: []Flag{
				{Name: "socket", Kind: FlagString, Doc: "Daemon socket (default: config / MAGUS_DAEMON_ADDRESS / auto-detect)"},
				{Name: "services", Kind: FlagBool, Doc: "Stop the daemon's hosted services, leaving the daemon running"},
			},
		},
	},
	Examples: []Example{
		{"Start the daemon (auto-backgrounds)", "magus server start"},
		{"Run the daemon in the foreground (supervisor or debugging)", "magus server start --foreground"},
		{"Stop the running daemon", "magus server stop"},
		{"Inspect daemon pool state", "magus status"},
		{"Use a custom socket path", "magus --daemon-address unix:///tmp/m.sock server start"},
	},
}

var completionCommand = Command{
	Name:        "completion",
	Short:       "Print a shell completion script",
	Description: "Print a bash, zsh, fish, or PowerShell completion script to stdout, ready to append to your shell startup file for tab-completion of magus commands.",
	Tags:        []string{"cli", "magus completion", "completion", "bash", "zsh", "fish", "powershell", "shell"},
	Long: `Print a shell completion script to stdout. Every script registers both
magus and the mgs shorthand.

Completes subcommands, the targets accepted by run and affected, the nouns
accepted by describe, the lenses accepted by insight, and each subcommand's
flags. Project paths are completed live by shelling out to magus ls -o name,
so they track the workspace instead of a baked-in list; outside a workspace
they are simply empty.

Install differs per shell, and zsh is the one that bites:

  bash        source it from ~/.bashrc, which re-reads it from the binary on
              every new shell and so survives an upgrade:
                  source <(magus completion bash)
  zsh         write it to a directory on $fpath as _magus. The script ends by
              invoking its own completion function, so sourcing it from
              ~/.zshrc runs that outside a completion context and registers
              nothing. Regenerate the file after magus self update.
  fish        write it to ~/.config/fish/completions/magus.fish, loaded on
              demand.
  powershell  append it to $PROFILE.`,
	Usage: "magus completion <bash|zsh|fish|powershell>",
	Examples: []Example{
		{"Bash: source from the rc, never goes stale", `echo 'source <(magus completion bash)' >> ~/.bashrc`},
		{"Zsh: install onto $fpath (not into ~/.zshrc)", "magus completion zsh > ~/.zsh/completions/_magus"},
		{"Fish", "magus completion fish > ~/.config/fish/completions/magus.fish"},
		{"PowerShell", "magus completion powershell >> $PROFILE"},
	},
}

var manCommand = Command{
	Name:        "man",
	Short:       "Install the man pages embedded in this binary",
	Description: "Write magus section 1 man pages from the running binary to a user-selected manpath.",
	Tags:        []string{"cli", "magus man", "manpage", "documentation", "install"},
	Long:        `Write the complete magus manpage set carried by this binary. The installer uses this command to place the pages under the selected installation prefix.`,
	Usage:       "magus man install [--dir <path>]",
	Children: []Command{
		{Name: "install", Short: "Write the embedded section 1 man pages"},
	},
	Examples: []Example{
		{"Install to the default user manpath", "magus man install"},
		{"Install to a custom prefix", "magus man install --dir ~/.local/share/man/man1"},
	},
}

// initCommand documents `magus init`. Defined here (untagged) so it can be
// embedded in the registry's All list regardless of build tags.
var initCommand = Command{
	Name:        "init",
	Short:       "Bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver)",
	Description: "Bootstrap a magus workspace with a magus.yaml config, magusfile stub, and VCS merge driver; supports global, local, and non-interactive modes.",
	Tags:        []string{"cli", "magus init", "bootstrap", "setup", "magus.yaml", "magusfile", "workspace"},
	Long: `Bootstrap a magus workspace in the current directory.

By default, magus.yaml is written to $XDG_CONFIG_HOME/magus/ (the global user
config location). Use --local to write it into the repo instead (useful for
checked-in, team-shared config). The magusfile stub and VCS merge driver are
always wired in the repo.

With --global only the global config is written; the per-clone workspace
bootstrap (magusfile stub + merge driver) is skipped.

The VCS is taken from --vcs, or chosen interactively when stdin is a terminal.

The "spell" subcommand scaffolds a new spell instead of bootstrapping a
workspace: "magus init spell <name>" writes spells/<name>/spell.buzz with the
mgs_ contract stubbed, each function documented, and a runnable test block.`,
	Usage: "magus init [flags]",
	Flags: []Flag{
		{Name: "global", Kind: FlagBool, Doc: "Write only the global config; skip the workspace bootstrap"},
		{Name: "local", Kind: FlagBool, Doc: "Write config into the repo (CWD) instead of $XDG_CONFIG_HOME/magus/"},
		{Name: "force", Kind: FlagBool, Doc: "Overwrite an existing config file"},
		{Name: "vcs", Kind: FlagString, Doc: "VCS to wire the merge driver for (git|hg); prompts when omitted on a TTY"},
	},
	Examples: []Example{
		{"Bootstrap the current repo", "magus init"},
		{"Non-interactive (CI): pick the VCS explicitly", "magus init --vcs git"},
		{"Write only the global config", "magus init --global"},
		{"Scaffold a new spell", "magus init spell mytool"},
	},
}

var versionCommand = Command{
	Name:        "version",
	Short:       "Print the client and daemon versions",
	Description: "Print the magus version string, git commit hash, and build date for the currently installed binary, plus the version reported by the daemon serving this workspace.",
	Tags:        []string{"cli", "magus version", "version", "build info", "commit", "daemon"},
	Long: `Print the magus version string, git commit hash, and build date.

Two versions, because there can be two binaries: this one, and the daemon
that has been serving the workspace since it was started. A daemon outlives
the CLI that started it, so upgrading magus leaves the older code running
until it is restarted - which is the case this command exists to show. The
daemon line reads "not running" when nothing answers, and --client skips the
probe entirely for a script that wants the build stamp with no daemon I/O.`,
	Flags: []Flag{
		{Name: "client", Kind: FlagBool, Doc: "Print only this binary's version; skip the daemon probe entirely"},
	},
	Usage: "magus version [flags]",
	Examples: []Example{
		{"Client and daemon", "magus version"},
		{"Just this binary, no daemon I/O", "magus version --client"},
		{"The bare version, for a CI pin comparison", "magus version -o name"},
	},
}

// A subcommand that reaches the dispatcher but not All is documented nowhere on the machine.
// These eight were in that state; TestManpageCoversEverySubcommand now keeps them from being.

var refsCommand = Command{
	Name:        "refs",
	Short:       "List where an ingested code symbol is defined and referenced",
	Description: "List a symbol's definition and every file that references it as file:line rows, read from the declared SCIP index rather than found by text search.",
	Tags:        []string{"cli", "magus refs", "symbols", "scip", "references", "knowledge graph"},
	Long: `List where an ingested code symbol is defined and every file that
references it, as file:line rows drawn from the SCIP index.

This is the occurrence-shaped view a symbol's fan-in needs: a flat list,
which is what you want when the question is "who calls this". The
node-link neighborhood that magus query renders is the wrong shape for
that question, which is why this is its own command rather than a flag.

The argument is a symbol node ID (symbol:...) or a name that resolves to
one. Symbols come from a declared SCIP index; see knowledge.symbols in
the configuration. A workspace with no index has no symbols to report,
and says so rather than falling back to a text search - a grep result
and an index result answer different questions, and quietly substituting
one for the other is how a wrong answer looks right.`,
	Flags: []Flag{
		{Name: "refresh", Kind: FlagBool, Doc: "Re-ingest the SCIP index before answering"},
		{Name: "occurrences", Kind: FlagBool, Doc: "Every exact source range, uncapped and verified against the tree - the view a mechanical edit needs, where the default line list is capped and describes fan-in"},
	},
	Usage: "magus refs <symbol> [flags]",
	Examples: []Example{
		{"Every reference to a symbol", "magus refs Open"},
		{"By fully-qualified node ID", "magus refs symbol:github.com/egladman/magus/Open"},
		{"As JSON", "magus refs Open -o json"},
	},
}

var cleanCommand = Command{
	Name:        "clean",
	Short:       "Remove declared Outputs (regenerable build artifacts)",
	Description: "Delete the files each selected project declares as Outputs, optionally dropping the matching cache entries so the next run rebuilds from scratch.",
	Tags:        []string{"cli", "magus clean", "outputs", "cache", "artifacts", "rebuild"},
	Long: `Remove the declared Outputs of the selected projects. With no project
arguments the cwd project (or the whole workspace, from the root) is selected.

These are the same files the cache snapshots and replays on a hit. Whether
each one is regenerable is the declaration's claim, not something clean
verifies - so a file magus only modifies rather than produces belongs in
ctx.modifiesExistingFiles, which clean never removes. Preview with the global
--dry-run flag before trusting a declaration you have not read.

--cache additionally invalidates the magus cache entries for those projects,
which is what forces a genuinely full rebuild: removing the files alone
leaves the entries that would replay them.`,
	Usage: "magus clean [flags] [project...]",
	Flags: []Flag{
		{Name: "cache", Kind: FlagBool, Doc: "Also invalidate magus cache entries for the selected projects"},
	},
	Examples: []Example{
		{"Preview what would be removed", "magus clean --dry-run"},
		{"Clean one project", "magus clean web"},
		{"Clean and force a full rebuild", "magus clean --cache"},
	},
}

var vcsCommand = Command{
	Name:        "vcs",
	Short:       "Staging and conflict resolution that knows what is generated",
	Description: "Stage a change the way the workspace's declarations say it should be staged, and settle an in-progress merge's conflicted generated files by regenerating them once.",
	Tags:        []string{"cli", "magus vcs", "git", "merge", "conflicts", "generated files", "staging"},
	Long: `Version-control operations that read the workspace's output declarations,
so generated files and hand-written sources are treated differently.

add stages what the workspace declares: sources, and the generated outputs a
source change in the same commit accounts for. Anything undeclared is reported
rather than swept in, which is the difference between it and git add -A.

resolve settles an in-progress merge, rebase, or cherry-pick. It classifies
every conflicted path at once, regenerates once instead of once per file,
settles the files one side deleted (which no VCS invokes a merge driver for),
and stages everything the regeneration touched so a following commit or
rebase --continue does not refuse on a dirty tree. Conflicts in files magus
does not generate are reported and left alone. Pass --against <ref> to merge
that ref first and settle what it conflicts with; add --dry-run to see the
classification and have the merge backed out again.

merge-driver is the per-file driver git and hg invoke during a merge. You do
not run it by hand; it is wired per clone, because a driver registration
cannot be committed. That is also why a forge reports conflicts your own
clone would settle silently, and why resolve exists as the bulk counterpart.

checkpoint prints the identity of the working state right now - head revision,
branch, whether the tree is dirty, and a digest of the uncommitted patch. Record
one when you hand a piece of work out, so a later reader knows what that work was
looking at. It RESOLVES AND RECORDS and never MINTS: no tag, no stash, no ref, no
file, nothing changed anywhere, so a checkpoint nobody keeps has cost nothing.
Feed the revision to anything that takes one; compare two digests to learn whether
two workers saw the same uncommitted tree, which the revision alone cannot say.

resolve works on git, Mercurial and Jujutsu. Only --against is git-only: merge the
base in yourself on the others, then run resolve.`,
	// No parent Flags: neither flag belongs to `magus vcs`, which takes none of
	// its own. They were declared here with the owning subcommand named in the doc
	// text ("(vcs resolve)", "(vcs add)") because a child could not carry flags,
	// which put them in one merged Options section on the man page and made a
	// generated binder for either child bind both.
	Usage: "magus vcs <add|resolve|checkpoint|merge-driver> [flags]",
	Children: []Command{
		{
			Name:  "add",
			Short: "Stage a change the way this workspace's declarations say it should be staged",
			Flags: []Flag{
				{Name: "untracked", Kind: FlagBool, Doc: "Also stage undeclared files"},
			},
		},
		{
			Name:  "resolve",
			Short: "Settle an in-progress merge's conflicted generated files, then regenerate once",
			Flags: []Flag{
				{Name: "against", Kind: FlagString, Doc: "Merge this `ref` first, then settle what it conflicts with"},
			},
		},
		{
			Name:  "checkpoint",
			Short: "Print the working state's identity, for recording what a delegated unit was handed; writes nothing",
		},
		{Name: "merge-driver", Short: "The per-file merge driver git and hg invoke; you do not run this by hand"},
	},
	Examples: []Example{
		{"Stage a change without sweeping in build residue", "magus vcs add"},
		{"Classify the dirty tree, stage nothing", "magus vcs add --dry-run"},
		{"Settle a conflicted merge", "magus vcs resolve"},
		{"Merge the base in and settle it in one step", "magus vcs resolve --against origin/main"},
		{"Record what a delegated unit was handed", "magus vcs checkpoint"},
		{"The one citable token, for a ledger cell", "magus vcs checkpoint -o name"},
	},
}

var memoryCommand = Command{
	Name:        "memory",
	Short:       "Durable cross-session project memory",
	Description: "Manage the per-repository handoff journal that lives outside the checkout: named entries people and agents can read across sessions and worktrees.",
	Tags:        []string{"cli", "magus memory", "handoff", "journal", "agents"},
	Long: `Manage the per-repository handoff journal, which is stored outside the
checkout so it survives worktrees and branch switches.

Entries are visible to people and to agents across sessions. They are NOT
automatic model memory: nothing writes one for you, and nothing is recalled
implicitly. An entry earns its place when a later reader needs to reopen the
evidence behind a decision - the run, the query, the output reference, the
document - rather than to be told a conclusion.

verify is the maintenance verb: it reports entries that are malformed, stale,
or that link to something no longer there. The same entries are reachable
through the magus_memory MCP tool and the console, so a journal written from
the CLI is readable by an agent without either side learning a new format.`,
	Usage: "magus memory <ls|get|put|delete|verify> [flags]",
	Children: []Command{
		{Name: "ls", Short: "Show entries and any repair warnings"},
		{Name: "get", Short: "Show one entry"},
		{
			Name:  "put",
			Short: "Create or replace a named entry",
			Flags: []Flag{
				{Name: "type", Kind: FlagString, Doc: "Entry type: pointer, decision, or plan"},
				{Name: "status", Kind: FlagString, Doc: "Lifecycle label, e.g. accepted, active, done, stale"},
				{Name: "body", Kind: FlagString, Doc: "Short why/caption, decision and plan only"},
				// Repeatable, so bound by the command itself; declared here only so
				// they reach the man page, which never listed them.
				{Name: "ref", Kind: FlagCustom, Doc: "Entry ref in 'kind: target' form; repeat for multiple refs"},
				{Name: "reference", Kind: FlagCustom, Doc: "Name of another entry this one relates to; repeat as needed"},
			},
		},
		{Name: "delete", Short: "Remove one entry"},
		{Name: "verify", Short: "Check malformed, stale, and broken-linked entries"},
	},
	Examples: []Example{
		{"List entries and warnings", "magus memory ls"},
		{"Read one entry", "magus memory get release-checklist"},
		{"Check the journal's health", "magus memory verify"},
	},
}

var diffCommand = Command{
	Name:        "diff",
	Short:       "Read the working tree's changes in the order they deserve attention",
	Description: "Report every uncommitted change annotated with what the workspace knows: whether it is generated, how widely its changed symbols are referenced, whether it is public API surface, and what coverage was observed.",
	Tags:        []string{"cli", "magus diff", "diff", "review", "changeset", "semver"},
	Long: `Read the working tree's uncommitted changes, annotated and ordered.

A changeset is not a list of files, it is a set of CONSEQUENCES, and a reader's
attention is scarce. Alphabetical order spends it at random: it gives a
regenerated lockfile the same weight as a signature change twelve packages
depend on. This orders by what a change can BREAK.

Generated files - declared target outputs - are folded away by default. Reading
one is reading a machine's restatement of a change made somewhere else, so the
source edit is the review. Pass --generated to see them anyway.

Each file carries the evidence behind its rank: how many files reference the
widest changed symbol it defines, whether any of those referents cross a project
boundary or the module boundary (which is the question a version bump turns on),
and the coverage a prior run observed. None of it is a verdict. magus does not
claim a change is breaking - deciding that needs signature compatibility, which
needs a base-side index magus does not keep and language semantics it does not
model - it reports who can see the thing you changed and lets you decide.

The console's Diff surface reads the same annotations over the same session,
and an agent can join that session through the magus_diff MCP tool.`,
	Usage: "magus diff [--generated] [--tui] [--watch] [<patch-file>|-] [flags]",
	Flags: []Flag{
		{Name: "generated", Kind: FlagBool, Doc: "Include declared target outputs, which are folded away by default"},
		{Name: "tui", Kind: FlagBool, Doc: "Read the changeset interactively, joined to the session the console and an agent share"},
		{Name: "watch", Kind: FlagBool, Doc: "Re-read and re-render whenever the working tree changes"},
	},
	Examples: []Example{
		{"Read what you are about to commit", "magus diff"},
		{"Include the generated files too", "magus diff --generated"},
		{"Navigate it hunk by hunk and mark what you have read", "magus diff --tui"},
		{"Machine-readable, for a script or a Buzz advisor", "magus diff -o json"},
	},
}

var notesCommand = Command{
	Name:        "notes",
	Short:       "Human-authored notes committed to the repository",
	Description: "Read the workspace's human-authored notes: prose a person wrote about the code, anchored to graph entities but derived from none of them.",
	Tags:        []string{"cli", "magus notes", "notes", "knowledge", "annotations"},
	Long: `Read the workspace's human-authored notes.

A note carries knowledge whose only provenance is a person: something no
extractor could derive from the tree, and that no rebuild would recover. That
is what separates it from the two things it is NOT. Documentation describes the
system for a reader and lives in the docs tree. A NOTE/WHY/TODO comment marks
one location in one file. A note attaches to graph ENTITIES - a symbol, a file,
a project, a target, another note - and may span several at once, which is how
it can record something like "these two caches must be invalidated together"
that no single comment could hold.

Anchors are required, and they name node IDs rather than positions. A symbol
anchor survives the code moving file and line and breaks only on a rename or a
deletion, which is exactly when the note should be re-read; a line number would
break on the next edit above it with nothing to detect.

There is no put. Notes are written by a person in their own editor and
committed under their own name, which is what makes git attribution meaningful
and what keeps the store worth trusting. Set knowledge.notes.path in magus.yaml
to declare where they live; with nothing declared the feature is inert.`,
	Usage: "magus notes <ls|get|edit|verify> [flags]",
	Children: []Command{
		{Name: "ls", Short: "Show notes and any repair warnings"},
		{Name: "get", Short: "Show one note"},
		{Name: "edit", Short: "Open one note in $VISUAL or $EDITOR"},
		{Name: "verify", Short: "Check malformed notes and anchors that no longer resolve"},
	},
	Examples: []Example{
		{"List every note", "magus notes ls"},
		{"Read one note", "magus notes get cache-invalidation-pairing"},
		{"Write or revise one", "magus notes edit cache-invalidation-pairing"},
		{"Check every anchor still resolves", "magus notes verify"},
	},
}

var buzzCommand = Command{
	Name:        "buzz",
	Short:       "Run a Buzz script",
	Description: "Run Buzz from a REPL, a file, stdin, or an inline snippet, with the Buzz stdlib, every magus host module, and the magus namespace available.",
	Tags:        []string{"cli", "magus buzz", "buzz", "scripting", "repl", "lsp"},
	Long: `Run Buzz source from a REPL, a file, stdin, or an inline snippet.

With no argument on a terminal it opens a REPL with the magusfile at the
current directory loaded, its targets and bindings ready. A piped or
redirected stdin runs as a script instead. In both, the Buzz stdlib, every
magus host module (fs, os, http, markdown, and the rest), and the magus
namespace are available, so a one-off script needs no dependency install.

Parsing is upstream-strict by default: a file written for the magusfile
engine needs --embedded, or it fails on rules upstream Buzz enforces and
magus does not. The most common one is "argument N must be labeled".

-t runs a file's test blocks and reports pass or fail, which is how Buzz
code in this ecosystem is tested. The lsp subcommand speaks the Language
Server Protocol over stdio for an editor integration.`,
	Flags: []Flag{
		// Backticks are load-bearing: flag.UnquoteUsage reads the quoted word as the
		// value's name, so this renders `-e code` rather than `-e string`. The
		// hand-written binding had them and the registry copy did not, which is the
		// kind of difference two lists drift into when only their names are compared.
		{Name: "e", Kind: FlagString, Doc: "Execute `code` given on the command line instead of a file"},
		{Name: "t", Kind: FlagBool, Doc: `Run the file's test "..." {} blocks and report pass/fail`},
		{Name: "test", Kind: FlagBool, AliasOf: "t", Doc: "Alias for -t"},
		{Name: "embedded", Kind: FlagBool, Doc: "Relax upstream strictness (top-level statements, optional argument labels) to match the magusfile engine"},
		{Name: "no-autoload", Kind: FlagBool, Doc: "Start the REPL without executing the magusfile"},
		{Name: "C", Kind: FlagString, Doc: "Working directory for the REPL's import resolution (default: cwd)"},
	},
	Usage: "magus buzz [file|-|lsp] [flags]",
	Children: []Command{
		{Name: "lsp", Short: "Language server over stdio (LSP)"},
	},
	Examples: []Example{
		{"Open a REPL with the magusfile loaded", "magus buzz"},
		{"Run a script", "magus buzz scripts/report.buzz"},
		{"Run an inline snippet", `magus buzz -e 'import "std"; fun main() > void { std\print("hi"); } main();'`},
		{"Run a file's test blocks", "magus buzz -t scripts/report.buzz"},
		{"Run a magusfile-style file", "magus buzz --embedded scripts/target.buzz"},
	},
}

var agentCommand = Command{
	Name:        "agent",
	Short:       "Install the knowledge-graph agent skills into a repo",
	Description: "Render the embedded agent skills into a repository's skill directories, or print a starter AGENTS.md; it never writes the AGENTS.md you own.",
	Tags:        []string{"cli", "magus agent", "skills", "agents", "AGENTS.md", "install"},
	Long: `Render the agent skills embedded in this binary and write or stream them
into named destinations (.claude/skills, .agents/skills, .opencode/skills,
and so on).

magus never writes your AGENTS.md. That file is yours, and an installer that
edits a file you own leaves bytes you did not write and cannot audit. So
install PRINTS the managed magus block for you to paste, and only when your
AGENTS.md is missing it or is carrying a stale one. sample prints a starter
AGENTS.md to stdout for you to own and tweak, and never writes a file.

agent is a pure data generator, which is what makes --tar the general
answer: it streams a tar archive to stdout, so skills can be installed
anywhere a shell can reach. The write-to-disk form exists for the in-repo,
paths-relative-to-<dir> case. Absolute destinations are refused unless
--global is set, so magus cannot silently write outside the working tree.`,
	Usage: "magus agent <install|sample> [flags]",
	Children: []Command{
		{Name: "install", Short: "Render the embedded skills and write or stream them into named destinations"},
		{Name: "sample", Short: "Print a starter AGENTS.md to stdout; never writes a file"},
	},
	Flags: []Flag{
		{Name: "dir", Kind: FlagString, Default: ".", Doc: "Repo directory to install into (agent install)"},
		{Name: "force", Kind: FlagBool, Doc: "Overwrite existing installed skill files (agent install)"},
		{Name: "prune", Kind: FlagBool, Doc: "Also remove installed skills this binary no longer ships; without it they are reported and left in place, and only skills magus wrote are ever candidates (agent install)"},
		{Name: "tar", Kind: FlagBool, Doc: "Stream a tar archive to stdout instead of writing files (agent install)"},
		{Name: "global", Kind: FlagBool, Doc: "Allow absolute destination paths in write mode (agent install)"},
	},
	Examples: []Example{
		{"Install into a repo's agent skills directory", "magus agent install .claude/skills"},
		{"Refresh installed skills", "magus agent install .claude/skills --force"},
		{"Refresh, and drop skills this version no longer ships", "magus agent install .claude/skills --force --prune"},
		{"Install anywhere via tar", "magus agent install --tar | tar -xf - -C ~/.config/opencode/skills"},
		{"Print a starter AGENTS.md", "magus agent sample"},
	},
}

var hookCommand = Command{
	Name:        "hook",
	Short:       "Evaluate one shell command or file path against the magus guard rules",
	Description: "Read one shell command, or one path an edit is about to write, and report a deny, advise, or pass verdict for an agent host's pre-tool-use hook.",
	Tags:        []string{"cli", "magus hook", "guard", "agents", "policy", "pre-tool-use"},
	Long: `Evaluate ONE shell command, or one file path an edit is about to write,
against this workspace's guard rules, and report a deny, advise, or pass
verdict.

It is built for an agent host's pre-tool-use hook. The input arrives on
stdin, so nothing has to survive being quoted through a shell twice.

Two input shapes are accepted. Plain text is the command (or the path)
itself. A JSON envelope from a host that writes one needs neither --path nor
a JSON tool to unwrap it: the envelope already says what is about to run and
whether it is a write. An explicit flag still wins, because a wrapper that
passed one meant it.

--observe records a path the agent merely REACHED, without judging it. No rule
applies to a read, so the verdict is always pass and the activity event
previews as observed rather than as a guard decision. Which of a host's tools
only look is the wrapper's knowledge, never magus's.

--agent-name, --session, --transcript, and --event are attribution, not policy.
They record who produced the observation on the activity event, and the verdict
never reads them. All are optional and unvalidated, including the host name,
which is an opaque label the caller chooses rather than a set magus knows: a
magus that enumerated hosts would need a release per host, and a wrapper that
cannot extract a session id must still be able to get a verdict.`,
	Usage: "magus hook [--path] [flags]",
	Flags: []Flag{
		{Name: "path", Kind: FlagBool, Doc: "Judge the input as a file path an edit is about to write, not as a shell command"},
		{Name: "observe", Kind: FlagBool, Doc: "Record the input as a path the agent reached, without judging it: no rule applies and the verdict is always pass"},
		{Name: "agent-name", Kind: FlagString, Doc: "Name of the agent host this invocation came from (attribution only)"},
		{Name: "session", Kind: FlagString, Doc: "The host's own session id for this invocation"},
		{Name: "transcript", Kind: FlagString, Doc: "Path to the host's own log of this session, recorded as a pointer; magus never opens it"},
		{Name: "event", Kind: FlagString, Doc: "The host's hook event name (e.g. PreToolUse)"},
	},
	Examples: []Example{
		{"Judge a shell command", "printf '%s' 'go build ./...' | magus hook"},
		{"Judge a path an edit is about to write", "printf '%s' 'MAGUS.md' | magus hook --path"},
		{"Record a path an agent read, without judging it", "printf '%s' 'internal/cache/output.go' | magus hook --observe"},
		{"Machine-readable verdict", "printf '%s' 'rm -rf /' | magus hook -o json"},
	},
}

var notifyCommand = Command{
	Name:        "notify",
	Short:       "Normalize an attention event and optionally notify the local desktop",
	Description: "Raise one canonical attention event from plain text or a JSON envelope, and optionally surface it as an operating-system notification.",
	Tags:        []string{"cli", "magus notify", "notifications", "events", "attention", "desktop"},
	Long: `Raise one canonical attention event.

The input is read from stdin and may be plain text or a complete event JSON
envelope. Either way the result is one normalized event, so a caller that
knows nothing about magus's event schema can still raise a well-formed one.

--outcome names the event's outcome vocabulary. --desktop additionally
raises an operating-system notification, which is the part a human notices;
without it the event is recorded and nothing pops up.`,
	Usage: "magus notify [--outcome <vocab>] [--desktop]",
	Flags: []Flag{
		{Name: "outcome", Kind: FlagString, Doc: "Outcome vocabulary for the event"},
		{Name: "desktop", Kind: FlagBool, Doc: "Also raise an OS notification"},
	},
	Examples: []Example{
		{"Raise a permission prompt on the desktop", "printf '%s\\n' 'needs approval' | magus notify --outcome permission --desktop"},
		{"Raise a pre-built event envelope", `printf '%s\n' '{"outcome":"permission","source":{"kind":"agent"},"message":"needs approval"}' | magus notify --desktop`},
	},
}
