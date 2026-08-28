package main

// subcommand is one entry in magus's top-level surface.
type subcommand struct {
	Name  string
	Short string // the one-line description shown by `magus help`
	// Group is the heading `magus help` files this command under. Entries are listed
	// in slice order, so a group's members must be contiguous.
	Group string
}

// The groups `magus help` prints, in order.
const (
	groupWork      = "Projects and targets"
	groupKnowledge = "Knowledge graph"
	groupChanges   = "Changes and history"
	groupIntegrate = "Integrations and services"
	groupSetup     = "Setup and maintenance"
)

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
	{Group: groupWork, Name: "ls", Short: "list all discovered projects"},
	{Group: groupWork, Name: "describe", Short: "define a magus concept and list all entities (tools|targets|projects|workspaces|mcp-tools)"},
	{Group: groupWork, Name: "where", Short: "print the absolute path of a project (fuzzy match)"},
	{Group: groupWork, Name: "run", Short: "run a target for selected projects"},
	{Group: groupWork, Name: "affected", Short: "run a target for VCS-diff affected projects"},
	{Group: groupWork, Name: "x", Short: "interactive shorthand: pick project + target (TTY only)"},
	{Group: groupWork, Name: "clean", Short: "remove declared Outputs (regenerable build artifacts) [--cache to also drop entries]"},

	{Group: groupKnowledge, Name: "query", Short: "search the knowledge graph and show a node's neighborhood"},
	{Group: groupKnowledge, Name: "refs", Short: "list where an ingested code symbol is defined and referenced"},
	{Group: groupKnowledge, Name: "explain", Short: "show one knowledge-graph node: its edges, provenance, blast radius"},
	{Group: groupKnowledge, Name: "path", Short: "show the shortest path between two knowledge-graph nodes"},
	{Group: groupKnowledge, Name: "graph", Short: "the graphs as objects: deps (project DAG), export (knowledge graph), stats (shape)"},

	{Group: groupChanges, Name: "diff", Short: "read uncommitted changes in the order they deserve attention, generated folded"},
	{Group: groupChanges, Name: "vcs", Short: "staging and conflict resolution that knows what is generated (add, resolve, merge-driver, checkpoint)"},
	{Group: groupChanges, Name: "session", Short: "what sessions did and what they are blocked on: humans read (ls, attention) and dispose; hosts write (hook, notify)"},
	{Group: groupChanges, Name: "memory", Short: "durable cross-session project memory (ls, get, put, delete, verify)"},
	{Group: groupChanges, Name: "notes", Short: "human-authored notes committed to the repo (ls, get, edit, verify)"},

	{Group: groupIntegrate, Name: "watch", Short: "emit changed file paths (pipe into affected --stdin)"},
	{Group: groupIntegrate, Name: "events", Short: "stream workspace events as JSONL for an editor plugin or other integration"},
	{Group: groupIntegrate, Name: "server", Short: "manage the persistent daemon (start / stop / status; MCP starts with it)"},
	{Group: groupIntegrate, Name: "status", Short: "inspect the concurrency pool of a running parent magus"},
	{Group: groupIntegrate, Name: "buzz", Short: "run a Buzz script (Buzz stdlib + every magus host module)"},
	{Group: groupIntegrate, Name: "agent", Short: "install the knowledge-graph agent skills into a repo (agent install <dir>)"},

	{Group: groupSetup, Name: "init", Short: "bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver)"},
	{Group: groupSetup, Name: "doctor", Short: "validate the workspace"},
	{Group: groupSetup, Name: "config", Short: "view or update magus configuration"},
	{Group: groupSetup, Name: "completion", Short: "print a shell completion script (bash, zsh, fish)"},
	{Group: groupSetup, Name: "man", Short: "install the man pages embedded in this binary"},
	{Group: groupSetup, Name: "self", Short: "manage the magus binary (self update / install)"},
	{Group: groupSetup, Name: "version", Short: "print version, commit, and build date"},
	{Group: groupSetup, Name: "help", Short: "show this message"},
}

// knownSubcommands is the dispatcher's set, derived so it cannot disagree with help.
var knownSubcommands = func() []string {
	names := make([]string, 0, len(subcommands))
	for _, s := range subcommands {
		names = append(names, s.Name)
	}
	return names
}()
