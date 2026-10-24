package commands

import (
	"time"

	"github.com/egladman/magus/libs/cmdline"
)

// commonTargets is the canonical set of project-scoped targets shared by
// the "run" and "affected" commands.
var commonTargets = []cmdline.Operand{
	{Name: "ls", Short: "Print selected projects without executing anything"},
	{Name: "build", Short: "Build selected projects"},
	{Name: "test", Short: "Test selected projects"},
	{Name: "lint", Short: "Lint selected projects (read-only)"},
	{Name: "format", Short: "Format source files in selected projects"},
	{Name: "clean", Short: "Remove declared outputs from selected projects"},
	{Name: "generate", Short: "Run code generation for selected projects"},
	{Name: "ci", Short: "Run the magusfile's ci target read-only (affected-set anchor)"},
}

var listCommand = cmdline.Command{
	Name:        "ls",
	Short:       "List all discovered projects",
	Description: "List every discovered project in the workspace with its language pack, source files, outputs, dependencies, and tool requirements.",
	Tags:        []string{"cli", "magus ls", "list", "projects", "discovery", "workspace"},
	Long: `Print every discovered project in the workspace along with its language
pack, source files, outputs, dependencies, and tool requirements.

An optional noun narrows the listing, singular or plural: "magus ls targets"
lists what the selected projects can run, with the doc or spell ops behind each,
and takes project paths after the noun. The noun defaults to projects, so a bare
"magus ls" keeps its meaning.

Output defaults to a human-readable text format. Use the global -o flag with
json or yaml for structured output suitable for scripting. -o name prints one
project path per line. -o template accepts a Go text/template evaluated
against the value -o json emits, so its field names are the json keys.`,
	Usage: "magus ls [<noun>] [project...] [flags]",
	// The noun is routed positionally by lsCmd (cmd/magus/list.go), whose lsNouns
	// accept-list these mirror.
	Children: []cmdline.Command{
		{Name: "targets", Short: "List what the selected projects can run, with the doc or spell ops behind each"},
		{Name: "target", Short: "The same listing; the noun takes either spelling"},
		{Name: "jobs", Short: "List every job this repository carries, whoever holds it, with the pairs that claim the same path"},
		{Name: "job", Short: "The same listing; the noun takes either spelling"},
	},
	Examples: []cmdline.Example{
		{Comment: "List all projects", Command: "magus ls"},
		{Comment: "What the cwd project can run", Command: "magus ls targets"},
		{Comment: "What one project can run", Command: "magus ls targets libs/foo"},
		{Comment: "Pipe-friendly: one path per line", Command: "magus ls -o name"},
		{Comment: "JSON output", Command: "magus ls -o json"},
		{Comment: "Custom Go template", Command: `magus ls -o template='{{range .projects}}{{.path}}{{"\n"}}{{end}}'`},
	},
}

var describeCommand = cmdline.Command{
	Name:        "describe",
	Short:       "Define a magus concept and list its entities",
	Description: "Define a magus concept (spell, charm, target, project, workspace, module, mcp-tool, tool, file, graph, harness) and list every entity of that kind, or detail one when a name is given.",
	Tags:        []string{"cli", "magus describe", "spell", "charm", "target", "project", "workspace", "module", "mcp-tool", "tool", "file", "graph", "harness", "introspection"},
	Long: `Define a magus concept and list every entity of that kind. The noun is
one of spell, charm, target, project, workspace, module, mcp-tool, tool, file,
graph, or harness; singular and plural are interchangeable. Pass a name after the noun
to detail a single entity instead of listing them all. (The knowledge graph
lives under magus graph: export for the merged graph, stats for its shape.)

The charm noun is the inverse of a target ref: "describe charm rw" lists every
target that declares the rw charm and the argv edit each one makes, the transpose
of the charms a single "describe target" lists.

For a target ref (e.g. "api:build", or ":test" for all projects) magus prints the
fully-evaluated dispatch plan: the workspace-rooted source and output globs, the
spells that fire, the charm-applied command, and any per-target policy. A target
that composes others also prints a "chain" line naming them in invocation order
(e.g. "generate -> lint -> build -> test"), read from the ctx.needs calls in the
magusfile; a cross-project step reads as "project:target". It lists DIRECT steps
only, so a step that itself composes is described by its own ref. Add a charm
and --explain (e.g. "lint:rw --explain") to see each charm reshape the command one
step at a time.`,
	Usage: "magus describe <noun> [<name>] [flags]",
	// The nouns are separate commands, each with its own flags. They were one
	// merged list on the parent, which is why -e had to be documented as "short
	// for --explain on a target ref, and for --evaluated on a project listing":
	// two different flags sharing a spelling because there was one namespace for
	// all the nouns. Split, each -e is an ordinary alias of the flag beside it.
	Children: []cmdline.Command{
		{Name: "targets", Short: "List every target the workspace defines"},
		{
			Name:        "job",
			Short:       "Print one job's terms: its criteria, paths, check and dependencies",
			Description: "Print what one job grants its holder: the criteria, the write and read paths, the check, its goals, its dependencies, and the paths out of reach. It adds each write path's blast radius and each goal's grade against current evidence.",
			Long: `Print one job's terms, which is what a holder reads on arrival.

It carries no procedure. Taking a job is ` + "`magus job exec`" + `'s work to DO, and a
block of "run this, then this" is a procedure a reader can skip, mistype or
half-follow.

It also carries what the WORKSPACE knows and the job's author may not have
written down: the projects the write paths reach, the declared output globs that
land inside them, the paths another live job is holding, the build inputs and
workspace config that have one owner, and the projects that change alongside the
held ones without declaring a dependency. It is context and never a status,
with one refusal: a job whose check is the ci
gate, or a target that chains to it, has no terms printed at all. The gate runs
once, in the forking session's tree, after every job lands.

It also grades where each declared goal stands against the evidence magus holds
right now, which is still a READ: it records nothing, so asking never advances a
job and never blocks the holder still working on it. It is the same grading
` + "`magus job wait`" + ` does, so the two cannot disagree.`,
			Usage: "magus describe job <job> [flags]",
		},
		{
			Name:  "target",
			Short: "Detail one target ref: its dispatch plan, globs, spells and policy",
			Flags: []cmdline.Flag{
				{Name: "explain", Kind: cmdline.Bool, Doc: "For a ref with charms: show the per-charm argv trace (base then each charm)"},
				{Name: "e", Kind: cmdline.Bool, AliasOf: "explain", Doc: "Short for --explain"},
				{Name: "cache", Kind: cmdline.Bool, Doc: "Show the live cache key, the ref a run would print, the component classes behind it, and what moved since the last recorded run"},
				{Name: "against", Kind: cmdline.String, Doc: "With --cache: diff the live key inputs against the stored lines behind an output `ref`"},
				{Name: "inputs", Kind: cmdline.Bool, Doc: "With --cache: list every key input line, so you can confirm a declared file was actually hashed"},
				{Name: "no-default-charms", Kind: cmdline.Bool, Doc: "With --cache: ignore magus.yaml default_charms when keying, matching a run made the same way (CI)"},
			},
		},
		{
			Name:  "projects",
			Short: "List the workspace's projects",
			Flags: []cmdline.Flag{
				{Name: "evaluated", Kind: cmdline.Bool, Doc: "Print workspace-rooted globs, effective claims, and per-target policies"},
				{Name: "e", Kind: cmdline.Bool, AliasOf: "evaluated", Doc: "Short for --evaluated"},
			},
		},
		// The singular, as with target/targets above: a trailing name switches the
		// listing into one project's record, which is the form other commands point at.
		// One noun, one flag set, so the flags stay declared on the plural rather than
		// generating a second binder and a second identical options section.
		{Name: "project", Short: "Detail one named project; takes the same flags as projects"},
		{
			Name:  "spells",
			Short: "List the spells the workspace resolves",
			Flags: []cmdline.Flag{
				// Bound by the command and declared nowhere until now, so no man page
				// listed it.
				{Name: "versions", Kind: cmdline.Bool, Doc: "Probe each spell's tools and report the versions that would key a run"},
			},
		},
		{Name: "charms", Short: "List charms and the targets that declare them"},
		{Name: "workspaces", Short: "List the workspaces registered in config"},
		{Name: "modules", Short: "List the Buzz host modules a magusfile can import"},
		{Name: "mcp-tools", Short: "List the tools the MCP server exposes"},
		{Name: "tools", Short: "List the external tools the workspace's spells require"},
		{Name: "file", Short: "Classify paths as generated output, declared source, maintained, or unclaimed"},
		{Name: "graph", Short: "Emit the target catalog and dependency graph"},
		{Name: "rules", Short: "List the guard rules this workspace enforces, and what each one catches"},
		{Name: "rule", Short: "Detail one guard rule, by the name a verdict reported"},
		{
			Name:  "harness",
			Short: "Print the host config a wired harness needs and the command you run to merge it",
			Description: "Print each entry a wired harness's host files lack, and the one command that merges them. " +
				"magus never writes host config; -o json prints the exact fragments that command reads back.",
			Long: `Print what the host files of a harness wired with magus\harness.provider(<spell>)
need: each entry to add, each hook entry replaced or retired because the descriptor
changed it, each key to set, and each whole file to write. Then it prints the one
command that merges them, which you read and run yourself. magus never writes host
config: the hook wiring is what lets the guard see an agent at all, so a change to
it is one a person makes with their own hands.

-o json prints the plan the command reads back: files keyed by path, each with the
exact fragment to merge (objects merge key by key, and an array arrives whole, with
your own entries kept in place) or the whole content to write. The command pipes
that plan into ` + "`magus buzz`" + `, which merges each file with merge\json. Omit the
id for every wired harness.`,
		},
	},
	Examples: []cmdline.Example{
		{Comment: "List every target", Command: "magus describe targets"},
		{Comment: "Print what every wired harness needs merged", Command: "magus describe harness"},
		{Comment: "One harness's exact fragments, for review or your own merge", Command: "magus describe harness <id> -o json"},
		{Comment: "List what the guard enforces", Command: "magus describe rules"},
		{Comment: "Look up the rule a verdict named", Command: "magus describe rule stage-all"},
		{Comment: "List a charm's declaring targets", Command: "magus describe charm rw"},
		{Comment: "Detail one project", Command: "magus describe project api"},
		{Comment: "Preview a charm-applied command", Command: "magus describe target lint:rw"},
		{Comment: "Trace how each charm reshapes the command", Command: "magus describe target --explain lint:rw,debug"},
	},
}

// detachDoc is --detach on run and affected, which detach the same way.
const detachDoc = "Run in the background as its own session, writing to a log under $XDG_STATE_HOME/magus/detached/; prints the pid and the log path and returns. Needs no server"

var runCommand = cmdline.Command{
	Name:        "run",
	Short:       "Run a target for selected projects",
	Description: "Run a named target (build, test, lint, format, ci, etc.) for the selected projects, defaulting to the cwd project when no arguments are given.",
	Tags:        []string{"cli", "magus run", "run", "target", "build", "test", "ci"},
	Long: `Run a named target for the selected projects. With no project
arguments, selects the project containing the current directory, or all projects
if the current directory is not inside a project. Explicit project paths on the
command line select exactly those projects.

--skip subtracts from whatever that selection resolved to, so a CI step can gate
every project except a named few without any shell filtering. It takes the same
project reference a positional does, and refuses a reference no project matches
rather than skipping nothing quietly.

The target ci is an ordinary magusfile-defined target - magus does not hardcode
its steps; your magusfile composes them with magus.needs. magus keeps ci as
the anchor that the affected set keys off, and always runs it read-only; apply
the rw charm (e.g. 'magus run format:rw') to mutate files.

--stdin runs a saved shard plan instead of a selection: the document magus
affected <target> --plan printed, piped in or redirected from a file (< plan.json).
The plan names the target and each shard's projects, so the target positional is
optional (give it to add charms, as in ci:gha) and project positionals are
refused. --shard <id> runs that one shard; without it every shard runs here. A
malformed plan, a shard id the plan does not have, a target other than the
plan's, or an --n-shards other than its count is refused before anything runs
(MGS3029). Under the global --dry-run nothing runs: the plan is checked and
printed, and -o json, yaml or template renders the document as read, so a saved
plan renders more than once without being computed again.`,
	Usage: "magus run <target> [flags] [project...] | magus run [<target>] --stdin [--shard <id>] < plan.json",
	Flags: []cmdline.Flag{
		{Name: "graph", Kind: cmdline.Bool, Doc: "Render the dependency graph for the selected scope instead of executing"},
		{Name: "upstream", Kind: cmdline.Bool, Doc: "With --graph: show dependents instead of dependencies"},
		{Name: "depth", Kind: cmdline.Int, Doc: "With --graph: cap displayed depth (0 = unlimited)"},
		{Name: "skip", Kind: cmdline.Custom, Doc: "Exclude projects from the selection; repeatable or comma-separated. Takes project references like positionals, or a doublestar glob over project paths (libs/*); a value matching nothing is an error"},
		{Name: "no-cache", Kind: cmdline.Bool, Doc: "Force a fresh run even on a cache hit; still refreshes the entry"},
		{Name: "no-default-charms", Kind: cmdline.Bool, Doc: "Ignore magus.yaml default_charms for this run"},
		{Name: "detach", Kind: cmdline.Bool, Doc: detachDoc},
		{Name: "open", Kind: cmdline.Bool, Doc: "Open this run in the browser log viewer and stream to it as it goes (loopback; never leaves your machine)"},
		{Name: "step", Kind: cmdline.Bool, Doc: "Pause before each subprocess for interactive stepping (needs a TTY; implies --concurrency=1)"},
		{Name: "race", Kind: cmdline.String, Doc: "Race diagnostics: watch, replay, or watch,replay. watch flags a write two projects both claim (MGS4001, 4002, 4004). replay reruns cacheable projects in sequence and compares output hashes for non-determinism (MGS4003), at double the time."},
		{Name: "timeout", Kind: cmdline.Duration, Doc: "Abort if the run has not finished within this duration (e.g. 5m, 1h30m)"},
		{Name: "stdin", Kind: cmdline.Bool, Doc: "Run the shards of a saved shard plan, the document affected --plan prints, read from stdin; the plan names the target and the projects"},
		// A STRING, not an int: the shard id is passed through as a label (it reaches
		// RecordShardTotal as text and defaults from MAGUS_SHARD), and the man page
		// documented it as an int for as long as the two lists were written apart.
		// The drift test compared names only, so a type could disagree indefinitely.
		{Name: "shard", Kind: cmdline.String, Doc: "With --stdin: run only the saved plan's shard with this id. Without it: a label naming this run's shard in a CI matrix, paired with --n-shards; it selects nothing"},
		{Name: "n-shards", Kind: cmdline.Int, Doc: "Without --stdin: the shard count the --shard label belongs to. With it the count is the saved plan's, and a different value is refused"},
		{Name: "no-volatility-retry", Kind: cmdline.Bool, Doc: "Disable volatility auto-retry for this run"},
		{Name: "no-redundancy-check", Kind: cmdline.Bool, Doc: "Run the full ci gate: no deferral and no tier reduction (MGS3010); ci target only"},
	},
	Operands: commonTargets,
	Examples: []cmdline.Example{
		{Comment: "Build everything", Command: "magus run build"},
		{Comment: "Test one project", Command: "magus run test api/gateway"},
		{Comment: "Build two specific projects", Command: "magus run build api/gateway web/studio"},
		{Comment: "Every project that declares generate except two", Command: "magus run generate --skip docs --skip console"},
		{Comment: "Dry-run: show what would run", Command: "magus run build --dry-run"},
		{Comment: "Force a fresh rebuild past a cache hit", Command: "magus run build --no-cache"},
		{Comment: "Full CI pipeline", Command: "magus run ci"},
		{Comment: "Show dependency graph for build target", Command: "magus run build --graph"},
		{Comment: "Graph in DOT format", Command: "magus run build --graph -o dot"},
		{Comment: "Graph dependents of api/gateway", Command: "magus run build api/gateway --graph --upstream"},
		{Comment: "Stream JSONL target events to a file", Command: "magus run build -o jsonl --tee build.jsonl"},
		{Comment: "Run every shard of the affected ci plan here", Command: "magus affected ci --plan | magus run --stdin"},
		{Comment: "Run one shard of a saved plan", Command: "magus run ci:gha --stdin --shard 2 < plan.json"},
		{Comment: "Render a saved plan's summary without computing it again", Command: "magus run --stdin --dry-run -o 'template={{.summary}}' < plan.json"},
	},
	ExitStatus: []cmdline.ExitCode{
		{Code: 0, Meaning: "Every selected project's target succeeded, whether it ran or replayed from cache."},
		{Code: 1, Meaning: "At least one target failed. The failure was already reported with the path to its captured log, so there is no second error line here. This is the default failure status, not the only one: a magusfile calling os.exit(code) has that code honored verbatim, so a target may exit with a status this list does not name."},
		{Code: 2, Meaning: "Misuse: an unknown target, no project matched the filters, a flag that does not apply to this invocation, or a saved plan that cannot be run as asked (MGS3029)."},
		{Code: 75, Meaning: "Nothing ran, and trying again later would succeed; 75 is EX_TEMPFAIL, the transient-failure convention. A selected project's workspace lock or the machine's build budget was held by another magus invocation (magus never queues behind one; the error names the holder's pid, command and directory), or a ci gate was deferred as redundant under load (MGS3010; the error names the green gate it found and --no-redundancy-check overrides)."},
	},
}

var whereCommand = cmdline.Command{
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
	Usage: "magus where [flags] [filter...]",
	Flags: []cmdline.Flag{
		{Name: "all", Kind: cmdline.Bool, Doc: "Print all matching paths to stdout; do not error on ambiguity"},
		{Name: "A", Kind: cmdline.Bool, AliasOf: "all", Doc: "Short for --all"},
		{Name: "filter", Kind: cmdline.String, Doc: "Restrict file search by pattern. Form: type=<glob|regex|literal>,pattern=<value>"},
		{Name: "glob", Kind: cmdline.String, Doc: "Restrict file search to paths matching a doublestar glob (shorthand for --filter type=glob,...)"},
		{Name: "regex", Kind: cmdline.String, Doc: "Restrict file search to paths matching a Go regexp (shorthand for --filter type=regex,...)"},
		{Name: "literal", Kind: cmdline.String, Doc: "Restrict file search to paths containing this exact segment (shorthand for --filter type=literal,...)"},
	},
	Examples: []cmdline.Example{
		{Comment: "Navigate to a project", Command: `cd "$(magus where api)"`},
		{Comment: "Open in editor", Command: `code "$(magus where dash)"`},
		{Comment: "AND-filter: must match both tokens", Command: "magus where api gateway"},
	},
}

