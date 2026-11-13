package commands

import (
	"slices"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/clikit/command"
)

// Root is magus's command tree as a Command record: the program, its global
// flags, and its top-level commands in the order `magus help` lists them.
// The variables it reads are internal/config's Configuration, which this
// package does not import so the browser terminal compiles it to js/wasm.
var Root = apiversion.New(apiversion.Metadata{Name: "magus"}, command.CommandSpec{Command: command.Command{
	Name:        "magus",
	Short:       "workspace-aware build orchestrator and content-addressed cache",
	Usage:       "magus [flags] <subcommand> [args]",
	Description: "Standalone build orchestrator and content-addressed cache for polyglot monorepos, with workspace-aware subcommands for build, test, lint, and inspect.",
	Tags:        []string{"cli", "magus", "build", "monorepo", "orchestrator", "cache", "workspace"},
	Long: `magus is a standalone build orchestrator and content-addressed cache for
multi-language monorepos, and an evolution of Mage. It provides workspace-aware
subcommands for building, testing, linting, and inspecting projects without
requiring Mage to be installed.

magus reads optional configuration from magus.yaml (XDG, workspace root, or
CWD) and MAGUS_* environment variables. All configuration can be overridden
with CLI flags.

Global flags are accepted by every subcommand and may appear before or
after the subcommand word. Last-write-wins, matching kubectl conventions.`,
	Flags: []command.Flag{
		{Name: "root", Kind: command.String, Doc: "Workspace root `path`. Default: walk up from cwd until go.mod is found. Must precede the subcommand."},
		{Name: "config", Kind: command.String, Doc: "Config file `path`. Default: search for magus.yaml in CWD, workspace root, and $XDG_CONFIG_HOME/magus/. Must precede the subcommand."},
		{Name: "output", Kind: command.String, Doc: "Output `format`: text (default), json, yaml, name, jsonl, or template[=<go-template>]. Honored by subcommands that emit structured data. A template body renders a Go text/template over the same value -o json emits (field names are the json keys); a bare -o template with no body lists that output's fields instead of rendering - the json keys usable in -o json and -o template, with each field's type and doc."},
		{Name: "o", Kind: command.String, AliasOf: "output", Doc: "Short for --output; takes the same `format`"},
		{Name: "concurrency", Kind: command.Int, Doc: "Run at most `N` build steps at once. 0 means use the configured value (or MAGUS_CONCURRENCY, or concurrency_profile, balanced (min(NumCPU,8)) by default)."},
		{Name: "v", Kind: command.Bool, Doc: "Increase log verbosity. Repeat for more detail (-v, -vv, -vvv)."},
	},
	Files: []command.File{
		{Name: "magus.yaml, .magus.yaml", Doc: `Configuration file. Searched in CWD, workspace root, and
$XDG_CONFIG_HOME/magus/ in ascending priority order. Both plain and
dot-prefixed names are accepted; having both in the same directory is an error.`},
		{Name: ".magus/", Doc: "Content-addressed build cache in the workspace root. Override with MAGUS_CACHE_DIR."},
	},
	Children: slices.Concat(
		group("Projects and targets", listCommand, describeCommand, whereCommand, runCommand,
			affectedCommand, xCommand, cleanCommand, shellCommand),
		group("Knowledge graph", queryCommand, refsCommand, explainCommand, graphCommand),
		group("Changes and history", diffCommand, vcsCommand, sessionCommand, jobCommand, notesCommand, queueCommand),
		group("Background processes (start on demand)", serverCommand, mcpCommand),
		group("Integrations", watchCommand, eventsCommand, buzzCommand, spellCommand, agentCommand),
		group("Setup and maintenance", initCommand, doctorCommand, configCommand, selfCommand,
			versionCommand),
	),
}})

// Help is `magus help`: help lists it and the shells complete it, but it is not in
// Root because it prints what a bare `magus` prints and has no man page.
var Help = command.Command{Name: "help", Short: "Show this message", Group: "Setup and maintenance"}

// group files commands under one help heading, which keeps a group's members contiguous.
func group(heading string, commands ...command.Command) []command.Command {
	for i := range commands {
		commands[i].Group = heading
	}
	return commands
}
