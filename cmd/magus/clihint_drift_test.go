package main

import (
	"slices"
	"testing"

	"github.com/egladman/magus/internal/interactive/clihint"
)

// TestClihintHeadsAreRealSubcommands guards against the drift that shipped a
// hint for a command that no longer exists. Every canonical command referenced
// from user-facing output (clihint.All) must have a head token that dispatchSub
// actually routes - knownSubcommands is that switch's own accept-list. Rename or
// remove a subcommand and forget to update clihint, and this fails.
func TestClihintHeadsAreRealSubcommands(t *testing.T) {
	for _, c := range clihint.All {
		if !slices.Contains(knownSubcommands, c.Head()) {
			t.Errorf("clihint %q references head subcommand %q, which is not in knownSubcommands %v",
				c, c.Head(), knownSubcommands)
		}
	}
}

// TestClihintGraphLeavesAreRealSubcommands ties the graph-family hints to
// graphCmd's own accept-list. `graph` routes its second token positionally, so a
// hint like `magus graph export` is only valid while graphSubs still lists
// "export". This is the strongest guard the hand-rolled dispatch allows: graphSubs
// is the introspectable accept-list, not the switch itself, so it only catches
// drift if graphSubs stays in sync with graphCmd's switch (which SuggestNearest
// already depends on).
func TestClihintGraphLeavesAreRealSubcommands(t *testing.T) {
	for _, c := range []clihint.Command{clihint.GraphOpen, clihint.GraphExport, clihint.GraphStats} {
		if c.Head() != "graph" {
			t.Fatalf("expected a graph-family command, got %q", c)
		}
		if !slices.Contains(graphSubs, c.Leaf()) {
			t.Errorf("clihint %q references graph subcommand %q, which is not in graphSubs %v",
				c, c.Leaf(), graphSubs)
		}
	}
}

// TestClihintServerLeavesAreRealSubcommands ties the server-family hints to the
// tokens serverCmd routes on. serverCmd switches directly on clihint.*.Leaf(), so
// the accepted form and the hint already share one source of truth; this asserts
// they remain the exact pair start/stop, catching a stray edit that renames one
// side only.
func TestClihintServerLeavesAreRealSubcommands(t *testing.T) {
	got := []string{clihint.ServerStart.Leaf(), clihint.ServerStop.Leaf()}
	want := []string{"start", "stop"}
	if !slices.Equal(got, want) {
		t.Errorf("server leaves = %v, want %v", got, want)
	}
	for _, c := range []clihint.Command{clihint.ServerStart, clihint.ServerStop} {
		if c.Head() != "server" {
			t.Errorf("clihint %q is not a server-family command", c)
		}
	}
}

// TestClihintQueryOutputForm locks the query-output hint to the form queryCmd
// accepts. queryCmd matches its output positional against clihint.QueryOutput.Leaf(),
// so the hint (`magus query output <ref>`) and the accepted form cannot disagree -
// the exact bug that shipped `magus query <ref>`. This asserts the shape stays
// two-token (a bare `magus query` would reopen that gap) and renders as expected.
//
// Limit: dispatch is hand-rolled positional matching, not an introspectable
// command tree, so this cannot execute the router without a workspace. It guards
// the constant's shape and its rendering; the tie to acceptance is structural
// (queryCmd reads the same Leaf()), enforced at compile/review time rather than here.
func TestClihintQueryOutputForm(t *testing.T) {
	if clihint.QueryOutput.Head() != "query" {
		t.Errorf("QueryOutput head = %q, want %q", clihint.QueryOutput.Head(), "query")
	}
	if clihint.QueryOutput.Leaf() != "output" {
		t.Errorf("QueryOutput leaf = %q, want %q", clihint.QueryOutput.Leaf(), "output")
	}
	if got, want := clihint.QueryOutput.With("out1a2b3c"), "magus query output out1a2b3c"; got != want {
		t.Errorf("QueryOutput.With(ref) = %q, want %q", got, want)
	}
	if got, want := clihint.QueryOutput.With("out1a2b3c", "--open"), "magus query output out1a2b3c --open"; got != want {
		t.Errorf("QueryOutput.With(ref, --open) = %q, want %q", got, want)
	}
}
