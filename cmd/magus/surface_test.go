package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/hint"
)

// TestCLICommandHeadsAreRealSubcommands guards against the drift that shipped a
// hint for a command that no longer exists. Every canonical command referenced
// from user-facing output (hint.AllCommands) must have a head token that dispatchSub
// actually routes - knownSubcommands is that switch's own accept-list. Rename or
// remove a subcommand and forget to update hint's command registry, and this fails.
func TestCLICommandHeadsAreRealSubcommands(t *testing.T) {
	for _, c := range hint.AllCommands {
		if !slices.Contains(knownSubcommands, c.Head()) {
			t.Errorf("hint command %q references head subcommand %q, which is not in knownSubcommands %v",
				c, c.Head(), knownSubcommands)
		}
	}
}

// TestCLICommandPathsResolve checks the whole path, not just the head token the test
// above walks. That gap shipped three remedies naming command chains the dispatcher
// rejects: `magus config mcp token print` (the operator token moved to `config token`),
// `magus notes new <name>`, and a bare `magus ci`. Each had a real head, so nothing failed.
//
// Resolution walks internal/cli's tree, the same hand-maintained mirror
// TestManpageCoversEverySubcommand pins to knownSubcommands at the top level. A mirror is
// the strongest target available: outside graphSubs and describeAlias, every family
// dispatches on a bare switch with no accept-list to read, so an assertion against the
// router itself needs those switches restructured first. Where the two exceptions accept a
// spelling the registry does not carry, resolveCommandPath reads the dispatcher's data
// instead of the mirror's.
func TestCLICommandPathsResolve(t *testing.T) {
	for _, c := range hint.AllCommands {
		tokens := strings.Fields(strings.TrimPrefix(c.String(), "magus "))
		if err := resolveCommandPath(tokens); err != nil {
			t.Errorf("hint command %q: %v", c, err)
		}
	}
}

// resolveCommandPath reports whether tokens name a routable subcommand chain.
func resolveCommandPath(tokens []string) error {
	// describe and ls route their second token themselves against data internal/cli does
	// not mirror: describeAlias takes singular or plural where the registry documents one
	// spelling, and lsCmd matches "target"/"targets" while listCommand carries no children.
	if len(tokens) == 2 {
		switch tokens[0] {
		case "describe":
			if describeAlias[tokens[1]] == "" {
				return fmt.Errorf("describe noun %q is not one describeAlias accepts", tokens[1])
			}
			return nil
		case "ls":
			if tokens[1] == "target" || tokens[1] == "targets" {
				return nil
			}
		}
	}
	children := cli.All
	for i, tok := range tokens {
		idx := slices.IndexFunc(children, func(c cli.Command) bool { return c.Name == tok })
		if idx < 0 {
			parent := "magus " + strings.Join(tokens[:i], " ")
			return fmt.Errorf("%q is not a subcommand of %q - fix the hint, or add the entry to internal/cli/registry.go",
				tok, strings.TrimSpace(parent))
		}
		children = children[idx].Children
	}
	return nil
}

// TestCLICommandGraphLeavesAreRealSubcommands ties the graph-family hints to
// graphCmd's own accept-list. `graph` routes its second token positionally, so a
// hint like `magus graph export` is only valid while graphSubs still lists
// "export". This is the strongest guard the hand-rolled dispatch allows: graphSubs
// is the introspectable accept-list, not the switch itself, so it only catches
// drift if graphSubs stays in sync with graphCmd's switch (which SuggestNearest
// already depends on).
func TestCLICommandGraphLeavesAreRealSubcommands(t *testing.T) {
	for _, c := range []hint.Command{hint.GraphExport, hint.GraphStats} {
		if c.Head() != "graph" {
			t.Fatalf("expected a graph-family command, got %q", c)
		}
		if !slices.Contains(graphSubs, c.Leaf()) {
			t.Errorf("hint command %q references graph subcommand %q, which is not in graphSubs %v",
				c, c.Leaf(), graphSubs)
		}
	}
}

