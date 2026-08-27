package playground

import (
	"sort"
	"strings"

	"github.com/egladman/magus/internal/cli"
)

// This file teaches the browser terminal the REAL magus CLI surface, read from
// the same command registry the binary, the man pages and the shell completions
// are generated from.
//
// It is a read of a declaration, not a second copy of one. internal/cli is
// pure data - it imports flag and time and nothing else - so it compiles to
// js/wasm unchanged, and the console gets every subcommand and flag magus
// actually has without listing any of them here.
//
// What the console still CANNOT do is run them. A browser has no processes, no
// filesystem and no git, which is why internal/proc/run/run_wasm.go and friends
// exist. So the registry buys three things that need no execution: completing a
// subcommand, completing its flags, and explaining what one does. Everything
// else stays the playground's own in-memory evaluation.

// playgroundVerbs are the console's own commands. They are NOT magus
// subcommands: eval, clear and about exist only here, and ls/graph/run are
// in-memory analogues of the real ones that dry-run the loaded magusfile
// instead of touching a workspace.
//
// Kept separate from the registry names below so the help can say which is
// which. Conflating them is what made the old hardcoded list claim that `eval`
// and `about` were "magus CLI subcommands".
var playgroundVerbs = []string{"help", "ls", "targets", "graph", "run", "eval", "version", "clear", "about"}

// cliCommands returns every real magus subcommand name, sorted.
func cliCommands() []string {
	out := make([]string, 0, len(cli.All))
	for _, c := range cli.All {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// completableCommands is the console's completion set: its own verbs first, then
// the real subcommands it does not already shadow.
//
// Deduplicated, and that is not cosmetic. ls, graph, run and version exist in
// BOTH lists - the console has an in-memory analogue of each - and a name listed
// twice reads to the completer as two candidates, so `ru<tab>` stopped
// completing `run ` and offered an ambiguous match against itself.
func completableCommands() []string {
	seen := make(map[string]bool, len(playgroundVerbs))
	out := make([]string, 0, len(playgroundVerbs))
	for _, v := range playgroundVerbs {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, c := range cliCommands() {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// cliCommand finds a registry command by name.
func cliCommand(name string) (cli.Command, bool) {
	for _, c := range cli.All {
		if c.Name == name {
			return c, true
		}
	}
	return cli.Command{}, false
}

// cliFlagNames returns the flag spellings a subcommand declares, each with its
// leading dashes, sorted. Children are included under "<cmd> <child>".
func cliFlagNames(name string) []string {
	c, ok := cliCommand(name)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(c.Flags))
	for _, f := range c.Flags {
		out = append(out, dashPrefix(f.Name)+f.Name)
	}
	sort.Strings(out)
	return out
}

// dashPrefix picks the prefix a flag is written with: -c for a single character,
// --compact otherwise. Go's flag package accepts either, but the CLI's own help
// and docs spell them this way and a completion that disagreed would teach the
// wrong form.
func dashPrefix(name string) string {
	if len(name) == 1 {
		return "-"
	}
	return "--"
}

// cliChildren returns a subcommand's navigational children (e.g. deps under graph).
func cliChildren(name string) []string {
	c, ok := cliCommand(name)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(c.Children))
	for _, ch := range c.Children {
		out = append(out, ch.Name)
	}
	sort.Strings(out)
	return out
}

// explainCLI renders what a real magus subcommand is, and says plainly that the
// browser cannot run it.
//
// This replaces the old fallthrough, which handed anything unrecognized to the
// Buzz evaluator: typing `doctor` reported `BZZ1001 undefined: doctor`, which
// describes a Buzz scope rather than the magus command the reader meant. A
// terminal that knows the whole CLI can say the true thing instead.
func explainCLI(name string) []Line {
	c, ok := cliCommand(name)
	if !ok {
		return nil
	}
	rows := []Line{
		{HTML: `<span class="muted">magus</span> <b>` + esc(c.Name) + `</b> &mdash; ` + esc(c.Short)},
	}
	if c.Usage != "" {
		rows = append(rows, Line{HTML: `  <span class="muted">` + esc(c.Usage) + `</span>`})
	}
	if kids := cliChildren(c.Name); len(kids) > 0 {
		rows = append(rows, Line{HTML: `  <span class="muted">subcommands:</span> ` + esc(strings.Join(kids, "  "))})
	}
	if flags := cliFlagNames(c.Name); len(flags) > 0 {
		rows = append(rows, Line{HTML: `  <span class="muted">flags:</span> ` + esc(strings.Join(flags, "  "))})
	}
	rows = append(rows, Line{
		Class: "muted",
		HTML: `  this page cannot run it: a browser has no processes, no filesystem and no git. ` +
			`Run it in a real workspace, or use the playground verbs (<b>help</b>) to explore this magusfile.`,
	})
	return rows
}

// completeCLI completes a magus flag for the subcommand on the line.
//
// Only flags: the positional argument of a real subcommand is a project path, a
// ref or a query against a workspace this page does not have, so offering one
// would be guessing. Flags are declared, so they are knowable.
func completeCLI(cmd, base string) []string {
	if !strings.HasPrefix(base, "-") {
		return nil
	}
	var out []string
	for _, f := range cliFlagNames(cmd) {
		if strings.HasPrefix(f, base) {
			out = append(out, f)
		}
	}
	return out
}
