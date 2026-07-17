package clihint

import "testing"

func TestCommandRender(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"string", QueryOutput.String(), "magus query output"},
		{"string via fmt", QueryOutput.String(), QueryOutput.String()},
		{"with no args", Run.With(), "magus run"},
		{"with one arg", QueryOutput.With("out1a2b3c"), "magus query output out1a2b3c"},
		{"with two args", QueryOutput.With("out1a2b3c", "--open"), "magus query output out1a2b3c --open"},
		{"single-token head", Status.Head(), "status"},
		{"multi-token head", GraphOpen.Head(), "graph"},
		{"single-token leaf", Status.Leaf(), "status"},
		{"multi-token leaf", GraphOpen.Leaf(), "open"},
		{"deep leaf", MCPTokenGenerate.Leaf(), "generate"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestAllRegistered fails if a Command value is declared but left out of All, so
// the drift test in cmd/magus keeps walking the full set.
func TestAllRegistered(t *testing.T) {
	declared := []Command{
		Run, QueryOutput, GraphOpen, GraphExport, GraphStats, GraphBuild,
		ServerStart, ServerStop, ServerJob, Status, Watch, Affected,
		DescribeTargets, MCPTokenGenerate,
	}
	if len(All) != len(declared) {
		t.Fatalf("All has %d commands, declared list has %d; keep them in sync", len(All), len(declared))
	}
}