// TestCLICommandServerLeavesAreRealSubcommands ties the server-family hints to the
// tokens serverCmd routes on. serverCmd switches directly on hint.*.Leaf(), so
// the accepted form and the hint already share one source of truth; this asserts
// they remain the exact set start/stop/job/reload, catching a stray edit that renames
// one side only.
func TestCLICommandServerLeavesAreRealSubcommands(t *testing.T) {
	got := []string{hint.ServerStart.Leaf(), hint.ServerStop.Leaf(), hint.ServerJob.Leaf(), hint.ServerReload.Leaf()}
	want := []string{"start", "stop", "job", "reload"}
	if !slices.Equal(got, want) {
		t.Errorf("server leaves = %v, want %v", got, want)
	}
	for _, c := range []hint.Command{hint.ServerStart, hint.ServerStop, hint.ServerJob, hint.ServerReload} {
		if c.Head() != "server" {
			t.Errorf("hint command %q is not a server-family command", c)
		}
	}
}

// TestCLICommandQueryOutputForm locks the query-output hint to the form queryCmd
// accepts. queryCmd matches its output positional against hint.QueryOutput.Leaf(),
// so the hint (`magus query output <ref>`) and the accepted form cannot disagree -
// the exact bug that shipped `magus query <ref>`. This asserts the shape stays
// two-token (a bare `magus query` would reopen that gap) and renders as expected.
//
// Limit: dispatch is hand-rolled positional matching, not an introspectable
// command tree, so this cannot execute the router without a workspace. It guards
// the constant's shape and its rendering; the tie to acceptance is structural
// (queryCmd reads the same Leaf()), enforced at compile/review time rather than here.
func TestCLICommandQueryOutputForm(t *testing.T) {
	if hint.QueryOutput.Head() != "query" {
		t.Errorf("QueryOutput head = %q, want %q", hint.QueryOutput.Head(), "query")
	}
	if hint.QueryOutput.Leaf() != "output" {
		t.Errorf("QueryOutput leaf = %q, want %q", hint.QueryOutput.Leaf(), "output")
	}
	if got, want := hint.QueryOutput.With("out1a2b3c"), "magus query output out1a2b3c"; got != want {
		t.Errorf("QueryOutput.With(ref) = %q, want %q", got, want)
	}
	if got, want := hint.QueryOutput.With("out1a2b3c", "--open"), "magus query output out1a2b3c --open"; got != want {
		t.Errorf("QueryOutput.With(ref, --open) = %q, want %q", got, want)
	}
}

// TestManpageCoversEverySubcommand guards the drift that shipped `magus man` with pages for
// 21 of 30 subcommands, vcs among them. internal/cli/registry.go is a hand-maintained
// mirror of surface.go, and api_test.go cannot catch divergence: it locks api.lock against
// API(), which is generated from the same registry.
func TestManpageCoversEverySubcommand(t *testing.T) {
	// `magus help` prints what `magus` prints; man(1) has no page for it.
	const notDocumented = "help"

	documented := make(map[string]bool, len(cli.All))
	for _, c := range cli.All {
		documented[c.Name] = true
	}

	for _, s := range subcommands {
		if s.Name == notDocumented {
			continue
		}
		if !documented[s.Name] {
			t.Errorf("subcommand %q has no cli.All entry, so `magus man` installs no page for it "+
				"and the docs site renders none - add one to internal/cli/registry.go", s.Name)
		}
	}

	// A page for a command the dispatcher no longer routes is worse than a missing one:
	// manpage/magus-churn.1 is a committed artifact of exactly that.
	for _, c := range cli.All {
		if !slices.Contains(knownSubcommands, c.Name) {
			t.Errorf("cli.All documents %q, which knownSubcommands does not route - "+
				"remove the entry, or restore the subcommand", c.Name)
		}
	}
}
