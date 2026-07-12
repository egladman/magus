package manpage

import (
	"flag"
	"time"
)

// All is the ordered list of magus top-level commands consumed by the
// man-page generator (internal/manpage).
var All = []Command{
	listCommand,
	describeCommand,
	runCommand,
	xCommand,
	whereCommand,
	tailCommand,
	affectedCommand,
	insightCommand,
	graphCommand,
	watchCommand,
	statusCommand,
	doctorCommand,
	configCommand,
	serverCommand,
	completionCommand,
	initCommand,
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
	{Name: "clean", Short: "Remove build artefacts from selected projects"},
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
against the same struct that -o json emits.`,
	Usage: "magus ls [flags]",
	Examples: []Example{
		{"List all projects", "magus ls"},
		{"Pipe-friendly: one path per line", "magus ls -o name"},
		{"JSON output", "magus ls -o json"},
		{"Custom Go template", `magus ls -o template='{{range .Projects}}{{.Path}}{{"\n"}}{{end}}'`},
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
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Bool("explain", false, "For a target ref with charms: show the per-charm argv trace (base then each charm)")
		fs.Bool("evaluated", false, "For projects: print workspace-rooted globs, effective claims, and per-target policies")
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

The target ci is an ordinary magusfile-defined target — magus does not hardcode
its steps; your magusfile composes them with magus.needs. magus keeps ci as
the anchor that the affected set keys off, and always runs it read-only; apply
the rw charm (e.g. 'magus run format:rw') to mutate files.`,
	Usage: "magus run <target> [flags] [project...]",
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Bool("dry-run", false, "Print what would run without executing")
		fs.Bool("graph", false, "Render the dependency graph for the selected scope instead of executing")
		fs.Bool("upstream", false, "With --graph: show dependents instead of dependencies")
		fs.Int("depth", 0, "With --graph: cap displayed depth (0 = unlimited)")
	},
	Targets: CommonTargets,
	Examples: []Example{
		{"Build everything", "magus run build"},
		{"Test one project", "magus run test api/gateway"},
		{"Build two specific projects", "magus run build api/gateway web/studio"},
		{"Dry-run: show what would run", "magus run build --dry-run"},
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
stderr and the command exits 2. No interactive picker — use magus x for
that.`,
	Usage: "magus where [filter...]",
	Examples: []Example{
		{"Navigate to a project", `cd "$(magus where api)"`},
		{"Open in editor", `code "$(magus where dash)"`},
		{"AND-filter: must match both tokens", "magus where api gateway"},
	},
}

var tailCommand = Command{
	Name:        "tail",
	Short:       "Stream the most recent cached log (interactive only)",
	Description: "Stream the captured build log of the most recent cache entry for a project, with -f to follow and target selectors like project:test.",
	Tags:        []string{"cli", "magus tail", "tail", "logs", "cache", "interactive"},
	Long: `Stream the captured build log of the most recent cache entry for a
project. The log was written during a cache miss (when the build actually
ran). Subsequent cache hits replay the same log without re-running the build.

Requires an interactive terminal (like magus x). Set assume_interactive: true
in magus.yaml or MAGUS_ASSUME_INTERACTIVE=1 to override.

target follows the canonical path:target form used by magus run:
  (none)     cwd project, latest run of any target
  :build     cwd project, latest build run
  api        api project, latest run of any target
  api:test   api project, latest test run

Exits non-zero when the project is not found, or when no cache entries
exist yet (run a build first).`,
	Usage: "magus tail [-f] [-n N] [target]",
	Examples: []Example{
		{"Stream last log for cwd project", "magus tail"},
		{"Follow (stream new output as it arrives)", "magus tail -f"},
		{"Show last 50 lines", "magus tail -n 50"},
		{"Show entire log", "magus tail -n 0"},
		{"Last test run for the api project", "magus tail api:test"},
		{"Last build run for cwd project", "magus tail :build"},
	},
}

var xCommand = Command{
	Name:        "x",
	Short:       "Interactive shorthand: pick project + target",
	Description: "Interactive shorthand for magus run with a TTY picker for project and target, remembering the last target used per project.",
	Tags:        []string{"cli", "magus x", "interactive", "picker", "shorthand", "run", "tty"},
	Long: `Interactive shorthand for magus run. Filters are AND-combined
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
	Usage: "magus x [filter...]",
	Examples: []Example{
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
JSON CI shard plan for the affected set (for CI fan-out; always keys off the ci
anchor). --bisect drives VCS bisect using run history to find the commit that
introduced a regression.`,
	Usage: "magus affected <target> [flags]",
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Bool("dry-run", false, "Print what would run without executing")
		fs.String("base", "", "Override base ref for the VCS diff (default: MAGUS_VCS_BASE_REF or per-VCS built-in)")
		fs.Bool("stdin", false, "Read changed file paths from stdin instead of running a VCS diff")
		fs.Bool("null", false, "With --stdin: expect NUL-separated paths and double-NUL between batches")
		fs.Bool("graph", false, "Render the dependency graph for the affected scope instead of executing")
		fs.Bool("upstream", false, "With --graph: show dependents instead of dependencies")
		fs.Int("depth", 0, "With --graph: cap displayed depth (0 = unlimited)")
		fs.String("explain", "", "Show why <project> is in the affected set instead of executing")
		fs.Bool("plan", false, "Emit a provider-neutral JSON CI shard plan for the affected set")
		fs.Int("max-shards", 8, "With --plan: maximum CI shards (-1 = unlimited)")
		fs.Int("max-parallel-budget", 0, "With --plan: cross-shard concurrency cap; 0 = unlimited")
		fs.String("bisect", "", "Drive VCS bisect to find the commit that broke <project>")
		fs.String("good", "", "With --bisect: known-good commit SHA (auto-detected from history when empty)")
		fs.String("target", "test", "With --bisect: magus target to bisect")
	},
	Targets: CommonTargets,
	Examples: []Example{
		{"Build projects changed since the default base ref", "magus affected build"},
		{"Use a different base ref", "magus affected build --base main"},
		{"Pipe from watch for continuous builds", "magus watch | magus affected --stdin build"},
		{"List affected projects without building", "magus affected list"},
		{"Show dependency graph for the affected scope", "magus affected build --graph"},
		{"Graph as DOT for piping to Graphviz", "magus affected build --graph -o dot | dot -Tsvg > graph.svg"},
		{"Emit a CI shard plan for the affected set", "magus affected --plan"},
		{"Shard plan limited to four shards", "magus affected --plan --max-shards 4"},
		{"Bisect a regression in myapp", "magus affected --bisect ./apps/myapp"},
	},
}

var insightCommand = Command{
	Name:        "insight",
	Short:       "Behavioral code analysis from VCS and run-outcome history",
	Description: "Show where a codebase's attention and risk concentrate: hotspots, temporal coupling, ownership, and trend from VCS history, plus volatility from run-outcome history.",
	Tags:        []string{"cli", "magus insight", "analysis", "hotspots", "ownership", "coupling", "vcs", "volatility", "flaky"},
	Long: `Read history to show where a codebase's attention and risk concentrate.
Four lenses read version-control history; a fifth, volatility, reads run-outcome
history instead. The VCS lenses are contextual to the working directory by default -
run from inside a subtree and each reflects only that subtree's history; pass
--workspace to analyze the whole workspace (the fan-in postflight uses this). The
active VCS adapter must report per-commit files (git can).

VCS-history lenses (the first argument):

  hotspots   Edit frequency x complexity - the prime refactoring targets. The
             project view is the dependency graph heat-coloured by churn (with
             authors, recency, blast radius, and CI duration); --files ranks
             individual files by churn x complexity.
  affinity   Projects that change together (temporal coupling). A hidden pair
             co-changes without either declaring a dependency on the other - a
             candidate architectural smell.
  ownership  Author concentration: the primary author and their share, distinct
             author count (bus factor), and abandonment (projects gone quiet).
  trend      The recent half of the window versus the earlier half: a positive
             delta is a rising hotspot, a negative one is cooling.

Run-outcome lens:

  volatility Each (project, target) pair's recent pass/fail/volatile record scored
             by its Wilson lower bound; a pair at or above the configured threshold
             is flagged volatile - a flakiness signal, the prime stabilization
             targets. It reads the shared runtime-history file, not git, so it takes
             no --commits/--since window and is always workspace-wide.

  report     Every lens plus graph stats as one whole-workspace Markdown document
             (the magusfile's postflight target writes this to the GitHub Actions
             step summary).

The VCS lenses read the commit log: --commits caps the scan; --since bounds it by
date (90d, 12w, 6mo, 1y). Each lens accepts -o text|json|yaml|name; hotspots and
affinity also render -o mermaid (the hotspots file view renders a
churn-vs-complexity quadrant). The structural companion - god nodes, orphans, and
doc coverage from the knowledge graph - is magus graph stats; the report embeds it.`,
	Usage: "magus insight <lens> [flags]",
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Int("commits", 500, "Cap on how many recent commits to scan (VCS lenses only)")
		fs.String("since", "", "Only commits within this window, e.g. 90d, 12w, 6mo, 1y (VCS lenses only)")
		fs.Bool("workspace", false, "Analyze the whole workspace instead of the current project/subtree")
		fs.Bool("files", false, "hotspots: rank individual files instead of projects")
	},
	Examples: []Example{
		{"Prime refactoring targets (files)", "magus insight hotspots --files"},
		{"Churn-vs-complexity quadrant", "magus insight hotspots --files -o mermaid"},
		{"Hidden architectural coupling", "magus insight affinity"},
		{"Bus factor and abandonment", "magus insight ownership"},
		{"Rising vs cooling activity", "magus insight trend --since 90d"},
		{"Flaky (volatile) targets", "magus insight volatility"},
		{"Whole-workspace report (all lenses)", "magus insight report --workspace"},
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
		{Name: "deps", Short: "Emit the project dependency DAG (text, json, yaml, dot, mermaid, tree)", BuildFlags: func(fs *flag.FlagSet) {
			fs.Bool("upstream", false, "Show dependents instead of dependencies")
			fs.Int("depth", 0, "Cap displayed depth (0 = unlimited)")
			fs.String("spell", "", "Only projects driven by this spell")
			fs.String("target", "", "Target whose duration history annotates nodes (default: build)")
		}},
		{Name: "export", Short: "Export the merged knowledge graph (json node-link or graphml)", BuildFlags: func(fs *flag.FlagSet) {
			fs.Bool("refresh", false, "Force a full graph rebuild before exporting")
			fs.Bool("global", false, "Union the workspaces registered in config (knowledge.workspaces); node IDs are namespaced by workspace")
			fs.String("select", "", "Export only the neighborhood of a query (same grammar as magus query); required for -o dot and -o mermaid")
			fs.Int("budget", 50, "Node budget for --select (how many nodes the neighborhood may collect)")
		}},
		{Name: "stats", Short: "Report the knowledge graph's shape: god nodes, orphans, doc coverage", BuildFlags: func(fs *flag.FlagSet) {
			fs.String("kind", "", "Scope every section to one node kind (spell, target, doc, ...)")
			fs.Bool("refresh", false, "Force a full graph rebuild first")
			fs.Bool("global", false, "Union the workspaces registered in config (knowledge.workspaces) before computing stats")
		}},
		{Name: "open", Short: "Open the workspace graph in the hosted Graph Explorer (data never leaves your machine)", BuildFlags: func(fs *flag.FlagSet) {
			fs.Bool("targets", false, "Open the target dependency graph instead of the knowledge graph; pass a project path as a positional argument to scope to one project")
			fs.Bool("serve", false, "Hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; incompatible with --targets)")
			fs.Bool("print", false, "Print the explorer URL to stdout instead of opening a browser")
			fs.Bool("refresh", false, "Force a full graph rebuild before opening (knowledge graph only)")
			fs.String("url", "https://eli.gladman.cc/magus/console/graph/", "Base URL of the Graph Explorer page (override for a self-hosted mirror)")
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
		{"Open knowledge graph in browser", "magus graph open"},
		{"Open target dependency graph", "magus graph open --targets"},
		{"Scope target graph to one project", "magus graph open --targets website"},
		{"Print the URL instead of opening", "magus graph open --targets --print"},
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
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Duration("debounce", 200*time.Millisecond, "Quiet window before emitting a batch")
		fs.Bool("initial", true, "Emit an --all batch on startup before watching")
		fs.Bool("null", false, "NUL-separate paths; double-NUL between batches")
		fs.String("backend", "fsnotify", "Notification backend: fsnotify or poll")
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
	Long: `Show the magus configuration that affects this process — telemetry, cache
settings — and, when a parent magus process is running, the live state of its
concurrency pool (current slot usage, queued waiters).

When --watch is non-zero, status polls and reprints at that interval. On a
TTY the screen is cleared between reprints; piped output appends each
snapshot on its own line for log capture.`,
	Usage: "magus status [flags]",
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Duration("watch", 0, "Poll and reprint at this interval (e.g. --watch=1s); 0 means one-shot")
		fs.Bool("compact", false, "Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)")
		fs.String("socket", "", "Adopt server address as unix:// URL or bare path; default: auto-detect from MAGUS_DAEMON_SOCKET or scan sock dir")
		fs.String("probe", "", "Exec-probe mode: liveness or readiness (exit 0 healthy, 1 unhealthy; ignores --watch/--compact)")
		fs.String("workspace", "", "Workspace root to check for readiness with --probe=readiness (default: any loaded workspace)")
	},
	Examples: []Example{
		{"One-shot status snapshot", "magus status"},
		{"Live updates every second", "magus status --watch=1s"},
		{"Single-line snapshot for a multiplexer sidebar", "magus status --compact --watch=1s"},
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
  - Recognised MAGUS_* environment variables (typo detection)
  - Charm/target name collisions
  - Consistent target naming convention (any casing, but pick one)
  - VCS base-ref reachability

Every check is pass or fail; there are no warnings. Exits non-zero if any
check fails.`,
	Usage: "magus doctor [flags]",
	Examples: []Example{
		{"Run all checks", "magus doctor"},
		{"JSON report", "magus doctor -o json"},
	},
}

var configCommand = Command{
	Name:        "config",
	Short:       "View or update magus configuration",
	Description: "Inspect the effective merged configuration or write keys to the local or global magus.yaml, with subcommands for view, set, init, and cache prune.",
	Tags:        []string{"cli", "magus config", "configuration", "magus.yaml", "settings", "cache"},
	Long: `Inspect or modify the magus configuration. Configuration is loaded in
priority order: built-in defaults → user-global file → workspace file →
project-local file → MAGUS_* environment variables → CLI flags.

The view sub-command prints the effective merged configuration. The set
sub-command writes a key-value pair to the local (or global) config file.
The init sub-command materialises the built-in defaults to a magus.yaml so
they can be edited by hand.

Configuration is stored in magus.yaml (or .magus.yaml). The canonical
locations are the workspace root and $XDG_CONFIG_HOME/magus/.`,
	Usage: "magus config <view|set|init> [flags]",
	Children: []Command{
		{Name: "view", Short: "Print the effective configuration (defaults + file + env)"},
		{Name: "set", Short: "Write a key to the local (or global) config file"},
		{Name: "init", Short: "Materialise built-in defaults to magus.yaml"},
		{Name: "cache", Short: "Manage the build cache (prune --older-than)"},
	},
	Examples: []Example{
		{"Show effective config", "magus config view"},
		{"Show config as JSON", "magus config view -o json"},
		{"Set cache to read-only", "magus config set cache.immutable true"},
		{"Initialise magus.yaml from defaults", "magus config init"},
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
	Children: []Command{
		{Name: "start", Short: "Start a persistent daemon (foreground; use & or a supervisor to background)"},
		{Name: "stop", Short: "Send a graceful shutdown request to a running daemon"},
	},
	Examples: []Example{
		{"Start daemon in the background", "magus server start &"},
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
	Long:        `Print a shell completion script to stdout and append it to your shell's startup file.`,
	Usage:       "magus completion <bash|zsh|fish|powershell>",
	Examples: []Example{
		{"Bash", "magus completion bash >> ~/.bashrc"},
		{"Zsh", "magus completion zsh >> ~/.zshrc"},
		{"Fish", "magus completion fish >> ~/.config/fish/config.fish"},
		{"PowerShell", "magus completion powershell >> $PROFILE"},
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
	BuildFlags: func(fs *flag.FlagSet) {
		fs.Bool("global", false, "Write only the global config; skip the workspace bootstrap")
		fs.Bool("local", false, "Write config into the repo (CWD) instead of $XDG_CONFIG_HOME/magus/")
		fs.Bool("force", false, "Overwrite an existing config file")
		fs.String("vcs", "", "VCS to wire the merge driver for (git|hg); prompts when omitted on a TTY")
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
	Short:       "Print version, commit, and build date",
	Description: "Print the magus version string, git commit hash, and build date for the currently installed binary.",
	Tags:        []string{"cli", "magus version", "version", "build info", "commit"},
	Long:        `Print the magus version string, git commit hash, and build date.`,
	Usage:       "magus version",
}
