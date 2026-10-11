package job

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatesWorkspace is a ci chain of a linter, a test, a build that composes a compile step,
// a generator and a linter in another project, the shape a go-test check never finishes.
func gatesWorkspace() []*types.Project {
	return []*types.Project{
		{
			Path:    ".",
			Sources: types.MustParseGlobs("**/*.go"),
			TargetChains: map[string][]types.ChainStep{
				"ci": {
					{Target: "lint"}, {Target: "go-test"}, {Target: "build"}, {Target: "gen"},
					{Project: "docs", Target: "lint"},
				},
				"build": {{Target: "compile"}},
			},
			TargetOutputs: map[string][]types.OutputRef{"gen": {{Glob: "out/gen.go"}}},
		},
		{
			Path:    "docs",
			Sources: types.MustParseGlobs("**/*.md"),
		},
	}
}

func gateTargets(gates []UnrunGate) []string {
	var out []string
	for _, g := range gates {
		out = append(out, g.Project+":"+g.Target)
	}
	return out
}

func TestUnrunGatesListsCiTargetsNothingTheJobRuns(t *testing.T) {
	t.Parallel()

	check := func(target string) *types.LeaseCheck { return &types.LeaseCheck{Target: target, Project: "."} }
	goal := func(target string) types.Goal {
		return types.Goal{ID: "g-" + target, Check: types.LeaseCheck{Target: target, Project: "."}}.Resolve()
	}
	row := func(mutate func(*types.Job)) types.Job {
		r := types.Job{ID: "j", WritePaths: []string{"internal/job/gates.go"}, Check: check("go-test")}
		mutate(&r)
		return r
	}

	for _, tc := range []struct {
		name        string
		row         types.Job
		regenerated []RegeneratedOutput
		want        []string
	}{
		{
			name: "a go-test check leaves the linter and the build; compile rides with the build",
			row:  row(func(*types.Job) {}),
			want: []string{".:lint", ".:build", ".:gen"},
		},
		{
			name: "a goal that runs a target covers it",
			row:  row(func(r *types.Job) { r.Goals = []types.Goal{goal("lint")} }),
			want: []string{".:build", ".:gen"},
		},
		{
			name: "a check's chain members count as run",
			row:  row(func(r *types.Job) { r.Check = check("build") }),
			want: []string{".:lint", ".:go-test", ".:gen"},
		},
		{
			name: "a script check covers nothing",
			row:  row(func(r *types.Job) { r.Check = &types.LeaseCheck{Script: "hack/check.buzz"} }),
			want: []string{".:lint", ".:go-test", ".:build", ".:gen"},
		},
		{
			name:        "what the root regenerates is left to that list",
			row:         row(func(*types.Job) {}),
			regenerated: []RegeneratedOutput{{Output: "out/gen.go", Target: ".:gen", Source: "**/*.go"}},
			want:        []string{".:lint", ".:build"},
		},
		{
			name: "a project whose sources the write paths miss is not listed",
			row:  row(func(r *types.Job) { r.WritePaths = []string{"docs/page.md"} }),
			want: []string{"docs:lint"},
		},
		{
			name: "a read-only job writes nothing",
			row:  row(func(r *types.Job) { r.ReadOnly = true }),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := UnrunGates(t.TempDir(), tc.row, gatesWorkspace(), tc.regenerated)
			assert.Equal(t, tc.want, gateTargets(got))
			for _, g := range got {
				require.NoError(t, g.Goal.Validate(), "the goal to add must be declarable as printed")
			}
		})
	}
}

func TestUnrunGatesNeedsACiChain(t *testing.T) {
	t.Parallel()

	r := types.Job{ID: "j", WritePaths: []string{"a.go"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	assert.Empty(t, UnrunGates(t.TempDir(), r, []*types.Project{{Path: ".", Sources: types.MustParseGlobs("**/*.go")}}, nil))
	assert.Empty(t, UnrunGates(t.TempDir(), r, nil, nil), "no root project")
}

func TestUnrunGoalNamesTheProject(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "lint", unrunGoal(".", "lint", map[string]bool{}).ID)
	assert.Equal(t, "lint-libs-conventions", unrunGoal("libs/conventions", "lint", map[string]bool{}).ID)
	assert.Equal(t, types.GoalKindCheck, unrunGoal(".", "lint", map[string]bool{}).Kind)
}

// Two projects can fold to one id, and a ci target can share its name with a goal the row
// already holds: each goal to add gets an id of its own.
func TestUnrunGatesGiveEachGoalItsOwnID(t *testing.T) {
	t.Parallel()

	projects := []*types.Project{
		{
			Path:    ".",
			Sources: types.MustParseGlobs("**/*.go"),
			TargetChains: map[string][]types.ChainStep{
				"ci": {{Target: "check"}, {Project: "a/b", Target: "lint"}, {Project: "a-b", Target: "lint"}},
			},
		},
		{Path: "a/b", Sources: types.MustParseGlobs("**/*.go")},
		{Path: "a-b", Sources: types.MustParseGlobs("**/*.go")},
	}
	row := types.Job{ID: "j", WritePaths: []string{"a/b/x.go", "a-b/y.go", "z.go"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}

	got := UnrunGates(t.TempDir(), row, projects, nil)

	var ids []string
	for _, g := range got {
		ids = append(ids, g.Goal.ID)
	}
	assert.Equal(t, []string{"check-2", "lint-a-b", "lint-a-b-2"}, ids)
}

func TestRenderUnrunGatesCapsTheListAndPrintsTheGoals(t *testing.T) {
	t.Parallel()

	var gates []UnrunGate
	taken := map[string]bool{}
	for i := range unrunGatesLimit + 2 {
		gates = append(gates, UnrunGate{Target: fmt.Sprintf("t%d", i), Project: ".", Source: "**/*.go", Goal: unrunGoal(".", fmt.Sprintf("t%d", i), taken)})
	}
	var buf bytes.Buffer
	RenderUnrunGates(&buf, gates)
	out := buf.String()
	assert.Contains(t, out, unrunHeading)
	assert.Contains(t, out, "t0 . (reads **/*.go)")
	assert.NotContains(t, out, fmt.Sprintf("t%d . (reads", unrunGatesLimit))
	assert.Contains(t, out, "and 2 more; -o json lists every one")
	assert.Contains(t, out, `goals to add: [{"id":"t0","kind":"check"`)

	buf.Reset()
	RenderUnrunGates(&buf, nil)
	assert.Empty(t, buf.String(), "nothing to say prints no heading")
}
