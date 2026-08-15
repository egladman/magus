package main

// subcommand is one entry in magus's top-level surface.
type subcommand struct {
	Name  string
	Short string // the one-line description shown by `magus help`
}

// subcommands is the SINGLE source of truth for magus's top-level surface, in the
// order `magus help` lists them.
//
// Three copies of this list used to exist and all three had drifted: usage() had the
// descriptions, knownSubcommands had the names for did-you-mean matching, and the four
// completion scripts each had their own. `memory` reached knownSubcommands but never
// usage(), so the dispatcher accepted and suggested a command `magus help` did not
// list; `refs`, `memory` and `agent` reached neither completion script.
//
// magus-utils reads this declaration to generate the completion scripts, so a
// subcommand added here shows up in help, in did-you-mean, and in every shell without
// anyone remembering to update five files.
//
//go:generate go run ../magus-utils completions -surface surface.go -out completions
var subcommands = []subcommand{
	{Name: "ls", Short: "list all discovered projects"},
	{Name: "describe", Short: "define a magus concept and list all entities (tools|targets|projects|workspaces|mcp-tools)"},
	{Name: "run", Short: "run a target for selected projects"},
	{Name: "x", Short: "interactive shorthand: pick project + target (TTY only)"},
	{Name: "where", Short: "print the absolute path of a project (fuzzy match)"},
	{Name: "affected", Short: "run a target for VCS-diff affected projects"},
	{Name: "query", Short: "search the knowledge graph and show a node's neighborhood"},
	{Name: "explain", Short: "show one knowledge-graph node: its edges, provenance, blast radius"},
	{Name: "path", Short: "show the shortest path between two knowledge-graph nodes"},
	{Name: "refs", Short: "list where an ingested code symbol is defined and referenced"},
	{Name: "graph", Short: "the graphs as objects: deps (project DAG), export (knowledge graph), stats (shape)"},
	{Name: "watch", Short: "emit changed file paths (pipe into affected --stdin)"},
	{Name: "status", Short: "inspect the concurrency pool of a running parent magus"},
	{Name: "clean", Short: "remove declared Outputs (regenerable build artifacts) [--cache to also drop entries]"},
	{Name: "vcs", Short: "staging and conflict resolution that knows what is generated (add, resolve, merge-driver, checkpoint)"},
	{Name: "doctor", Short: "validate the workspace"},
	{Name: "config", Short: "view or update magus configuration"},
	{Name: "memory", Short: "durable cross-session project memory (ls, get, put, delete, verify)"},
	{Name: "notes", Short: "human-authored notes committed to the repo (ls, get, edit, verify)"},
	{Name: "server", Short: "manage the persistent daemon (start / stop / status; MCP starts with it)"},
	{Name: "buzz", Short: "run a Buzz script (Buzz stdlib + every magus host module)"},
	{Name: "completion", Short: "print a shell completion script (bash, zsh, fish)"},
	{Name: "man", Short: "install the man pages embedded in this binary"},
	{Name: "init", Short: "bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver)"},
	{Name: "agent", Short: "install the knowledge-graph agent skills into a repo (agent install <dir>)"},
	{Name: "hook", Short: "evaluate one shell command or file path against the magus guard rules (deny/advise/pass verdict)"},
	{Name: "notify", Short: "normalize an attention event and optionally notify the local desktop"},
	{Name: "self", Short: "manage the magus binary (self update / install)"},
	{Name: "version", Short: "print version, commit, and build date"},
	{Name: "help", Short: "show this message"},
}

// knownSubcommands is the dispatcher's set, derived so it cannot disagree with help.
var knownSubcommands = func() []string {
	names := make([]string, 0, len(subcommands))
	for _, s := range subcommands {
		names = append(names, s.Name)
	}
	return names
}()
