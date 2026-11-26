package evidence

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"context"
	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	terms "github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
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
	m, err := magus.Open(t.Context(), w.Root(), magus.WithSpawnRule(tier.Ceiling))
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
func (s *LeasedBoundarySuite) owners(row job.Job, rows []job.Job) []string {
	projects := func() ([]*types.Project, string) {
		return []*types.Project{{Path: ".", TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: "gen/**"}}}}}, ""
	}
	var out []string
	for _, b := range leasedBoundary(row, rows, "/repos/landing", projects, quietWindow{}) {
		out = append(out, b.Path)
	}
	return out
}

// A holder quiet past jobs.stale_after keeps its path, and its reason names the prune line.
func (s *LeasedBoundarySuite) TestNamesAQuietHolder() {
	const now = int64(100_000)
	window := time.Hour
	me := job.Job{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/hint"}}}
	busy := job.Job{ID: "busy", State: job.StateRunning, Updated: now, Paths: job.Paths{Write: []string{"run.go"}}}
	quiet := job.Job{ID: "quiet", State: job.StateRunning, Updated: now - int64(2*window/time.Second) - 720,
		Paths: job.Paths{Write: []string{"run.go"}}}
	projects := func() ([]*types.Project, string) { return nil, "" }

	got := leasedBoundary(me, []job.Job{me, busy, quiet}, "/repos/landing", projects, quietWindow{now: now, after: window})

	want := []terms.TermsBoundary{
		{Path: "run.go", Reason: "owned by live lease busy: coordinate, never work around"},
		{Path: "run.go", Reason: types.FormatDiagnostic(types.LeaseHolderQuiet, fmt.Sprintf(
			"run.go is held by job quiet, untouched for 2h12m (jobs.stale_after is 1h); "+
				"the root or a person checks it: `%s job prune --dry-run --all quiet`", hint.BinaryName()))},
	}
	s.Equal(want, got, "a quiet holder still holds its path")
}

func (s *LeasedBoundarySuite) TestSkipsTheRowsAncestors() {
	rows := []job.Job{
		{ID: "root", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal"}}},
		{ID: "root/a", Parent: "root", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job"}}},
		{ID: "root/a/leaf", Parent: "root/a", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job/plan.go"}}},
		{ID: "root/b", Parent: "root", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/guard"}}},
	}
	s.Equal([]string{"internal/guard"}, s.owners(rows[2], rows), "a sibling's boundary is off limits, an ancestor's is where the leaf was forked")
}

// An exited holder returned, so a sibling's terms no longer put its paths out of reach.
func (s *LeasedBoundarySuite) TestSkipsAnExitedRow() {
	rows := []job.Job{
		{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job"}}},
		{ID: "b", State: job.StateExited, Paths: job.Paths{Write: []string{"internal/guard"}}},
		{ID: "c", State: job.StateDeclared, Paths: job.Paths{Write: []string{"internal/hint"}}},
	}
	s.Equal([]string{"internal/hint"}, s.owners(rows[0], rows))
}

// A soft path is free until its holder writes it, so a sibling's terms leave it in reach.
func (s *LeasedBoundarySuite) TestSkipsASoftPath() {
	soft := job.Job{ID: "b", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job/plan.go", "internal/job/plan_test.go"}}}
	soft.Paths.Soft = []string{"internal/job/plan_test.go"}
	me := job.Job{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job/plan_test.go"}}}
	s.Equal([]string{"internal/job/plan.go"}, s.owners(me, []job.Job{me, soft}))
}

func (s *LeasedBoundarySuite) TestListsAHardenedPath() {
	held := job.Job{ID: "b", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job/plan.go", "internal/job/plan_test.go"}}}
	me := job.Job{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/job/plan_test.go"}}}
	s.Equal([]string{"internal/job/plan.go", "internal/job/plan_test.go"}, s.owners(me, []job.Job{me, held}))
}

func (s *LeasedBoundarySuite) TestSkipsAGeneratedOutputAnotherCheckoutHolds() {
	other := job.Job{ID: "b", State: job.StateRunning, CheckoutRoot: "/repos/mandate", Paths: job.Paths{Write: []string{"gen/decode.go", "run.go"}}}
	me := job.Job{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"gen/decode.go"}}}
	s.Equal([]string{"run.go"}, s.owners(me, []job.Job{me, other}), "each checkout regenerates its own gen/decode.go")
}

func (s *LeasedBoundarySuite) TestListsAGeneratedOutputHeldInTheSameCheckout() {
	other := job.Job{ID: "b", State: job.StateRunning, CheckoutRoot: "/repos/landing", Paths: job.Paths{Write: []string{"gen/decode.go"}}}
	me := job.Job{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"gen/decode.go"}}}
	s.Equal([]string{"gen/decode.go"}, s.owners(me, []job.Job{me, other}))
}

func TestDeletionCandidatesAskNothingOfARowThatWritesNothing(t *testing.T) {
	t.Parallel()

	load := func(context.Context) *knowledge.Graph {
		t.Fatal("a row that writes nothing loaded the graph")
		return nil
	}
	got, leftOut := DeletionCandidates(t.Context(), load, job.Job{ReadOnly: true, Paths: job.Paths{Write: []string{"a.go"}}})
	assert.Nil(t, got)
	assert.Zero(t, leftOut)
	got, leftOut = DeletionCandidates(t.Context(), load, job.Job{})
	assert.Nil(t, got)
	assert.Zero(t, leftOut)
}