var xCommand = cmdline.Command{
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
project (persisted one file per project under $XDG_STATE_HOME/magus/x/,
defaulting to $HOME/.local/state/magus/) is pre-highlighted.

x refuses to run when stdin or stderr is not a terminal: shorthand is for
humans. Scripts should call magus run directly.`,
	Usage: "magus x <ref> | magus x [filter...]",
	Examples: []cmdline.Example{
		{Comment: "Reproduce the invocation an output ref recorded", Command: "magus x out1a2b3c"},
		{Comment: "Browse all projects in a picker", Command: "magus x"},
		{Comment: "Resolve by leaf substring", Command: "magus x dash"},
		{Comment: "AND-narrow with a second filter", Command: "magus x dash mobile"},
	},
}

var affectedCommand = cmdline.Command{
	Name:        "affected",
	Short:       "Run a target for VCS-diff affected projects",
	Description: "Run a target for every project affected by a VCS diff, with forensic modes for explain, graph, CI shard plan, and regression bisect.",
	Tags:        []string{"cli", "magus affected", "affected", "changed files", "vcs", "git", "bisect", "ci"},
	Long: `Run a named target for every project that is affected by changes in
version control. The active VCS adapter is picked by autodetect from .git, .hg,
or .jj at the workspace root, or pinned with MAGUS_VCS_NAME / vcs.name. When
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
history to find the commit that introduced a regression.

magus affected ci sizes its gate to the change. Each changed path gets a risk
tier (trivial, mechanical, scoped, full) and the change takes the highest. Below
full, the gate runs only what the tier needs (the drift check and lint, the
targets that declare a changed doc, or the project's test with go-test narrowed
to the packages that can observe the change) and prints every path
with its tier to stderr; a trivial change runs nothing and exits 0. With --plan,
a trivial change emits an empty matrix beside a risk block.
--no-redundancy-check runs the full gate.

--plan downstream of magus in a pipe waits for every magus stage before it to
end, and prints no plan if one failed (MGS3030). CI gates its shards on drift
that way: magus affected generate --no-default-charms | magus affected ci --plan.`,
	Usage: "magus affected <target> [flags]",
	Flags: []cmdline.Flag{
		{Name: "impact", Kind: cmdline.Bool, Modes: []string{"impact"}, Doc: "Report the blast radius of the changeset (read-only; runs nothing)"},
		{Name: "base", Kind: cmdline.String, Modes: []string{"", "plan", "impact"}, Doc: "Override base ref for the VCS diff (default: MAGUS_VCS_BASE_REF or per-VCS built-in)"},
		{Name: "stdin", Kind: cmdline.Bool, Modes: []string{"", "plan"}, Doc: "Read changed file paths from stdin instead of running a VCS diff"},
		{Name: "null", Kind: cmdline.Bool, Modes: []string{"", "plan"}, Doc: "With --stdin: expect NUL-separated paths and double-NUL between batches"},
		{Name: "b", Kind: cmdline.String, AliasOf: "base", Modes: []string{"", "plan", "impact"}, Doc: "Short for --base"},
		{Name: "no-cache", Kind: cmdline.Bool, Doc: "Force a fresh run even on a cache hit; still refreshes the entry"},
		{Name: "no-default-charms", Kind: cmdline.Bool, Doc: "Ignore magus.yaml default_charms for this run"},
		{Name: "no-redundancy-check", Kind: cmdline.Bool, Doc: "Run the full ci gate: no deferral and no tier reduction (MGS3010); ci target only"},
		{Name: "detach", Kind: cmdline.Bool, Doc: detachDoc},
		{Name: "open", Kind: cmdline.Bool, Doc: "Open this run in the browser log viewer and stream to it as it goes (loopback; never leaves your machine)"},
		{Name: "step", Kind: cmdline.Bool, Doc: "Pause before each subprocess for interactive stepping (needs a TTY; implies --concurrency=1)"},
		{Name: "race", Kind: cmdline.String, Doc: "Race diagnostics: watch, replay, or watch,replay. watch flags a write two projects both claim (MGS4001, 4002, 4004). replay reruns cacheable projects in sequence and compares output hashes for non-determinism (MGS4003), at double the time."},
		{Name: "timeout", Kind: cmdline.Duration, Doc: "Abort if the run has not finished within this duration (e.g. 5m, 1h30m)"},
		{Name: "graph", Kind: cmdline.Bool, Doc: "Render the dependency graph for the affected scope instead of executing"},
		{Name: "upstream", Kind: cmdline.Bool, Doc: "With --graph: show dependents instead of dependencies"},
		{Name: "depth", Kind: cmdline.Int, Doc: "With --graph: cap displayed depth (0 = unlimited)"},
		{Name: "explain", Kind: cmdline.Custom, Doc: "Show why <project> is in the affected set instead of executing"},
		{Name: "plan", Kind: cmdline.Custom, Doc: "Emit a provider-neutral JSON CI shard plan for the affected set"},
		{Name: "max-shards", Kind: cmdline.Int, Default: 8, DefaultAtBind: true, Modes: []string{"plan"}, Doc: "With --plan: maximum CI shards (-1 = unlimited)"},
		{Name: "max-parallel-budget", Kind: cmdline.Int, DefaultAtBind: true, Modes: []string{"plan"}, Doc: "With --plan: cross-shard concurrency cap; 0 = unlimited"},
		{Name: "detail", Kind: cmdline.Bool, Modes: []string{"plan"}, Doc: "With --plan: add per-shard detail: the invocation, its spells, the files it declares it writes, and the skills its work routes to"},
		{Name: "bisect", Kind: cmdline.String, Modes: []string{"bisect"}, Doc: "Drive VCS bisect to find the commit that broke <project>"},
		{Name: "good", Kind: cmdline.String, Modes: []string{"bisect"}, Doc: "With --bisect: known-good commit SHA (auto-detected from history when empty)"},
		{Name: "target", Kind: cmdline.String, Default: "test", Modes: []string{"bisect"}, Doc: "With --bisect: magus target to bisect"},
	},
	Operands: commonTargets,
	Examples: []cmdline.Example{
		{Comment: "Build projects changed since the default base ref", Command: "magus affected build"},
		{Comment: "Use a different base ref", Command: "magus affected build --base main"},
		{Comment: "Pipe from watch for continuous builds", Command: "magus watch | magus affected --stdin build"},
		{Comment: "List affected projects without building", Command: "magus affected list"},
		{Comment: "Show dependency graph for the affected scope", Command: "magus affected build --graph"},
		{Comment: "Graph as DOT for piping to Graphviz", Command: "magus affected build --graph -o dot | dot -Tsvg > graph.svg"},
		{Comment: "Emit a CI shard plan for the affected set", Command: "magus affected ci --plan"},
		{Comment: "Gate a CI shard plan on drift: no plan, and no shards, unless generate passes", Command: "magus affected generate --no-default-charms | magus affected ci --plan"},
		{Comment: "Shard a test plan across at most four workers", Command: "magus affected test --plan --max-shards 4"},
		{Comment: "Bisect a regression in myapp", Command: "magus affected --bisect ./apps/myapp"},
	},
	ExitStatus: []cmdline.ExitCode{
		{Code: 0, Meaning: "Every affected project's target succeeded. An empty affected set is also 0: nothing changed is a pass, not a fault, so a CI job gating on this stays green on a docs-only commit."},
		{Code: 1, Meaning: "At least one target failed, already reported with the path to its captured log."},
		{Code: 2, Meaning: "Misuse: no target named, or --step without an interactive terminal."},
		{Code: 75, Meaning: "Nothing ran, and trying again later would succeed; 75 is EX_TEMPFAIL, the transient-failure convention. A selected project's workspace lock or the machine's build budget was held by another magus invocation (magus never queues behind one; the error names the holder's pid, command and directory), or a ci gate was deferred as redundant under load (MGS3010; the error names the green gate it found and --no-redundancy-check overrides)."},
	},
}

var queryCommand = cmdline.Command{
	Name:        "query",
	Short:       "Search the knowledge graph, and retrieve a run's output or journal by id",
	Description: "Resolve search terms to knowledge-graph nodes and return the ranked matches plus their neighborhood; also retrieves a target's captured output by ref and a run's journal by invocation id.",
	Tags:        []string{"cli", "magus query", "query", "knowledge graph", "search", "output", "reference", "invocation", "journal", "audit"},
	Long: `Ask the workspace what it knows. query resolves terms to nodes in the
knowledge graph and returns the ranked matches plus the induced subgraph around
them, collected up to a node budget. Its siblings explain and path read the same
graph: explain shows one node's context, path connects two nodes.

Terms are free text plus field matchers, and they compose: kind=spell, project=pkg/foo,
relation=uses, id=build, exclusion with kind!=op, and a regex with id=~build$. A bare word
matches names and documentation. The : grammar (kind:spell, -kind:op) still parses as a
compat alias.

query is also the retrieval verb for the two ids magus prints, each an EXPLICIT
subcommand rather than a shape-routed positional, so a search term can never collide
with an id:

  output <ref>       One target run's captured output, by the output ref
                     shown when the target ran (out1a2b3c). The default prints the
                     exact bytes, so it pipes anywhere. --identity shows the run's
                     identity instead - descriptor, lineage, cache key, and the
                     digests of the key's component classes, which is the
                     machine-comparable half of a works-on-my-machine report.
                     --attempts lists the ref's stored attempts, --publish uploads
                     the output to the remote cache as a signed bundle, and --open
                     hands the bytes to the browser log viewer in a URL fragment
                     (delivered privately; never uploaded).
  invocation <id>    One run's journal, by the invocation id shown as inv: in
                     query output <ref> --identity. --secrets narrows it to the
                     credential reads - which references the run reached for and
                     through which provider, never the value - which is how an audit
                     answers "what did this run touch". Run logs are trimmed to a cap
                     by the server's RotateLogs job, so this answers for recent runs
                     rather than forever.

The graph is cache-backed under <cache>/knowledge and only shards whose sources
changed are rebuilt, so a query is cheap to repeat; --refresh forces a full rebuild.`,
	Usage: "magus query <terms> [flags]",
	Flags: []cmdline.Flag{
		{Name: "budget", Kind: cmdline.Int, Doc: "Max nodes in the returned neighborhood (default 50)"},
		{Name: "kind", Kind: cmdline.String, Doc: "Restrict matches to these node kinds (comma-separated)"},
		{Name: "refresh", Kind: cmdline.Bool, Doc: "Force a full graph rebuild before querying"},
		{Name: "global", Kind: cmdline.Bool, Doc: "Query across the workspaces registered in config (knowledge.workspaces); IDs are namespaced by workspace"},
		{Name: "identity", Kind: cmdline.Bool, Doc: "output <ref>: show the run's identity (descriptor, lineage, cache key, component digests)"},
		{Name: "attempts", Kind: cmdline.Bool, Doc: "output <ref>: list the ref's stored attempts (newest first)"},
		{Name: "publish", Kind: cmdline.Bool, Doc: "output <ref>: upload this run's output to the remote cache as a signed bundle"},
		{Name: "stdin", Kind: cmdline.Bool, Doc: "output: print records `-o jsonl` wrote, from stdin; writes nothing"},
		{Name: "open", Kind: cmdline.Bool, Doc: "output <ref>: open the captured output in the browser log viewer (delivered privately)"},
		{Name: "print", Kind: cmdline.Bool, Doc: "With --open, print the viewer URL instead of launching a browser"},
		{Name: "url", Kind: cmdline.String, Default: "https://eli.gladman.cc/magus/console/logs/", DefaultAtBind: true, Doc: "With --open, base URL of the log viewer page (override for a self-hosted mirror)"},
		{Name: "secrets", Kind: cmdline.Bool, Doc: "invocation <id>: list only the credential reads (reference and provider, never the value)"},
	},
	Children: []cmdline.Command{
		{Name: "output", Short: "Retrieve one target run's captured output by output ref"},
		{Name: "invocation", Short: "Read one run's journal by invocation id (--secrets for the credential reads)"},
	},
	Examples: []cmdline.Example{
		{Comment: "Find a spell by name", Command: "magus query kind=spell go"},
		{Comment: "What uses this target", Command: "magus query relation=uses id=build"},
		{Comment: "Everything but ops", Command: "magus query docker kind!=op"},
		{Comment: "Print a run's captured output", Command: "magus query output out1a2b3c"},
		{Comment: "Compare a run's cache key", Command: "magus query output out1a2b3c --identity"},
		{Comment: "Audit a run's credential reads", Command: "magus query invocation invmsm3vcou1 --secrets"},
	},
}

var explainCommand = cmdline.Command{
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
	Flags: []cmdline.Flag{
		{Name: "refresh", Kind: cmdline.Bool, Doc: "Force a full graph rebuild before explaining"},
		{Name: "global", Kind: cmdline.Bool, Doc: "Resolve across the workspaces registered in config (knowledge.workspaces)"},
	},
	Examples: []cmdline.Example{
		{Comment: "A target in full", Command: "magus explain target:pkg/api:build"},
		{Comment: "Resolve by bare name", Command: "magus explain build"},
		{Comment: "A spell's context", Command: "magus explain spell:go"},
		{Comment: "As a record", Command: "magus explain build -o json"},
	},
}

var pathCommand = cmdline.Command{
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
	Flags: []cmdline.Flag{
		{Name: "refresh", Kind: cmdline.Bool, Doc: "Force a full graph rebuild before pathfinding"},
		{Name: "global", Kind: cmdline.Bool, Doc: "Resolve endpoints across the workspaces registered in config (knowledge.workspaces)"},
	},
	Examples: []cmdline.Example{
		{Comment: "How are two projects related", Command: "magus path pkg/api pkg/web"},
		{Comment: "From a spell to a diagnostic", Command: "magus path spell:go MGS3003"},
		{Comment: "Between two targets by ID", Command: "magus path target:pkg/api:build target:pkg/web:test"},
		{Comment: "As a record", Command: "magus path pkg/api pkg/web -o json"},
	},
}

var graphCommand = cmdline.Command{
	Name:        "graph",
	Short:       "The workspace's graphs as objects: deps, export, stats",
	Description: "Emit the project dependency DAG, export the knowledge graph for external graph tools, and report the graph's shape (god nodes, orphans, doc coverage).",
	Tags:        []string{"cli", "magus graph", "graph", "knowledge graph", "dependency graph", "export", "graphml"},
	Long: `The workspace's graphs as objects: emit, export, and measure them. The
query, explain, and path verbs read the knowledge graph; magus graph is the
home of the graph itself.

Subcommands (the first argument):

  build    Rebuild the knowledge graph now, reindexing code symbols first (runs
           each symbol-capable project's scip op) unless --no-symbols, then
           running each adapter declared in knowledge.sessions unless
           --no-sessions. The server does this automatically in the background;
           this is the manual trigger, after a branch switch or when the server
           is not running.
  deps     The project dependency DAG. A trailing list of project paths roots
           the graph; -o selects text, json, yaml, dot, or tree. The
           same view scoped to a run is available as magus run <target> --graph
           and magus affected <target> --graph.
  export   The merged knowledge graph: the deterministic, cache-backed graph of
           the magus domain (projects, targets, spells, ops, charms, modules,
           methods, diagnostics, docs, buzz sources). -o json emits the
           node-link form; -o graphml emits GraphML. External graph viewers
           (Gephi, yEd) read both directly. --select "<terms>" narrows the
           export to a query's neighborhood (same engine as magus query); -o dot
           renders only with --select, since the full graph has too many nodes
           to lay out. The graph is cache-backed under
           <cache>/knowledge; only shards whose sources changed are rebuilt.
           --open sends it to the hosted, interactive Graph Explorer instead of
           stdout (there is no separate "open" subcommand): by default the graph
           rides in the URL fragment (#data=...), which browsers never send to a
           server; --serve instead hands it from an ephemeral 127.0.0.1 loopback
           server (no size limit), and --targets opens the target dependency
           graph instead of the knowledge graph.
  stats    The graph's shape: god nodes (the most connected spells, modules,
           targets - where structural risk concentrates), orphans (docs that
           document nothing, spells no target uses), and doc coverage (the
           share of diagnostics, spells, and modules with a doc). --kind scopes
           every section to one node kind. The VCS-history lenses (hotspots,
           affinity, ownership, trend, unreferenced) are a separate view, served
           by the client MCP tool (magus\insight) and the console's Insight page - not by
           this command.
  diff     Nodes and edges added, removed, or changed relative to a baseline
           export or a git revision (--rev): the PR-review blast-radius
           artifact, emit as json or markdown for a CI comment.`,
	Usage: "magus graph <build|push|pull|deps|export|stats|diff> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "build",
			Short: "Rebuild the knowledge graph now, reindexing code symbols first",
			Flags: []cmdline.Flag{
				{Name: "no-sessions", Kind: cmdline.Bool, Doc: "Do not run the declared agent-session adapters first"},
				{Name: "no-symbols", Kind: cmdline.Bool, Doc: "Rebuild the domain graph only; do not reindex code symbols"},
			},
		},
		{
			Name:  "push",
			Short: "Push the knowledge graph to a container registry as an OCI artifact",
			Flags: []cmdline.Flag{
				{Name: "ref", Kind: cmdline.String, Doc: "The artifact to push to, as <registry>/<repository>:<tag> (required; never derived); with --table, a <registry>/<repository> defaulting to ghcr.io/egladman/magus-embed"},
				{Name: "username", Kind: cmdline.String, Doc: "The registry username; the token is read from stdin, the way docker login --password-stdin takes one"},
				{Name: "refresh", Kind: cmdline.Bool, Doc: "Rebuild the graph before pushing instead of exporting what is cached"},
				{Name: "table", Kind: cmdline.Bool, Doc: "Publish the embedding table from the cache instead of the graph, tagged with its pin, write-once"},
			},
		},
		{
			Name:  "pull",
			Short: "Fetch a published knowledge graph; reads public artifacts with no credentials",
			Flags: []cmdline.Flag{
				{Name: "ref", Kind: cmdline.String, Doc: "The artifact to pull (default: derived from the repository's origin remote); with --table, a <registry>/<repository> defaulting to ghcr.io/egladman/magus-embed"},
				{Name: "out", Kind: cmdline.String, Doc: "Write the graph here instead of stdout"},
				{Name: "table", Kind: cmdline.Bool, Doc: "Fetch the embedding table the binary pins into the cache instead of the graph"},
			},
		},
		{Name: "deps", Short: "Emit the project dependency DAG (text, json, yaml, dot, tree)", Flags: []cmdline.Flag{
			{Name: "upstream", Kind: cmdline.Bool, Doc: "Show dependents instead of dependencies"},
			{Name: "depth", Kind: cmdline.Int, Doc: "Cap displayed depth (0 = unlimited)"},
			{Name: "spell", Kind: cmdline.String, Doc: "Only projects driven by this spell"},
			{Name: "target", Kind: cmdline.String, Doc: "Target whose duration history annotates nodes (default: build)"},
		}},
		{Name: "export", Short: "Export the merged knowledge graph (json node-link or graphml)", Flags: []cmdline.Flag{
			{Name: "refresh", Kind: cmdline.Bool, Doc: "Force a full graph rebuild before exporting"},
			{Name: "global", Kind: cmdline.Bool, Doc: "Union the workspaces registered in config (knowledge.workspaces); node IDs are namespaced by workspace"},
			{Name: "reproducible", Kind: cmdline.Bool, Doc: "Omit everything that is not a function of the source tree (locally observed runtime attrs, git history), so two checkouts of one commit export identical bytes"},
			{Name: "open", Kind: cmdline.Bool, Doc: "Deliver the graph to the hosted Graph Explorer instead of stdout; it never leaves your machine"},
			{Name: "follow", Kind: cmdline.Bool, Doc: "With --open: keep the explorer updating from the running server instead of showing a snapshot (needs magus server start)"},
			{Name: "targets", Kind: cmdline.Bool, Doc: "With --open: open the target dependency graph instead of the knowledge graph; pass a project path to scope it"},
			{Name: "serve", Kind: cmdline.Bool, Doc: "With --open: hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; incompatible with --targets)"},
			{Name: "print", Kind: cmdline.Bool, Doc: "With --open: print the explorer URL to stdout instead of launching a browser"},
			{Name: "url", Kind: cmdline.String, Default: "https://eli.gladman.cc/magus/console/graph/", DefaultAtBind: true, Doc: "With --open: base URL of the Graph Explorer page (override for a self-hosted mirror)"},
			{Name: "static", Kind: cmdline.Bool, Doc: "Deprecated alias for --reproducible"},
			{Name: "select", Kind: cmdline.String, Doc: "Export only the neighborhood of a query (same grammar as magus query); required for -o dot"},
			{Name: "budget", Kind: cmdline.Int, Default: 50, DefaultAtBind: true, Doc: "Node budget for --select (how many nodes the neighborhood may collect)"},
			{Name: "symbols", Kind: cmdline.Bool, Doc: "Include every indexed code symbol, which the whole-graph export leaves out; a `magus diff --baseline` needs them"},
		}},
		{Name: "stats", Short: "Report the knowledge graph's shape: god nodes, orphans, doc coverage", Flags: []cmdline.Flag{
			{Name: "kind", Kind: cmdline.String, Doc: "Scope every section to one node kind (spell, target, doc, ...)"},
			{Name: "refresh", Kind: cmdline.Bool, Doc: "Force a full graph rebuild first"},
			{Name: "global", Kind: cmdline.Bool, Doc: "Union the workspaces registered in config (knowledge.workspaces) before computing stats"},
			{Name: "symbols", Kind: cmdline.Bool, Doc: "Include the lazily-loaded symbol shards in the stats; excluded by default because they can dwarf the domain graph"},
		}},
		{
			Name:  "diff",
			Short: "Nodes/edges added, removed, or changed vs a baseline export or --rev; PR blast-radius",
			Flags: []cmdline.Flag{
				{Name: "refresh", Kind: cmdline.Bool, Doc: "Force a full graph rebuild of the current graph before diffing"},
				{Name: "global", Kind: cmdline.Bool, Doc: "Diff the global (all-workspaces) graph; match this to how the baseline was exported"},
				{Name: "rev", Kind: cmdline.String, Doc: "Diff against a git revision (e.g. HEAD~1, main) instead of an export file"},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Project DAG as DOT", Command: "magus graph deps -o dot"},
		{Comment: "DAG rooted at one project, dependents up", Command: "magus graph deps pkg/api --upstream"},
		{Comment: "Knowledge graph for an external viewer", Command: "magus graph export -o json > graph.json"},
		{Comment: "GraphML for Gephi or yEd", Command: "magus graph export -o graphml > graph.graphml"},
		{Comment: "A query's neighborhood as DOT", Command: "magus graph export --select 'kind=spell go' -o dot"},
		{Comment: "Where structural risk concentrates", Command: "magus graph stats"},
		{Comment: "Doc coverage for spells only", Command: "magus graph stats --kind spell"},
		{Comment: "Open knowledge graph in browser", Command: "magus graph export --open"},
		{Comment: "Open target dependency graph", Command: "magus graph export --open --targets"},
		{Comment: "Scope target graph to one project", Command: "magus graph export --open --targets docs"},
		{Comment: "Print the URL instead of opening", Command: "magus graph export --open --targets --print"},
	},
}

var spellCommand = cmdline.Command{
	Name:        "spell",
	Short:       "Build, publish, pull and list spells as OCI artifacts, and pin them in magus.lock",
	Description: "Pack a spell directory's tracked files as an OCI artifact with provenance annotations. Push it under one or more tags, pull and verify a published spell, list its tags, and check or rewrite the magus.lock pins of remote spells.",
	Tags:        []string{"cli", "magus spell", "spell", "publish", "pull", "oci", "registry", "digest", "remote spells", "credentials", "magus.lock", "lock", "update"},
	Long: `A spell as an artifact: what one workspace publishes so another imports it
by registry path, pinned by digest in magus.lock and versioned apart from the
magus binary. Authoring a spell is magus init spell.

Subcommands (the first argument):

  build    Pack <dir> exactly as push would and print the manifest digest the
           push would produce, without touching the network. --out writes the
           layer tar too.
  push     Pack <dir> as one uncompressed tar layer and push it to <ref>, a
           <registry>/<repository>:<tag>, then under each --tag with no second
           upload. Prints <registry>/<repository>@sha256:<digest>.
           magus/spell/<name> in place of <dir> pushes a spell magus ships,
           packed from the binary: the digest pull stamps on a copy.
  pull     Fetch <ref> by tag or digest, verify the manifest and layer digests,
           and print the pinned reference and the directory holding the files:
           [<dir>] when given, otherwise the user cache. A bare registry path,
           as a magusfile imports it, pulls the digest magus.lock pins.
           magus/spell/<name> <dir> copies a spell magus ships instead, from
           the binary with no network: the files its published artifact holds,
           spell.buzz opening on a "// magus:origin <reference>" line, and
           prints the magus.yaml override to add.
  ls       List <registry>/<repository>'s tags, following pagination.
           magus/spell lists every spell magus ships instead, each pinned to
           the digest a release publishes, with no network.
  lock     Check that magus.lock pins every remote spell magus.yaml declares,
           for its declared tag, and verify each pinned digest; no tag is
           resolved. --update resolves each tag and rewrites magus.lock, and is
           what the update charm on the lock-owning target runs. The workspace
           is not loaded, so credentials resolve through the environment.

Only files the VCS tracks are packed, each with a fixed mode, owner and time,
and the manifest carries org.opencontainers.image.{title,source,revision,created},
created being the revision's commit time (SOURCE_DATE_EPOCH overrides), so one
commit builds to one digest on every machine. <dir> must hold a tracked
spell.buzz; a tracked symlink is refused.

Credentials: the spells.registries entry in magus.yaml for the reference's host
names a username and a secret reference, resolved through the workspace's
secret provider. --username overrides it and reads the password from stdin.
With neither, requests are anonymous. A new GHCR package is private until
its owner makes it public in the package settings; magus never changes it.`,
	Usage: "magus spell <build|push|pull|ls|lock> [args] [flags]",
	Children: []cmdline.Command{
		{
			Name:  "build",
			Short: "Pack a spell directory and print the manifest digest a push would produce",
			Flags: []cmdline.Flag{
				{Name: "out", Kind: cmdline.String, Doc: "Also write the packed layer (an uncompressed tar) to this file"},
				{Name: "source", Kind: cmdline.String, Doc: "The org.opencontainers.image.source URL; default: the VCS remote as https"},
			},
		},
		{
			Name:  "push",
			Short: "Push a spell directory's tracked files, or a spell magus ships, as an OCI artifact and print its pinned reference",
			Flags: []cmdline.Flag{
				{Name: "username", Kind: cmdline.String, Doc: "The registry username; the password is then read from stdin, overriding spells.registries"},
				{Name: "tag", Kind: cmdline.Custom, Doc: "Another tag to write the same manifest under; repeatable"},
				{Name: "source", Kind: cmdline.String, Doc: "The org.opencontainers.image.source URL; default: the VCS remote as https"},
			},
		},
		{
			Name:  "pull",
			Short: "Fetch and verify a published spell into the cache or a directory, or copy out one magus ships",
			Flags: []cmdline.Flag{
				{Name: "username", Kind: cmdline.String, Doc: "The registry username; the password is then read from stdin, overriding spells.registries"},
			},
		},
		{
			Name:  "ls",
			Short: "List a spell repository's tags, or the spells magus ships",
			Flags: []cmdline.Flag{
				{Name: "username", Kind: cmdline.String, Doc: "The registry username; the password is then read from stdin, overriding spells.registries"},
			},
		},
		{
			Name:  "lock",
			Short: "Check magus.lock against magus.yaml, or rewrite it with --update",
			Flags: []cmdline.Flag{
				{Name: "update", Kind: cmdline.Bool, Doc: "Ask the registry what each declared tag names now, and rewrite magus.lock"},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Print the digest a push of this commit would produce", Command: "magus spell build spells/harness/cursor"},
		{Comment: "Publish under a version and a floating tag", Command: "magus spell push spells/harness/cursor ghcr.io/owner/repo/spells/cursor:v1.2.0 --tag latest"},
		{Comment: "Pull a published spell into a directory", Command: "magus spell pull ghcr.io/owner/repo/spells/cursor:v1.2.0 ./vendor/cursor"},
		{Comment: "List a spell repository's tags", Command: "magus spell ls ghcr.io/owner/repo/spells/cursor"},
		{Comment: "List every spell magus ships with the digest a release publishes", Command: "magus spell ls magus/spell"},
		{Comment: "Pin every declared remote spell's tag in magus.lock", Command: "magus spell lock --update"},
	},
}

var eventsCommand = cmdline.Command{
	Name:        "events",
	Short:       "Stream workspace events as JSONL for an integration to consume",
	Description: "Stream magus events as JSONL, one event per line, so an editor plugin, a status bar, or any other integration can react to runs and results.",
	Tags:        []string{"cli", "magus events", "events", "integration", "editor", "plugin", "jsonl", "subscribe"},
	Long: `Stream workspace events as JSONL, one event per line. This is the stream
third-party integrations build against: an Emacs or Vim plugin, a status bar,
a notifier.

Every magus process in the workspace feeds the stream, so a run started in
another terminal shows up here. It needs no server, no token, and no loadable
magusfile - an editor can attach to a repository whose magusfile is mid-edit.

The stream is outbound only. Nothing a subscriber does can change a magus
verdict; the inbound counterpart is magus shell.

target.output is excluded unless named with --type: it is the one event type
that scales with build size rather than project count. To read a target's full
log, take the ref from its target.result event and pass it to query output.

A subscriber must ignore event types and fields it does not recognize. That is
what lets the taxonomy grow without breaking clients built against an earlier
schema.`,
	Usage: "magus events [flags]",
	Flags: []cmdline.Flag{
		{Name: "follow", Kind: cmdline.Bool, Doc: "Keep streaming as events occur instead of exiting after the replay"},
		{Name: "f", Kind: cmdline.Bool, AliasOf: "follow", Doc: "Short for --follow"},
		{Name: "type", Kind: cmdline.String, Doc: "Restrict to these event types (comma-separated); default is every type except target.output"},
		{Name: "limit", Kind: cmdline.Int, Default: 20, Doc: "Replay this many recent invocations before following; 0 replays nothing (only new events), negative replays every retained run"},
		{Name: "interval", Kind: cmdline.Duration, Default: 250 * time.Millisecond, Doc: "How often --follow polls the run log for new events"},
	},
	Examples: []cmdline.Example{
		{Comment: "Watch a workspace live", Command: "magus events --follow"},
		{Comment: "Only what an editor needs for progress", Command: "magus events --follow --type run.started,target.result"},
		{Comment: "What did the last run do", Command: "magus events --limit 1"},
	},
}

var watchCommand = cmdline.Command{
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
	Flags: []cmdline.Flag{
		{Name: "debounce", Kind: cmdline.Duration, Default: 200 * time.Millisecond, Doc: "Quiet window before emitting a batch"},
		{Name: "initial", Kind: cmdline.Bool, Default: true, Doc: "Emit an --all batch on startup before watching"},
		{Name: "null", Kind: cmdline.Bool, Doc: "NUL-separate paths; double-NUL between batches"},
		{Name: "backend", Kind: cmdline.String, Default: "fsnotify", Doc: "Notification backend: fsnotify, poll, or auto (probes fsnotify, falls back to poll)"},
		{Name: "ignore", Kind: cmdline.Custom, Doc: "Ignore pattern; repeatable. Form: type=<glob|regex|literal>,pattern=<value>"},
	},
	Examples: []cmdline.Example{
		{Comment: "Continuous build pipeline", Command: "magus watch | magus affected --stdin build"},
		{Comment: "Increase debounce for slow editors", Command: "magus watch --debounce 500ms | magus affected --stdin test"},
		{Comment: "Polling backend (when inotify is unavailable)", Command: "magus watch --backend poll | magus affected --stdin build"},
	},
}

var statusCommand = cmdline.Command{
	Name:        "status",
	Short:       "Inspect concurrency pool and configuration",
	Description: "Show effective config plus the live concurrency pool state of any running parent magus process, with optional --watch polling and --compact output.",
	Tags:        []string{"cli", "magus status", "status", "concurrency", "pool", "server", "monitoring"},
	Long: `Show the magus configuration that affects this process - telemetry, cache
settings - and, when a parent magus process is running, the live state of its
concurrency pool (current slot usage, queued waiters).

When --watch is non-zero, status polls and reprints at that interval. On a
TTY the screen is cleared between reprints; piped output appends each
snapshot on its own line for log capture.

--wait prints no snapshot: it exits 0 once this machine's build budget can
seat a run, and it is how a run refused with MGS3009 waits without a retry
loop. Piped into a magus run (magus status --wait | magus run test .) it reads
that run's command from the pipe, waits until the budget can seat the claims
that run will make, and holds the run from starting until it exits; the run
then proceeds as usual, or starts nothing (MGS3030) if the wait failed. On
its own it waits for a free slot. A claim larger than the whole budget exits
78 at once; no broker means nothing to wait for, and it exits 0.`,
	Usage: "magus status [flags]",
	Flags: []cmdline.Flag{
		{Name: "watch", Kind: cmdline.Duration, Doc: "Poll and reprint at this interval (minimum 15s; 0 means one-shot)"},
		{Name: "W", Kind: cmdline.Duration, AliasOf: "watch", Doc: "Short for --watch"},
		{Name: "compact", Kind: cmdline.Bool, Doc: "Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)"},
		{Name: "c", Kind: cmdline.Bool, AliasOf: "compact", Doc: "Short for --compact"},
		{Name: "symbols", Kind: cmdline.Bool, Doc: "Include the expensive symbol-index freshness scan"},
		{Name: "socket", Kind: cmdline.String, Doc: "Proc server to report on, as a unix:// URL or bare path; default: MAGUS_PROC_SOCKET inside a run, else every live one in the socket dir. --probe asks the server at server.address unless this names one"},
		{Name: "probe", Kind: cmdline.String, Doc: "Exec-probe mode: liveness or readiness (exit 0 healthy, 1 unhealthy; ignores --watch/--compact)"},
		{Name: "workspace", Kind: cmdline.String, Doc: "Workspace root to check for readiness with --probe=readiness (default: any loaded workspace)"},
		{Name: "wait", Kind: cmdline.Bool, Doc: "Exit 0 once the build budget can seat the magus run reading this pipe (a free slot when none does); holds that run until then, then reserves the seat for it until its first claim"},
	},
	Examples: []cmdline.Example{
		{Comment: "One-shot status snapshot", Command: "magus status"},
		{Comment: "Run tests once the build budget can seat them", Command: "magus status --wait | magus run test ."},
		{Comment: "Live updates every 15 seconds", Command: "magus status --watch=15s"},
		{Comment: "Single-line snapshot for a multiplexer sidebar", Command: "magus status --compact --watch=15s"},
		{Comment: "Inspect a specific running parent", Command: "magus status --socket=unix:///run/user/1000/magus/server.sock"},
	},
}

var doctorCommand = cmdline.Command{
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
  - Installed agent skills still current with this binary
  - Recognized MAGUS_* environment variables (typo detection)
  - Charm/target name collisions
  - Consistent target naming convention (any casing, but pick one)
  - VCS base-ref reachability

Findings come at two levels. [fail] is a workspace that is wrong regardless
of how you like to work - a dependency graph cycle, an unparsable magusfile,
two targets claiming one output - and exits non-zero. [advice] is a
convention magus recommends, such as target naming or language coverage; it
is reported and exits zero, because ci is the one target name magus reserves
and the rest of the layout is yours. No flag promotes advice to failure.

Every finding is reported under a stable check name (vcs-base-ref,
cacheable-secret-reads). --list prints them all with what each looks at,
without running any, so the name can be looked up rather than provoked.`,
	Usage: "magus doctor [flags]",
	Flags: []cmdline.Flag{
		{Name: "probe", Kind: cmdline.Bool, Doc: "Run each declared tool-readiness probe instead of only listing it (forks a process per gated tool)"},
		{Name: "fix", Kind: cmdline.Bool, Doc: "Run the remedy each finding names, where one exists (see --dry-run to list them first)"},
		{Name: "list", Kind: cmdline.Bool, Doc: "Print every check magus would run (name, subject, and MGS code) without running any of them"},
	},
	Examples: []cmdline.Example{
		{Comment: "Run all checks", Command: "magus doctor"},
		{Comment: "Name every check without running one", Command: "magus doctor --list"},
		{Comment: "The check names alone, for scripting", Command: "magus doctor --list -o name"},
		{Comment: "JSON report", Command: "magus doctor -o json"},
	},
}

var configCommand = cmdline.Command{
	Name:        "config",
	Short:       "View or update magus configuration",
	Description: "Inspect the effective merged configuration or write keys to the local or global magus.yaml, with subcommands for view, set, history, cache, and mcp.",
	Tags:        []string{"cli", "magus config", "configuration", "magus.yaml", "settings", "cache"},
	Long: `Inspect or modify the magus configuration. Configuration is loaded in
priority order: built-in defaults → user-global file → workspace file →
project-local file → MAGUS_* environment variables → CLI flags.

The view sub-command prints the effective merged configuration. The set
sub-command writes a key-value pair to the local (or global) config file.
"magus init" writes a magus.yaml holding no keys, so every key keeps its
built-in default until set. A config file a newer magus wrote is never
replaced: set stops and names the build to update to.

Configuration is stored in magus.yaml (or .magus.yaml). The canonical
locations are the workspace root and $XDG_CONFIG_HOME/magus/.`,
	Usage: "magus config <view|set|history|cache|mcp> [flags]",
	// Nested to match the real tree. These were five flat children with no flags,
	// while the CLI parses four levels deep and binds fourteen flags across six of
	// them, every one of which was documented nowhere, because a child could not
	// carry flags and nothing walked past the first level.
	Children: []cmdline.Command{
		{Name: "view", Short: "Print the effective configuration (defaults + file + env)"},
		{
			Name:  "set",
			Short: "Write a key to the local (or global) config file",
			Flags: []cmdline.Flag{
				{Name: "global", Kind: cmdline.Bool, Doc: "Write to the global config ($XDG_CONFIG_HOME/magus/magus.yaml)"},
			},
		},
		{
			Name:  "history",
			Short: "Manage forecaster runtime history",
			Children: []cmdline.Command{
				{
					Name:  "passed",
					Short: "Record a run outcome in the forecaster history",
					Flags: []cmdline.Flag{
						{Name: "history", Kind: cmdline.String, DefaultAtBind: true, Doc: "Path to the history JSON to write (default: configured history_path)"},
						{Name: "ref", Kind: cmdline.String, Doc: "Ref the run was on (git branch, hg named branch, jj bookmark)"},
						{Name: "commit", Kind: cmdline.String, Doc: "Commit the run was at"},
						{Name: "target", Kind: cmdline.String, Default: "ci", Doc: "Target that ran"},
						{Name: "status", Kind: cmdline.String, Default: "passed", DefaultAtBind: true, Doc: "How the run came out: passed or failed"},
					},
				},
				{
					Name:  "import",
					Short: "Merge another history file into this one",
					Flags: []cmdline.Flag{
						{Name: "history", Kind: cmdline.String, DefaultAtBind: true, Doc: "Path to the history JSON to write (default: configured history_path)"},
					},
				},
				{Name: "dedup", Short: "Collapse duplicate runs in the history file"},
			},
		},
		{
			Name:  "cache",
			Short: "Manage the build cache (prune)",
			Children: []cmdline.Command{
				{
					Name:  "prune",
					Short: "Evict cache entries by age or count",
					Flags: []cmdline.Flag{
						{Name: "older-than", Kind: cmdline.Duration, Doc: "Remove entries older than this duration (e.g. 168h = 7 days)"},
						{Name: "keep-last", Kind: cmdline.Int, Doc: "Keep only the newest N entries, evict the rest (--remote only)"},
						{Name: "remote", Kind: cmdline.Bool, Doc: "Prune the configured remote backend instead of the local cache"},
						{Name: "dry-run", Kind: cmdline.Bool, Doc: "Print what would be removed without deleting anything"},
					},
				},
				{
					Name:  "export",
					Short: "Write cache entries to an archive",
					Flags: []cmdline.Flag{
						{Name: "to", Kind: cmdline.String, Doc: "Write the archive to this file (default: stdout)"},
						{Name: "remote", Kind: cmdline.Bool, Doc: "Sign the caches the workspace's spells declare (go: GOCACHE and GOMODCACHE) and store them in the remote tier, instead of writing the local tier's archive"},
					},
				},
				{
					Name:  "import",
					Short: "Load cache entries from an archive",
					Flags: []cmdline.Flag{
						{Name: "remote", Kind: cmdline.Bool, Doc: "Restore the newest verified bundle of each cache the workspace's spells declare from the remote tier, instead of reading an archive"},
					},
				},
				{Name: "key", Short: "Manage the remote cache signing key"},
			},
		},
		{
			Name:  "mcp",
			Short: "Manage MCP connector tokens",
			Children: []cmdline.Command{
				{
					Name:  "connector",
					Short: "Manage connector tokens (mcp=write)",
					Children: []cmdline.Command{
						{
							Name:  "create",
							Short: "Mint a connector token",
							Flags: []cmdline.Flag{
								{Name: "name", Kind: cmdline.String, Doc: "Name for this connector token (default: connector-N)"},
								{Name: "expires", Kind: cmdline.String, Doc: "Lifetime: a duration like 90d or 48h, at most 366d (default 90d)"},
							},
						},
						{Name: "ls", Short: "List every stored token: names, ids, classes, grants, and expiry (never the secret)"},
						{Name: "revoke", Short: "Revoke a stored token by exact id or exact name"},
					},
				},
			},
		},
		{
			Name:  "token",
			Short: "Manage the operator token (every scope)",
			Children: []cmdline.Command{
				{
					Name:  "generate",
					Short: "Create the operator token",
					Flags: []cmdline.Flag{
						{Name: "force", Kind: cmdline.Bool, Doc: "Overwrite an existing token (rotation)"},
					},
				},
				{Name: "print", Short: "Print the current operator token to stdout (denied to agent sessions)"},
				{Name: "revoke", Short: "Delete the operator token (the server mints a fresh one on next start)"},
				{Name: "status", Short: "Show whether an operator token exists and its fingerprint"},
			},
		},
		{
			Name:  "console",
			Short: "Manage the console (PWA) auth tokens",
			Children: []cmdline.Command{
				{
					Name:  "token",
					Short: "Manage console tokens",
					Children: []cmdline.Command{
						{
							Name:  "create",
							Short: "Mint a console token",
							Flags: []cmdline.Flag{
								{Name: "name", Kind: cmdline.String, Doc: "Name for this console token (default: console-N)"},
								{Name: "expires", Kind: cmdline.String, Doc: "Lifetime: a duration like 90d or 48h, at most 366d (default 90d)"},
								{Name: "viewer", Kind: cmdline.Bool, Doc: "Mint a READ-ONLY viewer token: it can read the console and cannot submit jobs, edit memory, or open a share"},
								{Name: "code", Kind: cmdline.Bool, Doc: "Print a one-time code instead, for a console link's #code=; the console trades it for the token within a minute, once"},
							},
						},
						{Name: "ls", Short: "List every stored token"},
						{Name: "revoke", Short: "Revoke a stored token by exact id or exact name"},
					},
				},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Show effective config", Command: "magus config view"},
		{Comment: "Show config as JSON", Command: "magus config view -o json"},
		{Comment: "Set cache to read-only", Command: "magus config set key=cache.immutable,value=true"},
		{Comment: "Prune local cache entries older than a week", Command: "magus config cache prune --older-than 168h"},
	},
}

var serverCommand = cmdline.Command{
	Name:        "server",
	Short:       "Manage the magus server: MCP, the console, APIs and background jobs",
	Description: "Start, stop, reload or check the magus server a person starts: MCP over HTTP, the console, the APIs, background jobs and the warm graph and symbol watch.",
	Tags:        []string{"cli", "magus server", "server", "mcp", "console", "socket", "persistent"},
	Long: `Start, stop, reload or check the magus server.

The server is the background process a person asks for. It serves MCP and the
console over HTTP, the APIs behind them, background jobs and scheduled
maintenance, and keeps each workspace's knowledge graph and symbol indexes
current. It keeps workspaces warm, so nested magus calls that forward to it pay
for discovery and config once. Nothing starts it but ` + "`magus server start`" + ` (and
` + "`graph export --follow`" + `, which asks for the console by name), and it runs
until stopped.

It is not what holds this host's capacity: that is ` + "`magus broker`" + `, which a
run starts on its own. The server asks the broker like any run does.

The socket address is resolved in priority order:
  --socket flag  >  MAGUS_SERVER_ADDRESS env  >  server.address in magus.yaml  >
  default ($XDG_RUNTIME_DIR/magus/server.sock)

A detached server logs to $XDG_STATE_HOME/magus/server.log; under
--foreground it logs to stderr for the supervisor to keep.

Signals:
  SIGHUP           reload configuration, the same as ` + "`magus server reload`" + `
  SIGTERM, SIGINT  stop: close the socket, cancel the runs it adopted, and
                   wait up to shutdown_grace (default 5m) for them to unwind
  a second one     exit now`,
	Usage: "magus server <start|stop|status|reload> [flags]",
	// Each subcommand carries its own flags. --foreground sat on the parent with
	// "(server start)" in its doc, and stop's --socket was not declared at all, bound
	// by the command, absent from every man page.
	Children: []cmdline.Command{
		{
			Name:  "start",
			Short: "Start the server (auto-backgrounds by default; --foreground blocks)",
			Flags: []cmdline.Flag{
				{Name: "foreground", Kind: cmdline.Bool, Doc: "Run in the foreground and block, instead of auto-backgrounding"},
			},
		},
		{
			Name:  "stop",
			Short: "Send a graceful shutdown request to the running server",
			Flags: []cmdline.Flag{
				{Name: "socket", Kind: cmdline.String, Doc: "Server socket (default: config / MAGUS_SERVER_ADDRESS / server.sock)"},
				{Name: "pools", Kind: cmdline.Bool, Doc: "Stop per-process pool parents whose build differs from this binary (leftover magus mcp / runs), leaving a matching server alone"},
			},
		},
		{
			Name:        "status",
			Short:       "The server: whether it is up and where you reach it",
			Description: "Report the running server's pid, version, uptime, socket, listeners, MCP url and console url, what it is running, and the workspaces it has loaded. Exits non-zero when no server is running.",
			Long: `The server, and nothing else: is it up, and where do I reach it.

` + "`magus status`" + ` is the other one. It answers what this workspace and this
host are doing: the broker and what holds capacity, the server, what is
loaded, and what the cache and config are. It embeds the server rows too, so
this verb is the narrow view rather than a different fact.

It exits non-zero when no server is running, matching ` + "`magus server stop`" + `,
so a script can chain on it.`,
			Usage: "magus server status [--socket <addr>] [flags]",
			Flags: []cmdline.Flag{
				{Name: "socket", Kind: cmdline.String, Doc: "Server socket (default: config / MAGUS_SERVER_ADDRESS / server.sock)"},
			},
		},
		{
			Name:  "reload",
			Short: "Re-read configuration without restarting: drop the server's open workspaces",
			Flags: []cmdline.Flag{
				{Name: "socket", Kind: cmdline.String, Doc: "Server socket (default: config / MAGUS_SERVER_ADDRESS / server.sock)"},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Start the server (auto-backgrounds)", Command: "magus server start"},
		{Comment: "Run the server in the foreground (supervisor or debugging)", Command: "magus server start --foreground"},
		{Comment: "Stop the running server", Command: "magus server stop"},
		{Comment: "Stop leftover pool parents from an older build", Command: "magus server stop --pools"},
		{Comment: "Reload configuration without restarting", Command: "magus server reload"},
		{Comment: "Everything running on this host", Command: "magus status"},
		{Comment: "Use a custom socket path", Command: "magus --server-address unix:///tmp/m.sock server start"},
	},
}

var brokerCommand = cmdline.Command{
	Name:        "broker",
	Short:       "The per-user process holding this host's capacity and shared services",
	Description: "Check or stop the broker: the per-user process that holds this host's concurrency slots, declared memory and shared services, which a run starts on its own.",
	Tags:        []string{"cli", "magus broker", "broker", "capacity", "memory_mb", "services", "concurrency"},
	Long: `The broker holds this host's capacity: the concurrency slots and declared
memory_mb every magus on it shares, and the services runs keep warm between
them. Every run asks it before starting a step; a step that does not fit
alongside what other invocations hold is refused with MGS3009 (exit 75).

A run starts a broker when none answers, and prints one line saying so. It
loads no workspace, records no telemetry and listens on a unix socket only
($XDG_RUNTIME_DIR/magus/broker.sock). It exits once it has held nothing (no
claim, no service with a dependent) for ten minutes; a started broker logs to
$XDG_STATE_HOME/magus/broker.log.

Each run holds one connection to it for its life, and every claim and service
reference rides that connection, so a run killed outright releases what it
held at once. When a broker dies with runs still going, they re-assert their
claims on the next one.

The broker setting in magus.yaml decides what a run does about it: required
refuses a step when none answers (MGS3022, exit 69), best-effort (the default)
runs unarbitrated and says so once, off never starts or contacts one.

Run with no target, it serves in this process and logs to stderr, or to the
file --log names. Under systemd it takes the socket the supervisor hands over
(LISTEN_FDS), refusing one that is malformed or bound anywhere but
broker.sock. ` + "`magus broker units`" + ` prints the systemd or launchd units for
it; magus never installs them.

Signals:
  SIGHUP   reopen the --log file, for a log rotator that moved it aside
  SIGTERM  drain: seat no new claim or service, turning each away with the
           reason, and exit once the runs holding it finish or
           shutdown_grace (default 5m) passes
  SIGTERM  a second one, or SIGINT at any time: stop its services and exit now

A run the drain turns away is refused under broker: required (MGS3022) and
runs unarbitrated under best-effort, each naming the draining broker. Runs
still holding claims when the broker exits keep going and re-assert them on
the next one.`,
	Usage: "magus broker [status|stop|units] [flags]",
	Flags: []cmdline.Flag{
		{Name: "idle-exit", Kind: cmdline.Duration, Default: 10 * time.Minute, Doc: "Exit once the broker has held nothing this long; 0 never exits, for a supervisor that keeps it alive"},
		{Name: "log", Kind: cmdline.String, Doc: "Append stdout and stderr to this file, reopening it on SIGHUP; a run that starts a broker passes $XDG_STATE_HOME/magus/broker.log"},
	},
	Children: []cmdline.Command{
		{
			Name:        "status",
			Short:       "The broker: its capacity, every claim holding it, and its services",
			Description: "Report the broker's pid, socket, capacity, each claim holding it and the services it hosts. Exits non-zero when no broker is running.",
			Usage:       "magus broker status [flags]",
		},
		{
			Name:  "stop",
			Short: "Stop the broker, or with --services only the services it hosts",
			Flags: []cmdline.Flag{
				{Name: "services", Kind: cmdline.Bool, Doc: "Stop the broker's hosted services, leaving the broker running"},
			},
		},
		{
			Name:        "units",
			Short:       "Print the systemd or launchd units that supervise the broker",
			Description: "Print the unit files a supervisor needs to run the broker: a systemd socket and service that socket-activate it, or a launchd agent that keeps it alive. magus prints them and never installs them.",
			Usage:       "magus broker units [systemd|launchd] [flags]",
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Is a broker up, and what holds capacity", Command: "magus broker status"},
		{Comment: "Stop the services it keeps warm", Command: "magus broker stop --services"},
		{Comment: "Print the systemd units that socket-activate it", Command: "magus broker units systemd"},
		{Comment: "Never start or ask one, for this run", Command: "magus run test . --broker off"},
	},
}

var mcpCommand = cmdline.Command{
	Name:        "mcp",
	Short:       "Serve MCP over stdio for the agent host that launched it",
	Description: "Serve the MCP tools over stdin and stdout for the agent host that launched the process, against the workspace it was launched in, with no server and no token.",
	Tags:        []string{"cli", "magus mcp", "mcp", "agent", "stdio"},
	Long: `Serve MCP over stdin and stdout, one JSON-RPC message per line, for the
agent host that launched this process. It opens the workspace it is launched
in and needs no server and no bearer token: the caller is the local process
the host started, admitted with mcp=write, and every tool call is recorded on
the activity trail with the stdio credential and the client's name.

Stdout is the protocol wire and carries nothing else; logs and one line
saying what is being served go to stderr. It stops when the host closes
stdin, or on Ctrl+C.

Register it with an MCP client as a stdio server:

  command  magus
  args     ["mcp"]

magus server start serves the same tools over Streamable HTTP for one
long-lived server shared by several clients; that endpoint takes a
connector token (magus config mcp connector create). A target this process
runs asks the broker for host capacity like any other run.

Per-client configuration lives in docs/guides/integrations/mcp.md, not in
this binary: naming a client here would make a change to its config format a
magus release.`,
	Usage: "magus mcp",
	ExitStatus: []cmdline.ExitCode{
		{Code: 0, Meaning: "The host closed stdin."},
		{Code: 1, Meaning: "The workspace did not load, or reading stdin or writing stdout failed."},
		{Code: 2, Meaning: "An argument was given; mcp takes none."},
	},
}

var completionCommand = cmdline.Command{
	Name:        "completion",
	Short:       "Print a shell completion script",
	Description: "Print a bash, zsh, fish, or PowerShell completion script to stdout, ready to append to your shell startup file for tab-completion of magus commands.",
	Tags:        []string{"cli", "magus completion", "completion", "bash", "zsh", "fish", "powershell", "shell"},
	Long: `Print a shell completion script to stdout. Every script registers both
magus and the mgs shorthand.

Completes subcommands, the targets accepted by run and affected, the nouns
accepted by describe, and each subcommand's flags. Project paths are
completed live by shelling out to magus ls -o name, so they track the
workspace instead of a baked-in list; outside a workspace they are simply
empty.

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
  powershell  append it to $PROFILE. pwsh is accepted as an alias, for the
              cross-platform PowerShell binary's own name.`,
	Usage: "magus completion <bash|zsh|fish|powershell>",
	Examples: []cmdline.Example{
		{Comment: "Bash: source from the rc, never goes stale", Command: `echo 'source <(magus completion bash)' >> ~/.bashrc`},
		{Comment: "Zsh: install onto $fpath (not into ~/.zshrc)", Command: "magus completion zsh > ~/.zsh/completions/_magus"},
		{Comment: "Fish", Command: "magus completion fish > ~/.config/fish/completions/magus.fish"},
		{Comment: "PowerShell", Command: "magus completion powershell >> $PROFILE"},
	},
}

var manCommand = cmdline.Command{
	Name:        "man",
	Short:       "Install the man pages embedded in this binary",
	Description: "Write magus section 1 man pages from the running binary to a user-selected manpath.",
	Tags:        []string{"cli", "magus man", "manpage", "documentation", "install"},
	Long:        `Write the complete magus manpage set carried by this binary. The installer uses this command to place the pages under the selected installation prefix.`,
	Usage:       "magus man install [--dir <path>]",
	Children: []cmdline.Command{
		{
			Name:  "install",
			Short: "Write the embedded section 1 man pages",
			Flags: []cmdline.Flag{
				{Name: "dir", Kind: cmdline.String, Doc: "Directory for section 1 man pages (default: the user manpath)"},
				{Name: "dry-run", Kind: cmdline.Bool, Doc: "Print what would be written without touching the filesystem"},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Install to the default user manpath", Command: "magus man install"},
		{Comment: "Install to a custom prefix", Command: "magus man install --dir ~/.local/share/man/man1"},
	},
}

// initCommand documents `magus init`. Defined here (untagged) so it can be
// embedded in the registry's All list regardless of build tags.
var initCommand = cmdline.Command{
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

The config holds no keys: every key keeps its built-in default until
"magus config set" changes it, so no key a later magus removes is pinned
there. An existing config is left as it is; --force replaces it with an
empty one. Nothing a newer magus wrote (the config, the managed sections in
.gitattributes and the hooks, the merge driver registration) is replaced:
init stops and names the build to update to.

The VCS is taken from --vcs, or chosen interactively when stdin is a terminal.

The "spell" subcommand scaffolds a new spell instead of bootstrapping a
workspace: "magus init spell <name>" writes spells/<name>/spell.buzz with the
mgs_ contract stubbed, each function documented, and a runnable test block.`,
	Usage: "magus init [flags]",
	Flags: []cmdline.Flag{
		{Name: "global", Kind: cmdline.Bool, Doc: "Write only the global config; skip the workspace bootstrap"},
		{Name: "dry-run", Kind: cmdline.Bool, Doc: "Print the config, magusfile, and merge-driver destinations without writing any of them"},
		{Name: "local", Kind: cmdline.Bool, Doc: "Write config into the repo (CWD) instead of $XDG_CONFIG_HOME/magus/"},
		{Name: "force", Kind: cmdline.Bool, Doc: "Replace an existing config file with one holding no keys"},
		{Name: "vcs", Kind: cmdline.String, Doc: "VCS to wire the merge driver for (git|hg); prompts when omitted on a TTY"},
	},
	Examples: []cmdline.Example{
		{Comment: "Bootstrap the current repo", Command: "magus init"},
		{Comment: "Non-interactive (CI): pick the VCS explicitly", Command: "magus init --vcs git"},
		{Comment: "Write only the global config", Command: "magus init --global"},
		{Comment: "Scaffold a new spell", Command: "magus init spell mytool"},
	},
}

var versionCommand = cmdline.Command{
	Name:        "version",
	Short:       "Print the client and server versions",
	Description: "Print the magus version string, git commit hash, and build date for the currently installed binary, plus the version reported by the server serving this workspace.",
	Tags:        []string{"cli", "magus version", "version", "build info", "commit", "server"},
	Long: `Print the magus version string, git commit hash, and build date.

Two versions, because there can be two binaries: this one, and the server
that has been serving the workspace since it was started. A server outlives
the CLI that started it, so upgrading magus leaves the older code running
until it is restarted - which is the case this command exists to show. The
server line reads "not running" when nothing answers, and --client skips the
probe entirely for a script that wants the build stamp with no server I/O.

In json and yaml the server key is present only when the probe ran: absent
means it never ran (--client, or -o name, which prints the bare version and
renders no server), and an empty value means it ran and nothing answered.`,
	Flags: []cmdline.Flag{
		{Name: "client", Kind: cmdline.Bool, Doc: "Print only this binary's version; skip the server probe entirely"},
	},
	Usage: "magus version [flags]",
	Examples: []cmdline.Example{
		{Comment: "Client and server", Command: "magus version"},
		{Comment: "Just this binary, no server I/O", Command: "magus version --client"},
		{Comment: "The bare version, for a CI pin comparison", Command: "magus version -o name"},
	},
}

// A subcommand that reaches the dispatcher but not All is documented nowhere on the machine.
// These eight were in that state; TestManpageCoversEverySubcommand now keeps them from being.

var shellCommand = cmdline.Command{
	Name:        "shell",
	Short:       "Check a command against this workspace's conventions before running it",
	Description: "Read one shell command, or one path an edit is about to write, and report what this workspace would rather you ran.",
	Tags:        []string{"cli", "magus shell", "guard", "conventions", "hook", "pre-tool-use"},
	Long: `Check a shell command against this workspace's conventions, and name the
better command when there is one.

The rules are the workspace's own. A raw ` + "`go build`" + ` misses the cache and the
affected set; a recursive grep misses what the symbol index already knows;
` + "`git add -A`" + ` sweeps regenerated output into a commit about something else.
None of that is a fact about who typed the command, which is why this is a
plain subcommand rather than something under an agent namespace.

Nothing is executed and nothing is prevented. This reports; you decide.

THE INPUT ARRIVES TWO WAYS, and they are the same command either way. A person
passes it as one quoted argument. A host pipes it on stdin, where it may be
plain text or the JSON envelope the host already writes, so nothing has to
survive being quoted through a shell twice. That is deliberate: an agent and a
person get the same verdict from the same entry point, and neither reads
documentation the other cannot.

--path judges the input as a file path an edit is about to write, rather than
as a shell command.

--observe records a path the agent merely REACHED, without judging it. No rule
applies to a read, so the verdict is always pass and the activity event
previews as observed rather than as a guard decision. Which of a host's tools
only look is the caller's knowledge, never magus's.

--message reads the input as a message the person typed. Nothing is judged
and nothing is said back: the guard records the topics the message raises, such
as a question about package structure, and a rule reads that record on the
agent's next call. The message text itself is not kept.

--agent-name, --session, --transcript, and --event are attribution, not policy.
They record who produced the observation on the activity event, and the verdict
never reads what they say. The host name is an opaque label rather than a set
magus knows: a magus that enumerated hosts would need a release per host, and a
caller that cannot extract a session id must still be able to get a verdict.

--transport names the form of the hook calling, such as sh or buzz, as that form
declares it. Installed hook glue passes it, and glue that passes it without
--agent-name is refused with MGS3024: the configuration "magus describe
harness" prints names the host, so a call that does not is a stale or hand-written
config, never defaulted to one host. With --agent-name and --session it names the CALLER that the
once-per-session notices and the full text of a repeated deny are kept for, as
one key, host/transport/session. A session id alone is not enough: two hosts can
present the same id, and two forms of one hook wired into a session are two
callers, each told a rule in full once.

--lease is the exception: it IS policy. It names the lease the caller is acting
as, and a write is then graded against that lease's declared write boundary in
this workspace's lease ledger. Inside its write paths passes; inside its deny
paths, or inside another live lease's write paths, is denied and the reason
names the owning lease. It defaults to the magus.lease member of $BAGGAGE - the
W3C baggage list a spawning tool exports - and the flag wins when both are set.

A call that names no valid lease while a fleet is running is ADVISED and never
blocked: a person editing their own repository has no lease id, and the guard is
a seatbelt for callers that opt in rather than a sandbox. With no ledger, or
with no lease in it declared or running, nothing is graded and nothing is read.

EXIT CODES are the contract a host blocks on: 2 is a deny and everything else is
allowed. An advisory exits 0 on purpose - it attaches context and does not block,
and a suggestion that failed a script would not be a suggestion. Input that could
not be READ is 2, so a host that blocks on 2 fails closed when bytes were lost on
the way in; an EMPTY input passes, because a wrapper that hands this nothing must
not block every tool call.`,
	Usage: "magus shell '<command>' [flags]",
	Flags: []cmdline.Flag{
		{Name: "path", Kind: cmdline.Bool, Doc: "Judge the input as a file path an edit is about to write, not as a shell command"},
		{Name: "observe", Kind: cmdline.Bool, Doc: "Record the input as a path the agent reached, without judging it: no rule applies and the verdict is always pass"},
		{Name: "message", Kind: cmdline.Bool, Doc: "Read the input as a message the person typed, without judging it: it records the topics the message raises, which a later rule reads, and the verdict is always pass"},
		{Name: "lease", Kind: cmdline.String, Doc: "The lease this call is acting as, graded against the ledger's declared write boundary; outranks the spawn record, the checkout's marker and magus.lease in $BAGGAGE"},
		{Name: "agent-name", Kind: cmdline.String, Doc: "Name of the agent host this invocation came from (attribution only); required with --transport"},
		{Name: "transport", Kind: cmdline.String, Doc: "The form of the hook calling, such as sh or buzz; the once-per-session notices and deny explanations are kept per host, transport and session. Without --agent-name it is refused (MGS3024)"},
		{Name: "session", Kind: cmdline.String, Doc: "The host's own session id for this invocation"},
		{Name: "agent", Kind: cmdline.String, Doc: "The host's id for the subagent making this call, empty for the main conversation; a subagent magus saw spawned is graded under its job"},
		{Name: "transcript", Kind: cmdline.String, Doc: "Path to the host's own log of this session, recorded as a pointer; magus never opens it"},
		{Name: "event", Kind: cmdline.String, Doc: "The host's hook event name (e.g. PreToolUse)"},
		{Name: "observes-skill-loads", Kind: cmdline.Bool, Doc: "This hook wiring observes the host loading a skill (a named reference document), so a rule may require one before a spawn, a write or a command; without it those rules stand down"},
		{Name: "tool-rows", Kind: cmdline.String, Doc: "The directory, relative to the workspace root, holding the host's tool rows that `magus agent harness install` writes; without it no tool is known and each call whose input shape names no command, path, search, prompt or skill is unjudged"},
		{Name: "renders-ask", Kind: cmdline.Bool, Doc: "This wiring puts an ask verdict in front of the person through the host's own approval prompt; without it an ask is returned as a deny"},
		{Name: "call-background", Kind: cmdline.Bool, Doc: "The host runs this shell command in the background, as the call asked; a leased worker's magus run is then refused"},
		{Name: "call-timeout-ms", Kind: cmdline.Int, Doc: "The timeout in milliseconds the call gave its shell command; 0 or absent means the host's foreground default"},
		{Name: "foreground-default-ms", Kind: cmdline.Int, Doc: "The timeout in milliseconds the host gives a shell command that names none, declared by the wiring; without it the run-timeout advice stands down"},
		{Name: "foreground-max-ms", Kind: cmdline.Int, Doc: "The longest timeout in milliseconds the host lets a foreground shell command wait, declared by the wiring; without it the run-timeout advice stands down"},
		{Name: "resumes-on-background-from", Kind: cmdline.String, Doc: "The first host version that resumes a stopped subagent when a background task it left running completes, declared by the wiring; without it a subagent's stop with a background task running is refused"},
	},
	Examples: []cmdline.Example{
		{Comment: "Check one command", Command: "magus shell 'go test ./...'"},
		{Comment: "Check a search", Command: "magus shell 'grep -rn HandleFoo internal/'"},
		{Comment: "Judge a path an edit would write", Command: "magus shell --path MAGUS.md"},
		{Comment: "As a host's pre-tool-use hook", Command: "printf '%s' 'go build ./...' | magus shell"},
		{Comment: "Record a path an agent read, without judging it", Command: "printf '%s' 'internal/cache/output.go' | magus shell --observe"},
		{Comment: "Grade a write as a lease", Command: "printf '%s' 'internal/job/store.go' | magus shell --path --lease f2-guard"},
		{Comment: "The verdict as JSON", Command: "magus shell 'git add -A' -o json"},
	},
	// The status IS the enforcement: a host that reads only the exit code blocks on 2
	// and runs the command on 0, so a wrapper author has to be told that advise passes
	// and that 2 is overloaded.
	ExitStatus: []cmdline.ExitCode{
		{Code: 0, Meaning: "The input is allowed: pass, or advise, which attaches context and does not block. --observe and --message always land here, because they judge nothing. An EMPTY input is also 0: a wrapper that hands this nothing must not block every tool call."},
		{Code: 1, Meaning: "Not returned by a verdict. A wrapper should treat anything other than 2 as allowed rather than enumerating codes, so that a future signal added here does not start blocking commands."},
		{Code: 2, Meaning: "A DENIED command or path, and also input that could not be READ - the two share the code deliberately: a guard that could not parse its input has not cleared the command either, so a host that blocks on 2 fails closed in both cases. Misuse (an unquoted command, an unknown flag) is also 2."},
	},
}

var refsCommand = cmdline.Command{
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
one for the other is how a wrong answer looks right.

--definition answers "where is the body" with the exact lines: each
definition as path:start-end, from the index's enclosing range, checked
against the file on disk: verified when the file predates its index,
changed (exit 1) when the symbol's name has left the start line, and
unverified when the name is there but the file was edited since. An index
that recorded no end line says so instead of guessing one. --source adds
the lines themselves.

--text switches to that other question on purpose: a literal substring
search with no symbol index and no graph, printed as path:line:text like
every other grep-shaped tool. It is the replacement a guard deny routes a
recursive grep to, so it answers on a cold worktree with no index built.
Its exit code is grep's (0 matched, 1 no match, 2 error), not the verdict
codes the symbol lookup above uses - the two modes answer different
questions and are not meant to share a contract.

--rename <new> is the one write: it replaces the symbol's name at every
site --occurrences verified, and nowhere else. A bare name must resolve
to exactly one symbol defined in the workspace; pass the symbol ID
otherwise. Before anything is written, every file is checked and graded
under the acting lease by the rules a host edit meets: a stale or
missing index, an unverified site, a declared output, or a guard deny
refuses the whole rename. Then every file is written or none is. The
global --dry-run resolves and grades every site and prints them, writing
nothing. The inverse is the same command with the two names swapped.`,
	Flags: []cmdline.Flag{
		{Name: "refresh", Kind: cmdline.Bool, Doc: "Re-ingest the SCIP index before answering"},
		{Name: "occurrences", Kind: cmdline.Bool, Doc: "Every exact source range, uncapped and verified against the tree: the view a mechanical edit needs. The default line list is capped and describes fan-in."},
		{Name: "definition", Kind: cmdline.Bool, Doc: "Print each definition as path:start-end, the lines its body spans, checked against the file on disk. A range the index did not record is said, never guessed"},
		{Name: "source", Kind: cmdline.Bool, Doc: "With --definition (implied), also print the definition's lines: a symbol's body by name, in place of grep -n then sed -n"},
		{Name: "text", Kind: cmdline.Bool, Doc: "Raw substring search, no symbol index: print path:line:text matches and exit 0/1/2 for matched/no-match/error (grep's contract, not refs' verdict exit codes). Trailing paths scope it, as grep's do; with none it searches the workspace"},
		// Custom, not Bool: bound by refsCmd itself alongside flaggen.BindRefs, the
		// same reason watch's --ignore is (see that entry above).
		{Name: "no-generated", Kind: cmdline.Custom, Doc: "In the fallback text search shown beside a symbol miss, or with --text, exclude declared-output files entirely instead of searching them and marking the ones that match"},
		{Name: "limit", Kind: cmdline.Int, Doc: "Print at most this many --text matches, then say how many more there were (0 for all). What `| head` would do, without losing the count or the exit code"},
		{Name: "rename", Kind: cmdline.String, Doc: "Rename the symbol to this name at every verified occurrence, after grading every file under the acting lease; every file is written or none is"},
	},
	Usage: "magus refs <symbol> [flags]",
	Examples: []cmdline.Example{
		{Comment: "Every reference to a symbol", Command: "magus refs Open"},
		{Comment: "By fully-qualified node ID", Command: "magus refs symbol:github.com/egladman/magus/Open"},
		{Comment: "As JSON", Command: "magus refs Open -o json"},
		{Comment: "Where a symbol's body starts and ends", Command: "magus refs Open --definition"},
		{Comment: "A symbol's body, by name", Command: "magus refs Open --source"},
		{Comment: "Raw text search, no index needed", Command: "magus refs TODO --text"},
		{Comment: "Preview a rename", Command: "magus refs parseQuery --rename parseTerms --dry-run"},
		{Comment: "Rename a symbol at every verified site", Command: "magus refs parseQuery --rename parseTerms"},
	},
}

var cleanCommand = cmdline.Command{
	Name:        "clean",
	Short:       "Remove declared outputs (regenerable, never sources)",
	Description: "Delete the files each selected project declares as Outputs, optionally dropping the matching cache entries so the next run rebuilds from scratch.",
	Tags:        []string{"cli", "magus clean", "outputs", "cache", "artifacts", "rebuild"},
	Long: `Remove the declared Outputs of the selected projects. With no project
arguments the cwd project (or the whole workspace, from the root) is selected.

These are the same files the cache snapshots and replays on a hit. Whether
each one is regenerable is the declaration's claim, not something clean
verifies - so a file magus only modifies rather than produces belongs in
ctx.modifiesExistingFiles, which clean never removes. Nor does it remove an
output the VCS tracks: that file is committed, and deleting it would dirty
the tree. Preview with the global --dry-run flag before trusting a
declaration you have not read.

--cache additionally invalidates the magus cache entries for those projects,
which is what forces a genuinely full rebuild: removing the files alone
leaves the entries that would replay them.`,
	Usage: "magus clean [flags] [project...]",
	Flags: []cmdline.Flag{
		{Name: "cache", Kind: cmdline.Bool, Doc: "Also invalidate magus cache entries for the selected projects"},
	},
	Examples: []cmdline.Example{
		{Comment: "Preview what would be removed", Command: "magus clean --dry-run"},
		{Comment: "Clean one project", Command: "magus clean web"},
		{Comment: "Clean and force a full rebuild", Command: "magus clean --cache"},
	},
}

var vcsCommand = cmdline.Command{
	Name:        "vcs",
	Short:       "Listing and conflict resolution that knows what is generated",
	Description: "List the dirty paths a commit of this change wants, read against the workspace's declarations, and settle an in-progress merge's conflicted generated files by regenerating them once.",
	Tags:        []string{"cli", "magus vcs", "git", "merge", "conflicts", "generated files", "staging", "ls-files"},
	Long: `Version-control operations that read the workspace's output declarations,
so generated files and hand-written sources are treated differently.

ls-files lists dirty paths and stages nothing; you stage what it lists. By
default it lists tracked, modified or deleted declared sources that no other
live job holds and that are not already staged. Every other dirty path is left
out by a group, and stderr names each group with the flag that lists it:
--include-untracked, --include-generated (outputs a dirty source explains),
--include-unexplained (outputs nothing here explains, with the drift code),
--include-unclaimed (paths no target declares, and the ones magus maintains),
--include-held (paths another live job writes) and --include-staged. A backend
that keeps no such state refuses the flag: only git stages, and jj has no
untracked files.

Positional patterns and --exclude take write-path patterns (a literal path,
a directory, or a glob with * and **), read from the current directory and
matched the same way on every backend. --project scopes to the files a project
owns. Patterns only narrow; a pattern that matches no dirty path is said on
stderr. Stage the listing with:

    magus vcs ls-files -o name | git add --pathspec-from-file=-

resolve settles an in-progress merge, rebase, or cherry-pick. It classifies
every conflicted path at once, regenerates once instead of once per file,
settles the files one side deleted (which no VCS invokes a merge driver for),
and stages everything the regeneration touched so a following commit or
rebase --continue does not refuse on a dirty tree. Conflicts in files magus
does not generate are reported and left alone. Pass --against <ref> to merge
that ref first and settle what it conflicts with; add --dry-run to see the
classification and have the merge backed out again.

--hook <name> is how git runs resolve itself. magus init --vcs git writes
the pre-merge-commit, pre-commit, post-commit, post-rewrite and
post-applypatch hooks beside the merge driver, each calling resolve once the
merge, cherry-pick, revert, rebase or am has the whole tree; magus doctor
reports a driver whose hooks are missing. It regenerates only the projects
whose declared sources or outputs the operation changed, plus what the merge
driver recorded, and stages the result: into the commit git is about to make,
or, for a clean merge, in place of git's own commit, which git then leaves to
you as "git commit". After a rebase or a clean cherry-pick the commits already
exist, so it stages and prints the amend; it never amends. A failed
regeneration stops a pending commit and prints the command that does it.

merge-driver is the per-file driver git and hg invoke during a merge. You do
not run it by hand; it is wired per clone, because a driver registration
cannot be committed. That is also why a forge reports conflicts your own
clone would settle silently, and why resolve exists as the bulk counterpart.

checkpoint prints the identity of the working state right now - head revision,
branch, whether the tree is dirty, and a digest of the uncommitted patch. Record
one when you hand a piece of work out, so a later reader knows what that work was
looking at. By default it RESOLVES AND RECORDS and never MINTS: no tag, no stash,
no ref, no file, nothing changed anywhere, so a checkpoint nobody keeps has cost
nothing. Feed the revision to anything that takes one; compare two digests to learn
whether two workers saw the same uncommitted tree, which the revision alone cannot
say.

--preserve is the one thing that mints. An identity says whether two trees match;
it cannot rebuild either one. --preserve additionally captures the uncommitted
work - tracked edits and untracked files alike - and prints a handle that gets it
back, using each backend's own mechanism: a commit under refs/magus/preserved for
git, a kept shelf for Mercurial, a commit Sapling keeps hidden, and for Jujutsu the
working-copy commit it already holds. The working copy is untouched either way.

Retention differs per backend. On git and Mercurial a capture is dropped at 30
days, by two passes that cover each other's gap: every preserve prunes, which
bounds a repository nothing schedules against, and the server's prune-preserved
job prunes on its own, which reaches a repository preserved once and never
again. That job is a no-op with no server running, so on a machine that runs
none the standalone spelling is magus server prune-preserved.

Sapling drops nothing either way: magus mints a hidden commit there, and no
Sapling command removes one without discarding the working copy, so those stay
until you remove them. List them with sl log --hidden -r "desc('magus preserved
working copy')". Jujutsu mints nothing, so nothing accumulates.

resolve works on git, Mercurial and Jujutsu. Only --against is git-only: merge the
base in yourself on the others, then run resolve.`,
	// No parent Flags: `magus vcs` takes none of its own, and a flag declared on the
	// parent lands in one merged Options section on the man page and in every child's
	// generated binder.
	Usage: "magus vcs <ls-files|resolve|checkpoint|merge-driver> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "ls-files",
			Short: "List the dirty paths a commit of this change wants; stages nothing",
			Flags: []cmdline.Flag{
				{Name: "include-untracked", Kind: cmdline.Bool, Doc: "Also list untracked files; refused on jj, which has none"},
				{Name: "include-generated", Kind: cmdline.Bool, Doc: "Also list declared outputs a dirty source in this change explains"},
				{Name: "include-unexplained", Kind: cmdline.Bool, Doc: "Also list declared outputs no source change here explains"},
				{Name: "include-unclaimed", Kind: cmdline.Bool, Doc: "Also list paths no target declares, and the ones magus maintains itself"},
				{Name: "include-held", Kind: cmdline.Bool, Doc: "Also list paths another live job in this checkout writes"},
				{Name: "include-staged", Kind: cmdline.Bool, Doc: "Also list paths whose whole change is already staged; git only"},
				{Name: "exclude", Kind: cmdline.Custom, Doc: "Leave out paths this write-path `pattern` covers; repeatable or comma-separated"},
				{Name: "project", Kind: cmdline.Custom, Doc: "List only the files this `project` owns; repeatable or comma-separated"},
			},
		},
		{
			Name:  "resolve",
			Short: "Settle an in-progress merge's conflicted generated files, then regenerate once",
			Flags: []cmdline.Flag{
				{Name: "against", Kind: cmdline.String, Doc: "Merge this `ref` first, then settle what it conflicts with"},
			},
		},
		{
			Name:  "checkpoint",
			Short: "Print the working state's identity, for recording what a lease was handed; writes nothing unless --preserve",
			Flags: []cmdline.Flag{
				{Name: "preserve", Kind: cmdline.Bool, Doc: "Also capture the uncommitted work and print a handle that restores it"},
			},
		},
		{Name: "merge-driver", Short: "The per-file merge driver git and hg invoke; you do not run this by hand"},
	},
	Examples: []cmdline.Example{
		{Comment: "Stage the dirty sources and nothing else", Command: "magus vcs ls-files -o name | git add --pathspec-from-file=-"},
		{Comment: "List one subtree's changes, with the outputs they regenerated", Command: "magus vcs ls-files --include-generated 'internal/guard/**'"},
		{Comment: "List a new file too", Command: "magus vcs ls-files --include-untracked --project libs/xdg"},
		{Comment: "Settle a conflicted merge", Command: "magus vcs resolve"},
		{Comment: "Merge the base in and settle it in one step", Command: "magus vcs resolve --against origin/main"},
		{Comment: "Record what a lease was handed", Command: "magus vcs checkpoint"},
		{Comment: "The one citable token, for a ledger cell", Command: "magus vcs checkpoint -o name"},
		{Comment: "Capture the uncommitted work too, before something risky", Command: "magus vcs checkpoint --preserve"},
	},
}

// queueCheckout are the flags naming the checkout every queue verb works in, beside
// the global --root.
var queueCheckout = []cmdline.Flag{
	{Name: "remote", Kind: cmdline.String, Default: "origin", Doc: "Name of the configured `remote` changes and the base are fetched from"},
	{Name: "vcs", Kind: cmdline.String, Default: "git", Doc: "Version control `backend` of the checkout at --root"},
}

// queueFacts are the flags choosing who answers what a change affects and which files
// are generated.
var queueFacts = []cmdline.Flag{
	{Name: "facts", Kind: cmdline.String, Doc: "`command` and its arguments, run with no shell and the fact asked for appended, answering what a change affects and which files are generated, for a build tool other than magus; without it the magus workspace at --root answers"},
	{Name: "target", Kind: cmdline.String, Default: "ci", Doc: "magus `target` the affected set is computed for; not with --facts"},
}

var queueCommand = cmdline.Command{
	Name:        "queue",
	Short:       "Merge approved changes through a speculative, partitioned merge queue",
	Description: "List the changes carrying merge intent, plan them into partitions of independent changes, validate speculative candidates, and merge the green ones through the provider.",
	Tags:        []string{"cli", "magus queue", "merge queue", "stacked changes", "pull requests", "ci"},
	Long: `A speculative, partitioned merge queue. A queue run has three steps, each with
the rights it needs and no more.

plan reads the changes carrying merge intent (a mergequeue.changes/v1 document on
stdin or --changes, as ls prints it), checks each one's approval at its head,
finds which changes are stacked on which, drops what conflicts with the base on
its own, and splits the rest into partitions whose affected sets are disjoint.
It writes a mergequeue.plan/v1 document.

validate runs the changes' code and needs read access only. Per partition it
builds candidates base+A, base+A+B and so on onto each other, regenerates
generated files on each, runs --gate on them, and writes a mergequeue.verdict/v1
per change to --verdicts the moment that change is decided.

apply holds the write credential and runs no change's code. It rebuilds each
green change's candidate itself and merges it through the provider, with the
change's own merge method, as soon as everything beneath it has merged. Its
<source> is the directory validate wrote, or run:<run>, the artifacts of a
validation run as the provider names it (github: <owner>/<name>/runs/<id>).

The checkout is the one at the global --root (default: the current directory),
and every relative path resolves against it. The provider is a built-in name
(github) or a Buzz script. Every verb prints JSONL events (mergequeue.event/v1)
on stdout; ls and describe print their document instead. The global --dry-run makes
apply report what would merge and call nothing on the provider.`,
	Usage: "magus queue <describe|ls|plan|validate|gate|apply|reviews> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "describe",
			Short: "Ask the provider what it supports on a base and what wiring the queue up still takes; prints the steps to run, or a mergequeue.capabilities/v1 document with -o json",
			Long: `Ask the provider what it supports on a base, its merge methods and how a change
is queued, and read how the queue is wired there: the status the base requires
and which integration it is pinned to, the repository settings the queue depends
on, and the required checks a queue push would leave unreported. It prints the
commands that finish the wiring; magus never runs them, a person does.

--app names the app whose credential apply will write with (github: a GitHub
App's slug), and the steps become that app's: install it, store its credential,
and pin the status to its id. The provider reads --app, never the queue. When it
needs more, describe says what, where the provider shows it, and the command to
run next. Every read goes to the provider over the network, with the credential
the provider reads (github: GITHUB_TOKEN or MERGEQUEUE_TOKEN).

-o json prints the mergequeue.capabilities/v1 document with the setup inside it.`,
			Usage: "magus queue describe --provider <provider> --base <branch> [flags]",
			Flags: append([]cmdline.Flag{
				{Name: "provider", Kind: cmdline.String, Doc: "`provider`: a built-in name (github) or a .buzz file"},
				{Name: "base", Kind: cmdline.String, Doc: "`branch` the queue merges into"},
				{Name: "status-context", Kind: cmdline.String, Default: "merge-queue", Doc: "Commit status the queue posts, whose wiring is described; empty describes what the provider supports and reads no setup"},
				{Name: "app", Kind: cmdline.String, Doc: "`app` apply writes with, as the provider names it (github: a GitHub App's slug[:App ID], required with a --status-context)"},
			}, queueCheckout...),
		},
		{
			Name:  "ls",
			Short: "Ask the provider for the changes carrying merge intent; prints a mergequeue.changes/v1 document",
			Usage: "magus queue ls --provider <provider> --base <branch> [flags]",
			Flags: append([]cmdline.Flag{
				{Name: "provider", Kind: cmdline.String, Doc: "`provider`: a built-in name (github) or a .buzz file"},
				{Name: "base", Kind: cmdline.String, Doc: "`branch` the queue merges into"},
			}, queueCheckout...),
		},
		{
			Name:  "plan",
			Short: "Check approval, find stacks, drop what conflicts with the base, and partition by affected set",
			Usage: "magus queue plan --provider <provider> --out <file> [flags]",
			Flags: append(append([]cmdline.Flag{
				{Name: "changes", Kind: cmdline.String, Default: "-", Doc: "The mergequeue.changes/v1 `document`, or `-` for stdin"},
				{Name: "provider", Kind: cmdline.String, Doc: "`provider` approval at each head is checked with"},
				{Name: "out", Kind: cmdline.String, Doc: "`file` the mergequeue.plan/v1 document is written to"},
				{Name: "depth", Kind: cmdline.Int, Default: 3, Doc: "Candidates of one partition that validate at once"},
				{Name: "parallel", Kind: cmdline.Int, Doc: "Changes admitted at once; 0 is one per CPU"},
			}, queueFacts...), queueCheckout...),
		},
		{
			Name:  "validate",
			Short: "Build and gate a candidate per change, writing each verdict the moment it is decided (read access only)",
			Usage: "magus queue validate --stdin --gate <command> --verdicts <dir> [flags] < plan.json",
			Flags: append(append([]cmdline.Flag{
				{Name: "stdin", Kind: cmdline.Bool, Doc: "Read the mergequeue.plan/v1 document from stdin; required"},
				{Name: "gate", Kind: cmdline.String, Doc: "`command` and its arguments, run with no shell in each candidate's checkout with the change's affected projects added ahead of its first `--`; exit 0 is green"},
				{Name: "regenerate", Kind: cmdline.String, Doc: "`command` and its arguments, run with no shell in a candidate with the change's affected projects added ahead of its first `--` and the generated files to rewrite listed on stdin"},
				{Name: "verdicts", Kind: cmdline.String, Doc: "`directory` the plan and the verdicts are written to, one entry per change; apply reads it as its <source>"},
				{Name: "only", Kind: cmdline.String, Doc: "Validate this one `change`; the changes beneath it in its partition are merged under it but not gated"},
				{Name: "parallel", Kind: cmdline.Int, Doc: "Candidates built or gated at once across every partition; 0 is one per CPU"},
				{Name: "remote-cache-read", Kind: cmdline.Bool, Doc: "Let hooks read magus's remote cache from GitHub Actions through a loopback proxy that forwards lookups with the runner's token and refuses writes. Hooks get a stand-in token. Refused without runner credentials or a trusted key"},
			}, queueFacts...), queueCheckout...),
		},
		{
			Name:  "gate",
			Short: "Run a command in a checkout of HEAD boxed and sandboxed exactly as validate runs --gate in a candidate",
			Long: `Check the commit HEAD names out into a box of its own under TMPDIR, with a
private home and temporary directory, and run <command> there under the sandbox
validate gives its --gate: the same box, the same environment and the same grants,
built by the same code. Uncommitted changes are not in the checkout. It exits with
the command's status.

This is how a CI job gates a change in the environment the merge queue will gate
it in, so a change green in CI is not red in the queue for where it ran. The
global --sandbox is the mode, raised to best-effort as validate raises it.

--cache keeps the local cache tier of the command's magus in a directory outside
the box that the caller carries between runs, the one write the box grants outside
itself, and under it the caches the workspace's spells declare (go: GOCACHE and
GOMODCACHE), each in the mode its spell grants it. So a command run in one box, such
as ` + "`magus config cache import --remote`" + `, warms the caches the next box builds
with. --env passes the named variables of this environment to the command's own
magus, over the sandbox's scrub, and no further: its children get the scrubbed
environment. Neither may move the box: a variable the box sets, or one naming a
cache it withholds, is refused.`,
			Usage: "magus queue gate [flags] -- <command> [args...]",
			Flags: append([]cmdline.Flag{
				{Name: "cache", Kind: cmdline.String, Doc: "`directory` the command's magus keeps its local cache tier and its spells' caches in, outside the box; empty keeps them in the box, as validate does"},
				{Name: "env", Kind: cmdline.String, Doc: "Comma-separated `names` of variables passed from this environment to the command's own magus"},
			}, queueCheckout...),
		},
		{
			Name:  "apply",
			Short: "Rebuild and merge the green verdicts <source> holds as they arrive (holds the write credential; runs no change's code)",
			Usage: "magus queue apply --provider <provider> --base <branch> [flags] <source>",
			Flags: append(append([]cmdline.Flag{
				{Name: "provider", Kind: cmdline.String, Doc: "`provider`: a built-in name (github) or a .buzz file"},
				{Name: "base", Kind: cmdline.String, Doc: "`branch` the queue merges into; a plan naming another is refused (MGS3028), and a run: source must have run on it"},
				{Name: "workflow", Kind: cmdline.String, Doc: "`definition` a run: source must have run, started by an event that runs the base's own copy of it (github: .github/workflows/queue.yaml); required with a run: source, whose uploads are otherwise refused (MGS3027)"},
				{Name: "status-context", Kind: cmdline.String, Default: "merge-queue", Doc: "Commit status the queue posts; branch protection requires it"},
				{Name: "once", Kind: cmdline.Bool, Doc: "Apply what <source> holds now and stop, rather than following it until it is complete"},
				{Name: "interval", Kind: cmdline.Duration, Default: 10 * time.Second, Doc: "How often <source> is read while following it"},
				{Name: "committer", Kind: cmdline.String, Doc: "\"Name <email>\" committing each update commit, overriding the provider's committer; with neither, a change needing one waits and apply stops"},
				{Name: "app", Kind: cmdline.String, Doc: "`app` whose credential the provider writes with, as the provider names it (github: a GitHub App's slug[:App ID], required). apply refuses to start when the base requires --status-context from another integration (MGS3019)"},
				{Name: "regenerate", Kind: cmdline.String, Doc: "The base's regeneration `command`, run with no shell or credential: projects go ahead of its first `--`, files to rewrite on stdin. It runs only where the build tool proves the change misses its code; elsewhere apply checks the rest."},
				{Name: "reproduce-gate", Kind: cmdline.String, Doc: "The `command` validate's --gate is given, shown on each kick-back validation decided so its author can run it again; apply never runs it, and never takes it from a verdict"},
				{Name: "reproduce-regenerate", Kind: cmdline.String, Doc: "The `command` validate's --regenerate is given, shown beside --reproduce-gate"},
			}, queueFacts...), queueCheckout...),
		},
		{
			Name:  "reviews",
			Short: "Classify the change since each approval standing on a change, as the queue admits it, and dismiss the approvals it does not carry with --dismiss",
			Long: `Read the approvals standing on one change and, for each given at an older commit
than the one a review of its head covers, classify what changed since, path by
path, with the classifier plan admits changes by: a rebase with its diff unchanged,
regenerated outputs, prose, or comment-only edits carry an approval when the base's
queue.carry_approvals allows that tier (default: all four), and code never does. A
code owner's approval is classified like any other.

--dismiss dismisses, through the provider, each approval that does not carry, telling
its reviewer which paths changed. It reads the head again first and dismisses nothing
when it moved, since the run on the new head decides. It needs the provider's write
credential (github: MERGEQUEUE_TOKEN with pull-requests write); without --dismiss it
only reads. Nothing runs the change's code: the classifier reads the base's own
workspace and the compared commits' contents.

-o json prints a mergequeue.reviews/v1 document.`,
			Usage: "magus queue reviews --provider <provider> --base <branch> --change <id> [flags]",
			Flags: append(append([]cmdline.Flag{
				{Name: "provider", Kind: cmdline.String, Doc: "`provider`: a built-in name (github) or a .buzz file"},
				{Name: "base", Kind: cmdline.String, Doc: "`branch` the change targets"},
				{Name: "change", Kind: cmdline.String, Doc: "`id` of the change, as the provider names it (github: the pull request number)"},
				{Name: "head", Kind: cmdline.String, Doc: "`commit` the change's head must be at; when it moved, nothing is classified or dismissed"},
				{Name: "dismiss", Kind: cmdline.Bool, Doc: "Dismiss each approval that does not carry, naming what changed since it"},
			}, queueFacts...), queueCheckout...),
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Print the commands that wire the queue up", Command: "magus queue describe --provider github --base main"},
		{Comment: "Print the commands that move it onto your own GitHub App", Command: "magus queue describe --provider github --base main --app acme-magus-queue"},
		{Comment: "The same for a private app whose App ID GitHub hides", Command: "magus queue describe --provider github --base main --app acme-magus-queue:2034567"},
		{Comment: "List what carries merge intent", Command: "magus queue ls --provider github --base main > changes.json"},
		{Comment: "Plan it", Command: "magus queue plan --provider github --out plan.json < changes.json"},
		{Comment: "Validate every candidate", Command: "magus queue validate --stdin --verdicts verdicts --gate 'magus run ci' < plan.json"},
		{Comment: "Gate HEAD in the box validate gives a candidate", Command: "magus queue gate --sandbox=required -- magus run ci"},
		{Comment: "Merge the green ones as they arrive", Command: "magus queue apply --provider github --base main verdicts"},
		{Comment: "Merge from a validation run's artifacts", Command: "magus queue apply --provider github --base main --workflow .github/workflows/queue.yaml run:acme/widgets/runs/7"},
		{Comment: "Plan with a provider of your own", Command: "magus queue plan --provider providers/gitlab.buzz --out plan.json < changes.json"},
		{Comment: "Say whether each approval on a pull request carries over the change since it", Command: "magus queue reviews --provider github --base main --change 482"},
		{Comment: "Dismiss the approvals it does not carry", Command: "magus queue reviews --provider github --base main --change 482 --dismiss"},
	},
}

var sessionCommand = cmdline.Command{
	Name:        "session",
	Short:       "What magus invocations did and what agents are blocked on: humans read and dispose, hosts write",
	Description: "One family over the repository's session store: list what magus invocations ran and the host session each ran in, list the blocks agents raised, close one by hand, and take the host-hook ingest that writes it all.",
	Tags:        []string{"cli", "magus session", "sessions", "attention", "history", "worktrees", "agents", "guard"},
	Long: `One noun over the repository's session store, with a human side and a
machine side.

Humans read it. The bare command lists past magus invocations with the targets
each one ran and how those runs ended, the OS user that ran it, and the host
session it ran in (a dash when no host delivered one: the invocation is
unattributed); ` + "`session attention`" + ` lists the requests
agents have raised - work blocked on input or on approval - and
` + "`session dispose`" + ` closes one. Nothing closes a request automatically: there is
no expiry, no severity inference and no auto-dispose flag, because a request
magus could answer by itself would not have needed a person - see the design
principles.

Agent hosts write it. Their hooks pipe every judged command through ` + "`magus shell`" + `,
which records what it judged, and every attention event through ` + "`session notify`" + `
(normalized; a waiting or permission outcome opens a durable request). No person
types notify; it exists to be wired. ` + "`magus shell`" + ` is the exception, and
deliberately so: it is the same command a person runs to ask what this workspace
would rather they ran.

The store is keyed by repository identity rather than by checkout path, so
every git worktree of one repo reads and writes the same records - what
another worktree just finished, and what it is blocked on, is visible here
without a server, a network, or a shared branch. It is append-only and never
rewritten; a line left half-written by a killed process is skipped and counted
rather than failing the read.

The listing shows this checkout's invocations; --all widens it to every checkout
of the repository. It takes --limit to bound by count and --since to bound by AGE, as a
duration back from now (2h, 45m, 168h) or an RFC3339 instant. --since compares
against each invocation's last fact, not its first, so a long one that is still
working stays listed however long ago it began.

--brief answers the listing's own question, where does the work stand, for a
model instead of a person. It prints this checkout read off disk: branch and
revision, commits not yet on the base ref, the dirty tree split by the
classifier ` + "`magus describe file`" + ` uses, up to a dozen live leases (editing first) with the command that
binds each one, the last recorded run's failures with the ref that holds their
output, whether any host hook config here invokes magus, and where this
workspace's rules live. Nothing in it is remembered: an agent host that
replaces a session's history with a summary can wire this to its
session-start event and hand the model state instead of prose.`,
	Usage: "magus session [ls] [flags]",
	Flags: []cmdline.Flag{
		{Name: "brief", Kind: cmdline.Bool, Doc: "Print this checkout's state for a session that lost its history: revision, unpushed commits, classified dirty tree, capped live leases, the last run's failures, guard wiring (--limit and --since do not apply)"},
		{Name: "all", Kind: cmdline.Bool, Doc: "List invocations from every checkout of this repository, not only this one"},
		{Name: "limit", Kind: cmdline.Int, Doc: "Show at most this many invocations (0 for all)"},
		{Name: "since", Kind: cmdline.String, Doc: "Show only invocations active since this point: a duration back from now (2h, 45m, 168h) or an RFC3339 timestamp"},
	},
	Children: []cmdline.Command{
		{Name: "ls", Short: "List past magus invocations and the targets they ran (the default)"},
		{
			Name:        "load",
			Short:       "Load a normalized agent-session event stream from a host transcript",
			Description: "Read one JSON event per line from stdin, re-judge every shell command against the current guard rules, and record the result in this repository's session store.",
			Long: `Load what an agent host's own transcript says a session did.

The guard's activity trail records what the hook SAW. A transcript records
what actually ran, including commands no hook was wired for, the skills a
session loaded, and what the hook printed back. Joining the two is what makes
"was this session guarded, and did it comply" answerable at all.

Extraction is not magus's job. A per-host adapter you own turns a transcript
into this stream, the same division ` + "`magus agent adoption`" + ` draws: magus takes
a stream in its own vocabulary rather than learning to read a host's logs, so
a host changing its format costs you one adapter edit instead of a magus
release.

The stream is one JSON object per line, on stdin or from --file. Every line
needs host, session, kind and ref; kind is one of shell.command, file.read,
file.write, skill.load, hook.output, spawn, or magus.call, and any other kind
is rejected with a diagnostic naming the set. cwd, ts, text, transcript and
outcome are optional.

Events are keyed on (host, session, kind, ref), so re-running an adapter over the same
transcript loads nothing twice: an adapter re-reads whole files instead of
tracking where it stopped. Events whose cwd belongs to another repository are
dropped, and worktrees of this one are kept. A checkout that no longer exists
identifies as its own path, so events from a worktree since deleted drop too.

A shell command's TEXT is never stored. It is re-judged in-process against the
current rules and kept as its program, the verdict (pass, advise or deny), the
rule or advisory behind that verdict, and a sha256 of the line. That is the
question the trail cannot answer - would today's rules have caught yesterday's
command - and it is also the rule the trail already settled: a command line is
content, and content stays in the host's own transcript, which ref and the
transcript pointer lead back to.`,
			Usage: "magus session load [--file <path>]",
			Flags: []cmdline.Flag{
				{Name: "file", Kind: cmdline.String, Doc: "Read the event stream from this file instead of stdin"},
			},
		},
		{
			Name:        "show",
			Short:       "Report one loaded session: what it ran, what the rules say, what it loaded",
			Description: "Summarize one loaded agent session: events by kind, commands grouped by program with re-judged verdicts, skills loaded, and files read and written.",
			Long: `Report one loaded session.

Commands are grouped by program and carry two counts that are deliberately
separate: how many of them today's rules would DENY, and how many the host
recorded as actually denied. The gap between the two is the audit. A command
the rules refuse that ran anyway was never judged, because no guard was wired,
because the binary was too old to judge it, or because the rule arrived after
the command did.

The id is a host session id, listed by ` + "`magus session`" + `. Sessions reach this
store through ` + "`magus session load`" + `; a session magus itself ran has target
results rather than events and is read from the listing instead.`,
			Usage: "magus session show <session-id>",
		},
		{
			Name:        "hints",
			Short:       "Report uptake per hint id: served, followed, rejected, reflex",
			Description: "Count, per hint id, how often a breadcrumb magus served was followed within the next few calls of the same session.",
			Long: `Report what sessions did with the suggestions magus made.

Every ` + "`next`" + ` entry on a result carries a stable id, and magus records the
ids a call served beside its own advisory markers. ` + "`session load`" + ` joins that
record onto a host's transcript, so this reads back, per id: how often it was
served, how often the command it named was run within the next few calls,
how often another magus verb was run instead, and how often the same command
was simply repeated.

The point is pruning by the number. A suggestion nobody takes is context spent
on advice, and the output names the floor under which one is not worth
rewording. It is a note and not an action: what to delete is a decision, and
this command only reports the counts.

It counts only what this repository has LOADED. An empty store reports
nothing, which is a fact about the store rather than about the hints.`,
			Usage: "magus session hints",
		},
		{
			Name:  "attention",
			Short: "List the open requests agents raised, oldest first; with -q, print nothing and exit 1 when the queue is empty",
			Long: `List the requests agents have raised in this repository.

A request is opened by ` + "`session notify`" + `: an event whose outcome is waiting
(blocked on input) or permission (blocked on approval) becomes a durable
request instead of a notification that scrolls past. Any other outcome opens
nothing.

With the global -q, print nothing and answer with the exit status instead:
0 when at least one request is open, 1 when the queue is empty. Neither status
reports a fault - an empty queue is the good state - so this is the form to
test from a shell prompt or a wrapper script.`,
		},
		{
			Name:  "dispose",
			Short: "Close one open request by id or unambiguous id prefix",
			Long: `Close one open request. A request closes once and stays closed;
re-raising a block that is already open updates nothing and adds no second row.

The id may be any unambiguous prefix of a request id, the way a short revision
names a commit. An ambiguous prefix is refused and names every candidate rather
than picking one, because the id addresses a person's decision to close a
block.`,
			Flags: []cmdline.Flag{
				{Name: "reason", Kind: cmdline.String, Doc: "Record why the request is being closed, alongside the disposition"},
			},
		},
		{
			Name:        "checkpoint",
			Short:       "Record where the work stands, so it can be picked up later",
			Description: "Record one checkpoint for this repository: the revision the work sits on, and a note saying where it stands.",
			Long: `Record where the work stands, so whoever comes back to it - you tomorrow,
or another session - does not have to reconstruct it.

Work stops for ordinary reasons. A day ends, a branch gets parked, an agent
host hits a usage limit or is simply closed. In every case the work sits
exactly where it was and nothing says so, and the next person starts by
rediscovering what the last one already knew.

` + "`magus vcs checkpoint`" + ` works out the same position and prints it; this one
records it and keeps it, with a note. The note is what makes it worth keeping,
so the position arrives with the reason someone stopped at it.

Run it by hand before you put something down, or wire it to an agent host's
stop hook. Only ` + "`--note`" + ` is worth typing: the workspace is where you ran it,
and the revision, branch and dirtiness are read from the tree. A tree with no
revision to report - one before its first commit, or under no VCS - still
records a usable checkpoint.

A host's hook envelope on stdin supplies the two pointers only a host knows, its
session id and its transcript path, so a wrapper needs no JSON tool on the
critical path. Nothing in that payload becomes the note: a host's closing message
is the model's prose, and a note is written by whoever stopped working. The read
is bounded, so this never blocks waiting for input that is not coming.

--agent-name is an opaque label the caller chooses, exactly as on the guard's
hook. magus does not know which tools exist, which model any of them ran, or how
to start one; it records what it was told and reports it to a person. Read the
result with ` + "`magus session`" + `.`,
			Usage: "magus session checkpoint [--note <text>] [flags]",
			Flags: []cmdline.Flag{
				{Name: "note", Kind: cmdline.String, Doc: "A sentence on where the work stands"},
				{Name: "agent-name", Kind: cmdline.String, Doc: "Name of the agent host this session ran on, when one did (attribution only)"},
				{Name: "session", Kind: cmdline.String, Doc: "The host's own session id for this session"},
				{Name: "transcript", Kind: cmdline.String, Doc: "Path to the host's own log of this session, recorded as a pointer; magus never opens it"},
			},
		},
		{
			Name:        "notify",
			Short:       "Normalize an attention event and optionally notify the local desktop",
			Description: "Raise one canonical attention event from plain text or a JSON envelope, and optionally surface it as an operating-system notification.",
			Long: `Raise one canonical attention event.

The input is read from stdin and may be plain text or a complete event JSON
envelope. Either way the result is one normalized event, so a caller that
knows nothing about magus's event schema can still raise a well-formed one.

--outcome names the event's outcome vocabulary. --desktop additionally
raises an operating-system notification, which is the part a human notices;
without it the event is recorded and nothing pops up.

An event whose outcome is waiting (blocked on input) or permission (blocked on
approval) additionally opens a durable request in this repository, listed by
` + "`session attention`" + ` and closed only by a person. Any other outcome opens
none. This is the only command that opens one.

A repeat of a request the queue already holds does not raise another desktop
notification. The open row is the block, and a second toast asks for a yes on
it. A failure still notifies, and so does a waiting or permission event that
could not be filed, because that one has no row.`,
			Usage: "magus session notify [--outcome <vocab>] [--desktop]",
			Flags: []cmdline.Flag{
				{Name: "outcome", Kind: cmdline.String, Doc: "Outcome vocabulary for the event"},
				{Name: "desktop", Kind: cmdline.Bool, Doc: "Also raise an OS notification"},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Show recent invocations", Command: "magus session"},
		{Comment: "Show today's work", Command: "magus session --since 24h"},
		{Comment: "Hand a compacted session this checkout's state", Command: "magus session --brief"},
		{Comment: "Full session records as JSON", Command: "magus session -o json"},
		{Comment: "Load a host transcript an adapter normalized", Command: "magus session load --file events.ndjson"},
		{Comment: "Read one loaded session back", Command: "magus session show 8f1c2d4e"},
		{Comment: "List open attention requests", Command: "magus session attention"},
		{Comment: "Ask whether anyone is waiting", Command: "magus session attention -q"},
		{Comment: "Close one request, saying why", Command: `magus session dispose att-3f9c -reason "approved and pushed by hand"`},
		{Comment: "Raise a permission prompt on the desktop (host-wired)", Command: "printf '%s\\n' 'needs approval' | magus session notify --outcome permission --desktop"},
	},
	ExitStatus: []cmdline.ExitCode{
		{Code: 0, Meaning: "Sessions or requests were listed, a request was disposed, or an event was normalized and emitted. A plain listing exits 0 whether or not anything was listed, because an empty queue is the good state. notify's delivery is best-effort and never changes this: a desktop notification that could not be raised, and a durable request that could not be opened, are both reported as warnings and still exit 0."},
		{Code: 1, Meaning: "dispose: the request named is not in the store, or was already disposed; a request closes once and stays closed. attention with -q: the queue is empty, so a prompt or watch loop can branch on status instead of parsing the listing. notify: stdin could not be read. Text that is not a complete event envelope becomes the event's message rather than an error. load: at least one line was rejected; usable lines are still loaded and the summary is printed, so fix the adapter and run it again. show: the session named has no loaded events."},
		{Code: 2, Meaning: "Misuse: an unknown subcommand, an argument to a listing, or a dispose naming other than exactly one id."},
	},
}

var jobCommand = cmdline.Command{
	Name:        "job",
	Short:       "Fork a job, take it, return it with its result, and verify that result",
	Description: "The POSIX child lifecycle over delegated work: fork declares a job, exec takes the lease on it in this checkout, exit returns it with its result, wait verifies that result, and run submits one of the server's own jobs.",
	Tags:        []string{"cli", "magus job", "job", "jobs", "lease", "agents", "delegation"},
	Long: `Delegated work on the shell's own lifecycle. A JOB is the unit of work; a LEASE
is the grant one holder has on it: its write and read paths, plus the one check
it runs. A job is not a run: ` + "`magus run`" + ` executes a target with no job involved,
while a job's check and the server's maintenance each cause runs.

Two channels write the job store. The client MCP tool (magus\job) is an agent's, this verb
is a person's, and they reach the same store and the same rules. One author per
JOB is the property that matters, and the store enforces it: a session holding a
lease may record the base it landed on, shrink its own write paths, end its own
job in fail or no_return, and fork a child of itself inside its own paths.
Everything else, widening a boundary and verifying a job included, is refused by name.

Jobs are kept per repository rather than per checkout, so a session forking in one
worktree and a holder working in another see the same set.

fork declares one job, from flags for the one-job case or from a JSON record on
--stdin. It replaces the job it names rather than merging into it, which is the
difference between a person declaring what a job IS and an agent advancing one
field of a live one. Every path flag is repeatable or comma-separated, and an
empty segment is refused rather than dropped. There is no --state: this declares a
new job, and one nobody has taken is declared.

exec takes a job in this checkout. It records the base this tree actually landed
on beside the checkpoint the job was handed, with the divergence between them as
a fact rather than a refusal. The guard hook binds the caller when it lets exec
through, keyed on the session and subagent its agent host names, so a subagent's
exec never binds its parent; a host that names neither binds the checkout. A
caller holding a job still declared or running is refused another; once its job
has exited or ended, the next exec takes the next job.

exit returns a job with its result, FILED ONTO THE JOB so whoever waits on it
reads the same record from any checkout. The run behind the result's output ref is
resolved in the holder's own checkout and its record filed alongside, because an
output store belongs to a cache dir and nobody else can reopen it. Do the work
first, then file the result: it is a record of what happened, not a form to fill
in while you are still deciding what to do. With no --stdin the job is abandoned
and recorded no_return, which is not the same as returning it and failing.

wait verifies what comes back. It reads the filed result and verifies EVIDENCE,
not claims: every changed path inside the declared write paths and outside the
denied ones, a change set that is not empty on a job that writes, descendants the
store carries, and every declared goal held: a recorded passing run for a check,
the diff since the checkpoint and the knowledge graph for paths and symbols.
Evidence must have been captured after the job was declared, so an old green run
cannot satisfy new work. A target's own run policy bounds its execution; wait does
not add a competing wall-clock timeout. There is no field for whether the holder
thinks it passed. A job that verifies is recorded pass. Exit 1 is a status, naming
every rule that failed; exit 2 is magus unable to answer, which is a result that
would not decode, nothing filed and nothing piped in, or a job that would not
write. Whether the work is GOOD stays the reading of whoever forked it.

run submits one of the server's own jobs, the housekeeping magus does for itself,
and returns. It is a no-op when no server is running, so a VCS hook can
call it unconditionally.

apply upserts jobs' specs from -f <file|->, the way kubectl apply reads a
manifest: the record is the whole spec, and the job keeps its state, holder and
registration, where a re-fork would hand a taken job out again. Only the
orchestrator widens; a holder may apply only the release of its own paths.

rm removes one row that should never have been written. prune ENDS every job
nobody is working, as exit would abandon it, and keeps each row as the record.

pause holds jobs' holders off the machine budget: their next magus run is refused
until resume. A run already holding the budget finishes. A job's weight, in (0,1]
on its record, leans its runs' retries ahead of other waiters; it is a lean, never
a place in a queue.

Reading is elsewhere, on the verbs that read everywhere else: magus ls jobs lists
them and magus describe job prints one job's terms.`,
	Usage: "magus job <fork|apply|exec|exit|wait|watch|run|rm|prune|pause|resume> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "fork",
			Short: "Declare one job, from flags or a JSON record on stdin",
			Description: "Declare one job: what its holder is handed, where it may write, and the one check it runs. " +
				"It refuses a job whose write paths cover a file the workspace has to LOAD (any project's magusfile.buzz, " +
				"its magus.yaml, or a spell source a magusfile imports) while another live job with write paths is bound " +
				"to this checkout, because a half-saved one of those stops the workspace loading for every job here at " +
				"once: give that job its own worktree. Every other fork records what it could prove about its write paths against " +
				"the jobs already here, in write_proof, which `magus ls jobs` prints. The whole record is validated at once: " +
				"a fork with problems lists every one with its code and fix and stores nothing, and the global --dry-run " +
				"does the same without forking, printing the record as it would be stored and exiting 1 while a problem " +
				"remains. A waiver in the record keeps a broad write path, an output a target regenerates outside the " +
				"write paths, or a ci target the job does not run.",
			Flags: []cmdline.Flag{
				{Name: "schema", Kind: cmdline.Bool, Doc: "Print the JSON schema a job must satisfy, and exit"},
				{Name: "stdin", Kind: cmdline.Bool, Doc: "Read one job as JSON on stdin instead of taking it from flags"},
				{Name: "criteria", Kind: cmdline.String, Doc: "What this job is for and what done means, as prose; the machine-checkable half is --check, and the goals a --stdin record declares"},
				{Name: "timeout", Kind: cmdline.Duration, Doc: "Deny this job's writes once this long has passed since the fork (e.g. 45m, 2h); unset means no bound, unless magus.yaml sets jobs.default_timeout"},
				{Name: "parent", Kind: cmdline.String, Doc: "The job this one is forked from"},
				{Name: "checkpoint", Kind: cmdline.String, Doc: "The working state this job is handed, as `magus vcs checkpoint -o name` prints it"},
				{Name: "write-paths", Kind: cmdline.Custom, Doc: "A path this job may write, or `<file>#<declaration>` to claim one declaration of a file (a path holding a literal # is spelled `./a#b.md` or `a\\#b.md`); repeatable or comma-separated"},
				{Name: "deny-paths", Kind: cmdline.Custom, Doc: "A path this job may not write, carved out of its write paths; repeatable or comma-separated"},
				{Name: "read-paths", Kind: cmdline.Custom, Doc: "A path whose projects this job may read; repeatable or comma-separated (additive: the written paths are readable already)"},
				{Name: "depends-on", Kind: cmdline.Custom, Doc: "A job this one waits on; repeatable or comma-separated"},
				{Name: "check", Kind: cmdline.String, Doc: "The one check this job runs, as `<target> <project> [-- args]` (the `magus run` is implied)"},
				{Name: "model", Kind: cmdline.String, Doc: "The model tier, or the model, the work was matched to; unset, the workspace's matrix or default chooses the tier"},
				{Name: "brief-format", Kind: cmdline.String, Doc: "The format a brief to this job's worker takes, markdown or buzz; unset, magus.yaml's agents.brief_format decides"},
				{Name: "read-only", Kind: cmdline.Bool, Doc: "A job that gathers evidence and writes nothing"},
				{Name: "checkout", Kind: cmdline.String, Doc: "Make a new checkout of this repository for the job at this directory, outside the workspace; the job owns it, and magus removes it once the job ends unless it holds work"},
				{Name: "fix", Kind: cmdline.Bool, Doc: "Make every edit a problem's fix names without judgment (add a write path, a goal or the integrator's waiver), validating again until none is left, and list only the problems that remain"},
			},
		},
		{
			Name:  "apply",
			Short: "Upsert jobs' specs from a file or stdin, keeping each job's state",
			Description: "Upsert each record's spec, the way `kubectl apply -f` reads a manifest: criteria, write, deny and " +
				"read paths, check, goals, model, depends_on, parent and timeout. The record is the whole spec, so a spec " +
				"field it leaves out is cleared, except an omitted checkpoint or timeout, which keeps the job's. Status is " +
				"never written: the job keeps its state, holder and registration, and a record carrying state is refused. " +
				"A new id creates the job. -f - reads stdin, and a stream holds one JSON job, an array, or one job per " +
				"line; every record is checked, with fork's rules for what it adds, before any is written, and every record's " +
				"problems are listed at once with their codes. A path the " +
				"orchestrator drops from a taken job is recorded as a revoked release carrying the digest of what the job " +
				"left. Widening is the orchestrator's; a session holding a lease may apply only a spec that releases some " +
				"of its own paths. The global --dry-run prints the spec diff and writes nothing.",
			Flags: []cmdline.Flag{
				{Name: "f", Kind: cmdline.String, Doc: "The records to apply: a file, or `-` for stdin; one JSON job, a JSON array, or one job per line"},
				{Name: "fix", Kind: cmdline.Bool, Doc: "Make every edit a problem's fix names without judgment, as job fork --fix does, before applying the records"},
			},
			Usage: "magus job apply -f <file|->",
		},
		{
			Name:        "exec",
			Short:       "Take the lease on a job here, and record the base this checkout landed on",
			Description: "Record the base this tree is on, as `magus vcs checkpoint -o name` prints it, and the checkout the job was taken in. The guard binds the caller to the job by the session and subagent its host names, else by this checkout.",
			Flags: []cmdline.Flag{
				{Name: "timeout", Kind: cmdline.Duration, Doc: "Run the command after -- and stop its whole process group once this long has passed (e.g. 20m); exits 124 at the deadline"},
				{Name: "slots", Kind: cmdline.Int, Default: 1, Doc: "With --timeout: the machine budget slots the command takes while it runs"},
			},
		},
		{
			Name:  "exit",
			Short: "Return a job with its result, or abandon it",
			Flags: []cmdline.Flag{
				{Name: "schema", Kind: cmdline.Bool, Doc: "Print the JSON schema a result must satisfy, and exit"},
				{Name: "stdin", Kind: cmdline.Bool, Doc: "Read this job's result from stdin; without it the job is abandoned"},
			},
		},
		{
			Name:  "wait",
			Short: "Verify the result a job was exited with",
			Flags: []cmdline.Flag{
				{Name: "schema", Kind: cmdline.Bool, Doc: "Print the JSON schema a result must satisfy, and exit"},
				{Name: "stdin", Kind: cmdline.Bool, Doc: "Read the result from stdin instead of from the job, for one that was never filed"},
				{Name: "integration", Kind: cmdline.Bool, Doc: "Grade only the job's check goals against the runs the --stdin result names in THIS checkout, and record that beside its state"},
				{Name: "evidence", Kind: cmdline.Custom, Doc: "Grade check goal `<goal>` against the run `<ref>` recorded in THIS checkout instead of the job's own; `check` names the job's check. The changed paths must read the same here as in the job's checkout; repeatable"},
			},
		},
		{
			Name:        "watch",
			Short:       "Follow what a job's holder is doing, until interrupted",
			Description: "Print one line per event as it happens: files changed under the job's declared write paths, tool calls the guard observed under its lease, and the runs magus recorded against it, merged in time order.",
			Long: `Follow one job's holder without asking it anything.

The three sources are the filesystem, the guard's activity trail, and the job's
own recorded runs. None of them needs the holder to cooperate or even to notice,
which is the point: messaging a worker to ask how it is going costs it the turn
it was in the middle of, and the answer you get is the worker's account of
itself rather than what happened.

A changed file is attributed to the job whose declared write paths cover it. The
write paths are disjoint, so the attribution is exact and needs nothing from the
worker; where two live jobs somehow cover one path the line says so and
attributes it to neither, because there is nothing in a path to break the tie.

It reads this checkout and needs no server. The same feed is served over the
console's Jobs view, which every verb that names a job prints a link to.` + "\n\n`magus describe job <job>`" + ` grades what a job has finished; this
shows what it is doing.`,
			Usage: "magus job watch <job>",
		},
		{Name: "run", Short: "Submit one of the server's own jobs and return"},
		{
			Name:  "rm",
			Short: "Remove one job from the plan",
			Description: "Remove ONE job from the plan, leaving every other row alone. This is not how a job ends: " +
				"`magus job exit` records what happened and leaves the row as the account of it, while rm is for a row " +
				"that should never have been written. A row that already ended is refused unless --force, because " +
				"deleting it destroys the only record that the work ran. The dropped rows are archived beside the plan first.",
			Flags: []cmdline.Flag{
				{Name: "force", Kind: cmdline.Bool, Doc: "Remove a row that already ended, destroying the record of what happened"},
			},
			Usage: "magus job rm <job> [flags]",
		},
		{
			Name:  "prune",
			Short: "End every job nobody is working",
			Description: "End, as no_return with an end_reason, every job the store can show nobody is working: exited " +
				"and never collected with `magus job wait`, overdue, orphaned by an ancestor that ended, or declared and " +
				"never taken past jobs.stale_after. Each ended job prints with its reason, then the count. A job a holder " +
				"took and touched within jobs.stale_after is never ended, nor is its parent. --all also ends a taken job " +
				"nobody touched within jobs.stale_after. The global --dry-run lists what would end and ends nothing.",
			Flags: []cmdline.Flag{
				{Name: "all", Kind: cmdline.Bool, Doc: "Also end a job a holder took and nobody touched within jobs.stale_after"},
			},
			Usage: "magus job prune [--all] [flags]",
		},
		{
			Name:  "pause",
			Short: "Hold jobs' holders off the machine budget until resumed",
			Description: "Mark each job paused, so `magus job exec` and the guard refuse its holder's next magus run with one " +
				"line naming who paused it, why, and `magus job resume <job>`. The pause records its owner (the caller's " +
				"session, or the person at the terminal) and --reason. While it holds, the read sweep and `magus job prune " +
				"--all` leave the row alone; past --until the pause lapses and both may end it again. Nothing is preempted: " +
				"a run already holding the budget finishes. A holder may not pause or resume a job; that is a person's or " +
				"the orchestrator's. Pausing is how a person gets one job out of a bind without stopping every other.",
			Flags: []cmdline.Flag{
				{Name: "reason", Kind: cmdline.String, Doc: "Why the job is paused, which the holder's refusal and the row show (required)"},
				{Name: "until", Kind: cmdline.String, Doc: "Lapse the pause after this long (2h) or at this RFC3339 instant; unset, it holds until resumed"},
			},
			Usage: "magus job pause <job>... --reason <text> [--until <duration|time>] [flags]",
		},
		{
			Name:  "resume",
			Short: "Lift the pause on jobs",
			Usage: "magus job resume <job>... [flags]",
		},
		{
			Name:  "grant",
			Short: "Lower one guard rule to advice for one job, typed by a person",
			Description: "Record a person's one-time approval: the guard advises instead of refusing the named rule for the " +
				"job's calls alone, until --for passes or the job ends, and the advice names who granted it and until when. " +
				"--revoke ends a grant early. Only a grantable rule is accepted (hooks-off); any other is refused, naming " +
				"the grantable ones. The guard refuses this verb to every agent and the job store refuses it from a process " +
				"carrying a lease, so it is typed at a person's own terminal.",
			Flags: []cmdline.Flag{
				{Name: "for", Kind: cmdline.Duration, Doc: "End the grant after this long (e.g. 2h); unset, it lasts until the job ends"},
				{Name: "revoke", Kind: cmdline.Bool, Doc: "End the job's grant of the rule"},
			},
			Usage: "magus job grant [--for <duration>] [--revoke] <job> <rule> [flags]",
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Declare a job", Command: "magus job fork session-load/core --write-paths internal/sessions/sessions.go --check 'test internal/sessions'"},
		{Comment: "Declare it from a record", Command: "magus job fork --stdin < job.json"},
		{Comment: "Take it in this checkout", Command: "magus job exec session-load/core"},
		{Comment: "Bound a long command", Command: "magus job exec --timeout 20m -- rg -l TODO ~/src"},
		{Comment: "Return it with its result", Command: "magus job exit session-load/core --stdin < result.json"},
		{Comment: "Verify what came back", Command: "magus job wait session-load/core"},
		{Comment: "Print the result schema", Command: "magus job exit --schema"},
		{Comment: "Submit a server job", Command: "magus job run sync-graph"},
		{Comment: "Change a live job's spec, keeping its state", Command: "magus job apply -f job.json"},
		{Comment: "Preview that change", Command: "magus job apply -f job.json --dry-run"},
		{Comment: "Grade a job's checks in a merged tree", Command: "magus job wait session-load/core --integration --stdin < evidence.json"},
		{Comment: "See which jobs a prune would end", Command: "magus job prune --dry-run"},
		{Comment: "Hold a job's runs off the budget, then let them back", Command: "magus job pause session-load/core --reason 'release build needs the machine' --until 2h && magus job resume session-load/core"},
	},
}

var gateCommand = cmdline.Command{
	Name:        "gate",
	Short:       "A person's decisions about the local gate",
	Description: "Record a person's waiver of the push gate for one commit, or revoke it.",
	Tags:        []string{"cli", "magus gate", "gate", "push", "waiver"},
	Usage:       "magus gate <waive> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "waive",
			Short: "Let one commit be pushed with no passing gate, typed by a person",
			Description: "Record a person's waiver for exactly one commit: push-ungated lets a push or `gh pr create` " +
				"of that commit through and names who waived it, when and why; any other commit is still refused. " +
				"--revoke removes the waiver. The guard refuses this verb to every agent and the store refuses it " +
				"from a process carrying a lease, so it is typed at a person's own terminal.",
			Flags: []cmdline.Flag{
				{Name: "reason", Kind: cmdline.String, Doc: "Why the gate was skipped, recorded with the waiver; required unless --revoke"},
				{Name: "revoke", Kind: cmdline.Bool, Doc: "Remove the commit's waiver"},
			},
			Usage: "magus gate waive <commit> (--reason <text> | --revoke) [flags]",
		},
	},
	Examples: []cmdline.Example{
		{Comment: "Push HEAD without the local gate, saying why", Command: `magus gate waive HEAD --reason "CI gates this release branch"`},
		{Comment: "Take the waiver back", Command: "magus gate waive --revoke HEAD"},
	},
}

var diffCommand = cmdline.Command{
	Name:        "diff",
	Short:       "Read the working tree's changes in the order they deserve attention",
	Description: "Report every uncommitted change annotated with what the workspace knows: whether it is generated, how widely its changed symbols are referenced, whether its symbols are public API, and what coverage was observed.",
	Tags:        []string{"cli", "magus diff", "diff", "review", "changeset", "semver"},
	Long: `Read the working tree's uncommitted changes, annotated and ordered.

A changeset is not a list of files, it is a set of CONSEQUENCES, and a reader's
attention is scarce. Alphabetical order spends it at random: it gives a
regenerated lockfile the same weight as a signature change twelve packages
depend on. This orders by what a change can BREAK.

Generated files - declared target outputs - are folded away by default. Reading
one is reading a machine's restatement of a change made somewhere else, so the
source edit is the review. Pass --generated to see them anyway.

At a terminal this opens an interactive viewer: the same annotations, plus
navigation and a way to mark what you have read. It is the same report either
way - the viewer renders the identical annotation lines - so nothing is hidden
behind a keypress, and ] and [ walk hunks while q leaves.

The viewer stands aside on its own wherever it cannot draw: no terminal, -o json,
--watch, a patch argument, or --impact. Those are not refusals, they are the
report printing instead, so a script or an agent needs no flag. --no-tui is for
a person who wants the report at a terminal anyway, and
"magus config set key=diff.tui,value=false" makes that the standing preference.

One rule decides what a word on the command line means: the CHANGESET is always
named by a flag - --rev, --patch, or the working tree by default - and every
positional is a PATH that narrows it. So "magus diff internal/ledger/" reads only
that subtree, and it means the same thing whichever source it is narrowing.

The patch source moved behind --patch for that rule. A bare - still reads stdin,
because it is unambiguous and it is what every pipe in the world already types.

--rev reads a committed range as base...head rather than the working tree, which
is how you review a branch: a colleague's, or the one your own agent just
finished. It is the half a working-tree diff cannot reach, and it keeps the
viewer, the navigation and the marks, because a revision is a tree state magus
can address rather than a patch somebody handed it. Three dots: the answer is
what head added since it diverged, never what the base gained meanwhile.

A receipt earned against a range attests to the blob at that revision, so it
survives the working tree moving underneath and does not follow the branch when
somebody force-pushes it. That is the whole difference from a working-tree
receipt, and it is why --ack takes --rev where it refuses a patch: magus can name
what a range receipt covers, and cannot name what a patch on stdin covers.

Each file carries the evidence behind its rank: how many files reference the
widest changed symbol it defines, whether any of those referents cross a project
boundary or the module boundary (which is the question a version bump turns on),
and the coverage a prior run observed. None of it is a verdict. magus does not
claim a change is breaking - deciding that needs signature compatibility, which
needs a base-side index magus does not keep and language semantics it does not
model - it reports who can see the thing you changed and lets you decide.

The console's Diff app reads the same annotations over the same session,
and an agent can join that session through the diff MCP tool.

--impact appends the blast radius of landing the change: which projects rebuild
and which were merely edited, who has been changing them, an estimate of the
rebuild from recorded run durations, what the workspace's advisors say, which
human-authored notes anchor a file or symbol you touched, and what the authors
asked magus while writing it. It is the same question magus affected --impact
answers, asked of a changeset instead of a target. It is context and never a
verdict - nothing is gated on it and the exit code is unchanged; neither the
flag nor the section it prints says "preflight", because that word names a gate
and this must never read as one. Each
section says when it could not measure something, so an empty one reads as
"nobody looked" rather than as a clean bill of health.

--impact also carries a REVIEW section, which is a bookmark rather than a
score. It reports the two things a reader cannot produce without reading:
files that changed AFTER they were read, and files never opened, widest
blast radius first. It reports no ratio and stays silent on a small change
nobody has disturbed - a count with a target is a count that gets cleared
instead of satisfied.

--thread <id> narrows the review to one thread on the pull request, the way a
path narrows it to one file. At a terminal the viewer opens on the thread's
hunk. Otherwise it prints the conversation oldest first, the hunk it is
about, and what the change there reaches: the changed symbols with the files
that reference them, who they are public to and the callers that reach them,
the coverage, the conformance findings, and the notes anchored to the file.
-o json carries the same record. The id is the thread id, its first comment's
id, or any reply's id. While the server runs, the report lists each thread's
id beside the hunk it sits on, and -o json carries them under each file's
"threads"; without it the report lists none and says so. The comments
are quoted from the host as other people's words. It does not combine with
--ack, --unread or --impact.

The report ends with the reading order: the hunks grouped by what links them, a
definition before its uses, an interface before its implementations, code before
its tests, each with the sentence that placed it. -o json carries the same order
under "order". It is absent, with a note naming the rebuild, when the symbol index
is not current, because an order drawn without the uses would read as "nothing is
related".

--unread narrows the report to the hunks no read mark covers, under every -o:
-o name prints one path:start-end per unread hunk, and -o json is the same
document, filtered, with an "unread" record saying how many were kept. It
always exits 0; a push that blocked on it would be a gate, and a gate on a read
count is the metric the paragraph below refuses. Where the marks cannot be read
it says the read state is unknown, on stderr and as "read_state": "unknown",
and calls no hunk unread. The drift notice a push hands off reports the same
count for the range being pushed.

A receipt covers a file at its CURRENT content, so editing it afterwards
voids the receipt. Stepping a file through in the viewer earns one; --ack covers
the changeset at once and takes an optional --reason kept with it. magus
never infers a receipt from an editor or a session: a measure satisfied by
scrolling would launder skimming into review. --ack refuses without a
terminal, and agent hosts are denied it outright.

The count is never shown to anyone but the reader. There is no team view and
no pull-request comment, because a read measure a second person can see is a
performance metric, and a performance metric gets gamed rather than met.`,
	Usage: "magus diff [--generated] [--impact] [--no-tui] [--watch] [--unread] [--thread <id>] [--rev <base>...<head>] [--patch <file>|-] [<path>...] [flags]",
	Flags: []cmdline.Flag{
		{Name: "generated", Kind: cmdline.Bool, Doc: "Include declared target outputs, which are folded away by default"},
		{Name: "impact", Kind: cmdline.Bool, Doc: "Append the blast radius of landing this: reach, ownership, an estimate from recorded run times, advisors, note anchors, and the evidence the authors consulted"},
		{Name: "no-tui", Kind: cmdline.Bool, Doc: "Print the report instead of opening the interactive viewer"},
		{Name: "watch", Kind: cmdline.Bool, Doc: "Re-read and re-render whenever the working tree changes"},
		{Name: "ack", Kind: cmdline.Bool, Doc: "Record that you have read the changed files at their current content; --impact reports what carries no such record"},
		{Name: "reason", Kind: cmdline.String, Doc: "An optional note kept with an --ack, for the next reader of the report"},
		{Name: "thread", Kind: cmdline.String, Doc: "Narrow the review to one pull request thread, by its id or the id of any comment in it, which the report lists beside each hunk while the server runs. The viewer opens on its hunk; printed, it shows the conversation, the hunk and its reach"},
		{Name: "unread", Kind: cmdline.Bool, Doc: "Narrow the report to the hunks no read mark covers, under every -o; -o name prints one path:start-end per hunk. Always exits 0. Where the marks cannot be read it says the read state is unknown and calls no hunk unread"},
		{Name: "rev", Kind: cmdline.String, Doc: "Review a committed range instead of the working tree, as base...head: a colleague's branch, or your agent's finished work"},
		{Name: "patch", Kind: cmdline.String, Doc: "Review a patch somebody handed you instead of the working tree; `-` reads stdin"},
		{Name: "baseline", Kind: cmdline.String, Doc: "The base's `graph export --symbols -o json`: adds what each changed symbol did to the API and the smallest semver bump that proves"},
		{Name: "conformance-min-cohort", Kind: cmdline.Int, Doc: "How many declarations a conformance norm needs before a changed symbol is compared against it; defaults to knowledge.conformance.gates.min_cohort"},
		{Name: "conformance-min-share", Kind: cmdline.Custom, Doc: "The share of a conformance norm's declarations, above 0 and at most 1, that must agree before it is reported; defaults to knowledge.conformance.gates.min_share"},
	},
	Examples: []cmdline.Example{
		{Comment: "Read what you are about to commit", Command: "magus diff"},
		{Comment: "Review a branch somebody else pushed", Command: "magus diff --rev main...feat/audience"},
		{Comment: "Narrow it to one subtree", Command: "magus diff internal/ledger/"},
		{Comment: "Include the generated files too", Command: "magus diff --generated"},
		{Comment: "Everything to know before landing it", Command: "magus diff --impact"},
		{Comment: "Print the report instead of opening the viewer", Command: "magus diff --no-tui"},
		{Comment: "Read only the hunks of a branch you have not marked read", Command: "magus diff --unread --rev main...HEAD"},
		{Comment: "Open the review on one pull request thread", Command: "magus diff --thread 2193847561"},
		{Comment: "Machine-readable, for a script or a Buzz advisor", Command: "magus diff -o json"},
	},
	// Documented because git trained everyone to expect the opposite: there is no
	// --exit-code here, and a script testing the status for "anything changed?"
	// silently never fires.
	ExitStatus: []cmdline.ExitCode{
		{Code: 0, Meaning: "The changeset was read and rendered. This is the status whether or not anything changed: unlike git diff --exit-code, a non-empty changeset is not a failure, and no flag makes it one."},
		{Code: 1, Meaning: "The changeset could not be read - an unreadable patch file, or stdin, or a working tree the VCS would not report on."},
		{Code: 2, Meaning: "Misuse: more than one patch argument, or an argument that is neither a readable patch nor -. The viewer never causes this: where it cannot draw - no terminal, -o json, --watch, a patch argument, --impact - it stands aside and the report prints instead."},
	},
}

var notesCommand = cmdline.Command{
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
	Usage: "magus notes <ls|get|edit|verify|capture> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "ls",
			Short: "Show notes and any repair warnings",
			Flags: []cmdline.Flag{
				{Name: "shared", Kind: cmdline.Bool, Doc: "Only notes committed to this repository (your team has these)"},
				{Name: "private", Kind: cmdline.Bool, Doc: "Only your own notes"},
				{Name: "reproducible", Kind: cmdline.Bool, Doc: "Omit values that are real but unstable (file mtimes, absolute paths), so the output can be committed"},
				{Name: "tag", Kind: cmdline.Custom, Doc: "Only notes carrying this tag; repeatable, a note must carry every one (gap/open lists open gaps)"},
			},
		},
		{Name: "get", Short: "Show one note"},
		{Name: "edit", Short: "Open one note in $VISUAL or $EDITOR"},
		{Name: "verify", Short: "Check malformed notes and anchors that no longer resolve"},
		{
			Name:  "capture",
			Short: "Capture the review under way as a note: your own remarks plus any colleagues' comments",
			Flags: []cmdline.Flag{
				{Name: "title", Kind: cmdline.String, Doc: "Title for the note (defaults to naming the reviewed base)"},
				{Name: "name", Kind: cmdline.String, Doc: "Note name (defaults to review-<patch digest>)"},
				{Name: "tag", Kind: cmdline.Custom, Doc: "Tag to set on the note; repeatable"},
				{Name: "gap", Kind: cmdline.Bool, Doc: "Capture a harness gap: positional arguments are the refs it was seen in, and every --anchor must resolve"},
				{Name: "anchor", Kind: cmdline.Custom, Doc: "With --gap, what the gap is about (repeatable): symbol:<key>, file:<path>#<decl>, diagnostic:<code>, or any note anchor"},
				{Name: "shared", Kind: cmdline.Bool, Doc: "Only notes committed to this repository (your team has these)"},
				{Name: "private", Kind: cmdline.Bool, Doc: "Only your own notes (default for capture)"},
			},
		},
	},
	Examples: []cmdline.Example{
		{Comment: "List every note", Command: "magus notes ls"},
		{Comment: "Read one note", Command: "magus notes get cache-invalidation-pairing"},
		{Comment: "Write or revise one", Command: "magus notes edit cache-invalidation-pairing"},
		{Comment: "Check every anchor still resolves", Command: "magus notes verify"},
		{Comment: "Capture the review under way", Command: "magus notes capture"},
		{Comment: "Record a harness gap that magus can find again", Command: "magus notes capture --gap --shared --title \"wait poll denied as sleep\" --anchor diagnostic:MGS3009 grd6392b52c2ea13c5a"},
		{Comment: "List open gaps with their find lines", Command: "magus notes ls --tag gap/open"},
	},
}

var buzzCommand = cmdline.Command{
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

A script run inside a workspace runs under that workspace's sandbox policy,
the same one a target gets, so its file, process and network access is
governed identically and a refusal is recorded on the activity trail.

Parsing is upstream-strict by default: a file written for the magusfile
engine needs --embedded, or it fails on rules upstream Buzz enforces and
magus does not. The most common one is "argument N must be labeled".

-t runs a file's test blocks and reports pass or fail, which is how Buzz
code in this ecosystem is tested. --coverprofile writes an LCOV report for
the file under -t (entry file only; imports are measured when they are the
-t subject). The lsp subcommand speaks the Language Server Protocol over
stdio for an editor integration.

--check parses and type-checks the named files and does not run them, which
is the only way to judge a script whose whole job is a side effect: a hook
that reads stdin and shells out cannot be validated by running it. It takes
several paths, reports every diagnostic rather than stopping at the first,
and fails only on errors; warnings print and pass. Resolving a file import
still executes that module's top level, since there is no check-only import
pass.

It cannot resolve magus/spell/*, which the workspace loader binds, so a
magusfile or a target definition reports an unresolved import. Those are
the files magus already checks by loading them; --check is for the ones
nothing loads.

-C changes the working directory first, in every mode, so one command line
runs the same from any directory: magus buzz -C "$ROOT" -s hooks/guard.buzz.`,
	Flags: []cmdline.Flag{
		// Backticks are load-bearing: flag.UnquoteUsage reads the quoted word as the
		// value's name, so this renders `-e code` rather than `-e string`. The
		// hand-written binding had them and the registry copy did not, which is the
		// kind of difference two lists drift into when only their names are compared.
		{Name: "e", Kind: cmdline.String, Doc: "Execute `code` given on the command line instead of a file"},
		{Name: "t", Kind: cmdline.Bool, Doc: `Run the file's test "..." {} blocks and report pass/fail`},
		{Name: "test", Kind: cmdline.Bool, AliasOf: "t", Doc: "Alias for -t"},
		{Name: "check", Kind: cmdline.Bool, Doc: "Parse and type-check the named files without running them; report every diagnostic"},
		{Name: "coverprofile", Kind: cmdline.String, Doc: "Write an LCOV coverprofile for the file under `-t` (requires `-t`)"},
		{Name: "profile", Kind: cmdline.Bool, Doc: "Print where compile and import time went, after the script runs"},
		{Name: "record", Kind: cmdline.Bool, Doc: "Keep what the script printed in the output store and print its ref, so a plan or a review can cite the run (`magus query output <ref>` reopens it)"},
		{Name: "embedded", Kind: cmdline.Bool, Doc: "Relax upstream strictness (top-level statements, optional argument labels) to match the magusfile engine"},
		{Name: "no-autoload", Kind: cmdline.Bool, Doc: "Start the REPL without executing the magusfile"},
		{Name: "C", Kind: cmdline.String, Doc: "Change to `dir` before anything else, as go -C does, so script paths and imports resolve from it; lsp takes it only as the first flag"},
	},
	Usage: "magus buzz [-C dir] [file...|-|lsp] [flags]",
	Children: []cmdline.Command{
		{Name: "lsp", Short: "Language server over stdio (LSP)"},
	},
	Examples: []cmdline.Example{
		{Comment: "Open a REPL with the magusfile loaded", Command: "magus buzz"},
		{Comment: "Run a script", Command: "magus buzz scripts/report.buzz"},
		{Comment: "Run an inline snippet", Command: `magus buzz -e 'import "std"; fun main() > void { std\print("hi"); } main();'`},
		{Comment: "Run a file's test blocks", Command: "magus buzz -t scripts/report.buzz"},
		{Comment: "Check files without running them", Command: "magus buzz --check scripts/report.buzz scripts/build.buzz"},
		{Comment: "Run a magusfile-style file", Command: "magus buzz --embedded scripts/target.buzz"},
		{Comment: "Write an LCOV coverprofile while testing", Command: "magus buzz -t --coverprofile=out.lcov scripts/report.buzz"},
		{Comment: "See which imports a script spends its time on", Command: "magus buzz --profile scripts/report.buzz"},
		{Comment: "Run a probe and keep its output to cite", Command: "magus buzz --record probes/does-the-key-move.buzz"},
		{Comment: "Run a root-relative script from any directory", Command: "magus buzz -C ~/src/app scripts/report.buzz"},
	},
}

var agentCommand = cmdline.Command{
	Name:        "agent",
	Short:       "Manage skills, harnesses, and agent feedback",
	Description: "Render agent skills, adapt a user-owned harness, or review recurring guard feedback; it never writes the AGENTS.md you own.",
	Tags:        []string{"cli", "magus agent", "skills", "agents", "AGENTS.md", "install", "harness"},
	Long: `Render the agent skills embedded in this binary and write or stream them
into named destinations (<skills-dir>).

magus never writes your AGENTS.md. That file is yours, and an installer that
edits a file you own leaves bytes you did not write and cannot audit. So
install PRINTS the managed magus block for you to paste, and only when your
AGENTS.md is missing it or is carrying a stale one. sample prints a starter
AGENTS.md to stdout for you to own and tweak, and never writes a file.

harness installs the skill trees of, or verifies, harnesses selected with
magus\harness.provider (several hosts are fine when you bounce between LLM
tools): verify actually runs the wired guard command against a synthetic
event rather than trusting its mere presence in the config. The host config
is yours for the same reason AGENTS.md is: magus describe harness prints the
entries it needs and the one command that merges them, and you run it. Omit
--id to act on every magusfile-wired provider. Guard feedback that keeps
recurring is doctor's recurring-guard-denials check, not a verb here.

agent is a pure data generator, which is what makes --tar the general
answer: it streams a tar archive to stdout, so skills can be installed
anywhere a shell can reach. The write-to-disk form exists for the in-repo,
paths-relative-to-<dir> case. Absolute destinations are refused unless
--global is set, so magus cannot silently write outside the working tree.

adoption reads shell commands, one per line, from stdin or from
--commands <file>, and reports how often the graph was reached versus a raw
text search. -o json emits the report as one object keyed total, graph_verbs,
text_searches, search_of_source, search_of_prose, file_reads, magus_runs,
other, and top_symbol_greps. Each top_symbol_greps entry carries pattern,
count, and run - the graph command to try for that pattern, routed by its
shape: magus query for a diagnostic code or a Buzz op, which magus refs
(compiled-language symbols only) would miss, and magus refs otherwise. The
text report prints the same command after each pattern, and run is empty for
a pattern no graph verb fits.`,
	Usage: "magus agent <install|harness|starter|adoption> [flags]",
	Children: []cmdline.Command{
		{Name: "install", Short: "Render the embedded skills and write or stream them into named destinations"},
		{Name: "harness", Short: "Install skills for, or verify, harnesses wired in the magusfile", Children: []cmdline.Command{
			{Name: "verify", Short: "Verify a descriptor and its configured hook file by actually probing the wired guard command", Flags: []cmdline.Flag{{Name: "id", Kind: cmdline.String, Doc: "Harness ID; omit to verify every magusfile-wired provider"}}},
			{Name: "install", Short: "Install skill trees declared by a harness", Flags: []cmdline.Flag{{Name: "id", Kind: cmdline.String, Doc: "Harness ID; omit to install every magusfile-wired provider"}}}}},
		{Name: "starter", Short: "Print a starter AGENTS.md to stdout; never writes a file"},
		{Name: "adoption", Short: "Report how often agents used the knowledge graph versus a raw text search", Flags: []cmdline.Flag{
			{Name: "commands", Kind: cmdline.String, Doc: "File of shell commands, one per line; without it commands are read from stdin"},
		}},
	},
	Flags: []cmdline.Flag{
		{Name: "dir", Kind: cmdline.String, Default: ".", Doc: "Repo directory to install into (agent install)"},
		{Name: "force", Kind: cmdline.Bool, Doc: "Overwrite existing installed skill files (agent install)"},
		{Name: "dry-run", Kind: cmdline.Bool, Doc: "Print what would be written and removed without touching the filesystem (agent install)"},
		{Name: "tar", Kind: cmdline.Bool, Doc: "Stream a tar archive to stdout instead of writing files (agent install)"},
		{Name: "global", Kind: cmdline.Bool, Doc: "Allow absolute destination paths in write mode (agent install)"},
		{Name: "skill-form", Kind: cmdline.String, Default: "both", Doc: "Skill form to install: both (default), short, or full (agent install)"},
	},
	Examples: []cmdline.Example{
		{Comment: "Install into a repository's agent skills directory", Command: "magus agent install .agents/skills"},
		{Comment: "Refresh installed skills", Command: "magus agent install .agents/skills --force"},
		{Comment: "See what an install would write and remove first", Command: "magus agent install .agents/skills --force --dry-run"},
		{Comment: "Install anywhere via tar", Command: "magus agent install --tar | tar -xf - -C .agents/skills"},
		{Comment: "Print a starter AGENTS.md", Command: "magus agent starter"},
		{Comment: "Measure graph adoption from shell commands", Command: "magus agent adoption --commands commands.txt"},
		{Comment: "Read commands from stdin", Command: "magus agent adoption < commands.txt"},
		{Comment: "The report as JSON, for a dashboard", Command: "magus agent adoption --commands commands.txt -o json"},
	},
}
