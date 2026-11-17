package evidence

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"context"
	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestGeneratedBoundaryNamesWhatAnOutputCarvesOut: a brief fences a lease's generated
// globs, and the hand files an exclusion carves out of one must read as the exception
// rather than fenced with the rest.
func TestGeneratedBoundaryNamesWhatAnOutputCarvesOut(t *testing.T) {
	t.Parallel()
	w := testkit.NewWorkspace(t)
	w.Magusfile(`import "magus";
export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles(["gen/*.go", "!gen/runtime.go"]);
}
`)
	m, err := magus.Open(t.Context(), w.Root(), magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	got := generatedBoundary(m, []string{"."}, []string{"gen"})

	require.Len(t, got, 1)
	assert.Equal(t, "gen/*.go", got[0].Path)
	assert.Contains(t, got[0].Reason, "except gen/runtime.go")
}

// LeasedBoundarySuite pins which of a sibling lease's paths a job's terms put out of reach.
type LeasedBoundarySuite struct {
	suite.Suite
}

func TestLeasedBoundarySuite(t *testing.T) { suite.Run(t, new(LeasedBoundarySuite)) }

// owners is the paths leasedBoundary lists for row, from a checkout at /repos/landing in a
// workspace that declares gen/** a generated output.
func (s *LeasedBoundarySuite) owners(row types.Job, rows []types.Job) []string {
	projects := func() ([]*types.Project, string) {
		return []*types.Project{{Path: ".", TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: "gen/**"}}}}}, ""
	}
	var out []string
	for _, b := range leasedBoundary(row, rows, "/repos/landing", projects) {
		out = append(out, b.Path)
	}
	return out
}

func (s *LeasedBoundarySuite) TestSkipsTheRowsAncestors() {
	rows := []types.Job{
		{ID: "root", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal"}}},
		{ID: "root/a", Parent: "root", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job"}}},
		{ID: "root/a/leaf", Parent: "root/a", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job/plan.go"}}},
		{ID: "root/b", Parent: "root", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/guard"}}},
	}
	s.Equal([]string{"internal/guard"}, s.owners(rows[2], rows), "a sibling's boundary is off limits, an ancestor's is where the leaf was forked")
}

// An exited holder returned, so a sibling's terms no longer put its paths out of reach.
func (s *LeasedBoundarySuite) TestSkipsAnExitedRow() {
	rows := []types.Job{
		{ID: "a", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job"}}},
		{ID: "b", State: types.StateExited, Paths: types.JobPaths{Write: []string{"internal/guard"}}},
		{ID: "c", State: types.StateDeclared, Paths: types.JobPaths{Write: []string{"internal/hint"}}},
	}
	s.Equal([]string{"internal/hint"}, s.owners(rows[0], rows))
}

// A soft path is free until its holder writes it, so a sibling's terms leave it in reach.
func (s *LeasedBoundarySuite) TestSkipsASoftPath() {
	soft := types.Job{ID: "b", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job/plan.go", "internal/job/plan_test.go"}}}
	soft.Paths.Soft = []string{"internal/job/plan_test.go"}
	me := types.Job{ID: "a", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job/plan_test.go"}}}
	s.Equal([]string{"internal/job/plan.go"}, s.owners(me, []types.Job{me, soft}))
}

func (s *LeasedBoundarySuite) TestListsAHardenedPath() {
	held := types.Job{ID: "b", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job/plan.go", "internal/job/plan_test.go"}}}
	me := types.Job{ID: "a", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"internal/job/plan_test.go"}}}
	s.Equal([]string{"internal/job/plan.go", "internal/job/plan_test.go"}, s.owners(me, []types.Job{me, held}))
}

func (s *LeasedBoundarySuite) TestSkipsAGeneratedOutputAnotherCheckoutHolds() {
	other := types.Job{ID: "b", State: types.StateRunning, CheckoutRoot: "/repos/mandate", Paths: types.JobPaths{Write: []string{"gen/decode.go", "run.go"}}}
	me := types.Job{ID: "a", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"gen/decode.go"}}}
	s.Equal([]string{"run.go"}, s.owners(me, []types.Job{me, other}), "each checkout regenerates its own gen/decode.go")
}

func (s *LeasedBoundarySuite) TestListsAGeneratedOutputHeldInTheSameCheckout() {
	other := types.Job{ID: "b", State: types.StateRunning, CheckoutRoot: "/repos/landing", Paths: types.JobPaths{Write: []string{"gen/decode.go"}}}
	me := types.Job{ID: "a", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"gen/decode.go"}}}
	s.Equal([]string{"gen/decode.go"}, s.owners(me, []types.Job{me, other}))
}

func TestDeletionCandidatesAskNothingOfARowThatWritesNothing(t *testing.T) {
	t.Parallel()

	load := func(context.Context) *knowledge.Graph {
		t.Fatal("a row that writes nothing loaded the graph")
		return nil
	}
	assert.Nil(t, DeletionCandidates(t.Context(), load, types.Job{ReadOnly: true, Paths: types.JobPaths{Write: []string{"a.go"}}}))
	assert.Nil(t, DeletionCandidates(t.Context(), load, types.Job{}))
}
