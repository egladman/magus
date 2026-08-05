package clihint

import (
	"testing"

	"github.com/egladman/magus/types"
)

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
		DescribeTargets, DescribeProject, Ls, LsTargets, Where, MCPTokenGenerate,
	}
	if len(All) != len(declared) {
		t.Fatalf("All has %d commands, declared list has %d; keep them in sync", len(All), len(declared))
	}
}

// TestRefMatchCommand covers the three renderings query.go and magus_output both
// rely on: root-project omission, a charm suffix on a match that required explicit
// charms, and --no-default-charms on a bare match in a workspace with configured
// defaults.
func TestRefMatchCommand(t *testing.T) {
	cases := []struct {
		name          string
		mt            types.RefMatch
		defaultCharms []string
		want          string
	}{
		{
			name: "root project omitted",
			mt:   types.RefMatch{Project: ".", Target: "build"},
			want: "magus run build",
		},
		{
			name: "non-root project named",
			mt:   types.RefMatch{Project: "pkg/a", Target: "build"},
			want: "magus run build pkg/a",
		},
		{
			name: "explicit charms suffix, no --no-default-charms",
			mt:   types.RefMatch{Project: ".", Target: "build", Charms: []string{"rw"}},
			want: "magus run build:rw",
		},
		{
			name:          "bare match with configured defaults gets --no-default-charms",
			mt:            types.RefMatch{Project: ".", Target: "build"},
			defaultCharms: []string{"rw"},
			want:          "magus run build --no-default-charms",
		},
		{
			name: "bare match with no configured defaults",
			mt:   types.RefMatch{Project: ".", Target: "build"},
			want: "magus run build",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RefMatchCommand(c.mt, c.defaultCharms); got != c.want {
				t.Errorf("RefMatchCommand(%+v, %v) = %q, want %q", c.mt, c.defaultCharms, got, c.want)
			}
		})
	}
}
