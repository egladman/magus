package job

import (
	"flag"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The person's two spellings are one declaration: a row typed as flags and the same row
// piped in as a record reach the store identically, or the CLI has quietly grown a
// second vocabulary.
func TestRegisterFromFlagsAndFromStdinAgree(t *testing.T) {
	t.Parallel()

	flags := flag.NewFlagSet("job fork", flag.ContinueOnError)
	spec := flaggen.BindJobFork(flags)
	require.NoError(t, flags.Parse([]string{
		"--instructions", "the store is the enforcement point",
		"--parent", "adjacency",
		"--checkpoint", "cf5509d09",
		"--write-paths", "internal/ledger,types/lease.go",
		"--deny-paths", "MAGUS.md",
		"--read-paths", "internal/trail",
		"--depends-on", "adj/guard",
		"--check", "test internal/ledger",
		"--model", "design",
		"--brief-format", "buzz",
	}))
	// The version is READ from the constant, not typed as a digit: this case is about the
	// two doors agreeing on the FIELDS, and a fixture whose version fell behind would fail
	// it at the version check instead, naming nothing it came here to compare.
	piped, err := DecodeStdinDeclaration(strings.NewReader(fmt.Sprintf(`{
	  "api_version": %q,
	  "id": "adj/store",`, job.APIVersion) + `
	  "parent": "adjacency",
	  "agent": {"model": {"tier": "design"}, "instructions": "the store is the enforcement point", "worker": {"type": "resumed"}},
	  "checkpoint": "cf5509d09",
	  "paths": {"write": ["internal/ledger", "types/lease.go"], "read": ["internal/trail"], "deny": ["MAGUS.md"]},
	  "depends_on": ["adj/guard"],
	  "validation": "magus run test internal/ledger",
	  "brief_format": "buzz"
	}`))
	require.NoError(t, err)

	fromFlags, err := FlagDeclaration("adj/store", *spec, tierWords("design"))
	require.NoError(t, err)
	require.NoError(t, types.ValidateDeclaration(fromFlags))

	var a, b job.Job
	fromFlags.Apply(&a)
	piped.Apply(&b)
	assert.Equal(t, a, b)
	assert.Equal(t, job.BriefFormatBuzz, a.BriefFormat)
}

// A brief format is one of the two, refused at fork rather than read as markdown later.
func TestForkRefusesAnUnknownBriefFormat(t *testing.T) {
	t.Parallel()
	d, err := FlagDeclaration("adj/store", flaggen.JobForkFlags{Instructions: "c", BriefFormat: "json"}, tierWords())
	require.NoError(t, err)
	err = types.ValidateDeclaration(d)
	require.Error(t, err)
	assert.ErrorContains(t, err, `brief_format "json"`)
}

// tierWords reads a --model word as a store declaring tiers would.
func tierWords(tiers ...string) func(string) job.Model {
	return func(word string) job.Model {
		if slices.Contains(tiers, word) {
			return job.Model{Tier: word}
		}
		return job.Model{ID: word}
	}
}

// forkAgentFlagsSuite pins how the agent flags of `job fork` write the
// agent section: each flag alone, and none at all.
type forkAgentFlagsSuite struct {
	suite.Suite
}

func TestForkAgentFlags(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(forkAgentFlagsSuite))
}

func (s *forkAgentFlagsSuite) fork(args ...string) job.Declaration {
	flags := flag.NewFlagSet("job fork", flag.ContinueOnError)
	spec := flaggen.BindJobFork(flags)
	s.Require().NoError(flags.Parse(args))
	d, err := FlagDeclaration("adj/store", *spec, tierWords("design"))
	s.Require().NoError(err)
	s.Require().NoError(types.ValidateDeclaration(d))
	return d
}

func (s *forkAgentFlagsSuite) TestNoAgentFlagWritesNoSection() {
	s.Nil(s.fork("--read-only").Agent)
}

func (s *forkAgentFlagsSuite) TestInstructionsResumeTheWarmWorker() {
	s.Equal(&job.Agent{
		Instructions: "ship it",
		Worker:       job.Worker{Type: job.WorkerTypeResumed},
	}, s.fork("--read-only", "--instructions", "ship it").Agent)
}

func (s *forkAgentFlagsSuite) TestModelNamingATierRequestsTheTier() {
	s.Equal(&job.Agent{
		Model:  job.Model{Tier: "design"},
		Worker: job.Worker{Type: job.WorkerTypeResumed},
	}, s.fork("--read-only", "--model", "design").Agent)
}

func (s *forkAgentFlagsSuite) TestModelNamingNoTierRequestsTheID() {
	s.Equal(job.Model{ID: "opus"}, s.fork("--read-only", "--model", "opus").Agent.Model)
}

func (s *forkAgentFlagsSuite) TestNewWorkerStartsANewWorkerForItsReason() {
	s.Equal(&job.Agent{
		Worker: job.Worker{Type: job.WorkerTypeNew, Reason: "the warm one read the old limiter"},
	}, s.fork("--read-only", "--new-worker", "the warm one read the old limiter").Agent)
}

// forkListFlagsSuite pins how the list flags of `job fork` reach the record.
type forkListFlagsSuite struct {
	suite.Suite
}

func TestForkListFlags(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(forkListFlagsSuite))
}

func (s *forkListFlagsSuite) declare(args ...string) (job.Declaration, error) {
	flags := flag.NewFlagSet("job fork", flag.ContinueOnError)
	spec := flaggen.BindJobFork(flags)
	s.Require().NoError(flags.Parse(args))
	return FlagDeclaration("adj/store", *spec, tierWords())
}

func (s *forkListFlagsSuite) TestRepeatsAndCommasReachThePaths() {
	d, err := s.declare("--write-paths", "a.go,b.go", "--write-paths", "c.go", "--depends-on", "adj/guard")
	s.Require().NoError(err)
	s.Equal([]string{"a.go", "b.go", "c.go"}, d.Paths.Write)
	s.Equal([]string{"adj/guard"}, d.DependsOn)
}

func (s *forkListFlagsSuite) TestATrailingCommaIsRefused() {
	_, err := s.declare("--deny-paths", "MAGUS.md,")
	s.ErrorContains(err, "--deny-paths")
}

// A path flag takes both spellings, and refuses the empty segment a trailing comma
// leaves: a boundary that silently shrank is the failure --skip already refuses.
func TestRegisterPathFlagsTakeRepeatsAndCommas(t *testing.T) {
	t.Parallel()

	var repeated, combined ListFlag
	require.NoError(t, repeated.Set("internal/ledger"))
	require.NoError(t, repeated.Set("types/lease.go"))
	require.NoError(t, combined.Set("internal/ledger, types/lease.go"))
	assert.Equal(t, repeated, combined)

	var refused ListFlag
	require.Error(t, refused.Set("internal/ledger,"))
	require.Error(t, refused.Set(""))
}

func TestParsePauseUntilReadsADurationOrAnInstant(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := map[string]int64{
		"":                     0,
		"2h":                   now.Add(2 * time.Hour).Unix(),
		"2027-01-15T09:00:00Z": 1_800_003_600,
	}
	for raw, want := range cases {
		got, err := job.ParsePauseUntil(raw, now)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"-2h", "tomorrow"} {
		_, err := job.ParsePauseUntil(raw, now)
		assert.Error(t, err, raw)
	}
}

type forkDeliverySuite struct {
	suite.Suite
}

func TestForkDelivery(t *testing.T) { suite.Run(t, new(forkDeliverySuite)) }

func (s *forkDeliverySuite) TestTheFlagReachesTheRecord() {
	flags := flag.NewFlagSet("job fork", flag.ContinueOnError)
	spec := flaggen.BindJobFork(flags)
	s.Require().NoError(flags.Parse([]string{"--instructions", "c", "--delivery", job.DeliveryFold}))
	row, err := FlagDeclaration("adj/store", *spec, tierWords())
	s.Require().NoError(err)
	s.Require().NoError(types.ValidateDeclaration(row))
	s.Equal(job.DeliveryFold, row.Delivery)
}

func (s *forkDeliverySuite) TestAStackedForkRecordsThisCheckoutsBranch() {
	r := testkit.NewRepository(s.T(), nil)
	r.Git("switch", "-q", "-c", "feat/parent")
	row, err := StackedHead(s.T().Context(), r.Root(), job.Declaration{ID: "child", Delivery: job.DeliveryStacked}, types.VCSOptions{})
	s.Require().NoError(err)
	s.Equal("feat/parent", row.ParentHead)
}

func (s *forkDeliverySuite) TestAStackedForkKeepsTheHeadTheRecordNames() {
	row, err := StackedHead(s.T().Context(), "", job.Declaration{ID: "child", Delivery: job.DeliveryStacked, ParentHead: "feat/named"}, types.VCSOptions{})
	s.Require().NoError(err)
	s.Equal("feat/named", row.ParentHead)
}

func (s *forkDeliverySuite) TestAStackedForkOnNoBranchIsRefused() {
	r := testkit.NewRepository(s.T(), nil)
	r.Git("switch", "-q", "--detach")
	_, err := StackedHead(s.T().Context(), r.Root(), job.Declaration{ID: "child", Delivery: job.DeliveryStacked}, types.VCSOptions{})
	s.Require().ErrorContains(err, "is on no branch")
}

func (s *forkDeliverySuite) TestAnUnstackedForkReadsNoBranch() {
	row, err := StackedHead(s.T().Context(), "", job.Declaration{ID: "child", Delivery: job.DeliverySeparate}, types.VCSOptions{})
	s.Require().NoError(err)
	s.Empty(row.ParentHead)
}

// widenSuite pins the record `job apply --add-write-path` builds from a stored row.
type widenSuite struct {
	suite.Suite
	store *Store
}

func TestWidenSuite(t *testing.T) { suite.Run(t, new(widenSuite)) }

func (s *widenSuite) SetupTest() {
	row := job.Job{ID: "w", State: job.StateRunning, Agent: &job.Agent{Model: job.Model{Tier: "bounded"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}}
	row.Paths.Write = []string{"a.go"}
	row.Check = &job.LeaseCheck{Target: "test", Project: "."}
	row.Validation = CheckCommand(*row.Check)
	s.store = tempStore(s.T(), s.T().TempDir())
	seed(s.T(), s.store, row)
}

func (s *widenSuite) apply(add ...string) []Applied {
	rec, err := WidenedDeclaration(s.store, "w", add)
	s.Require().NoError(err)
	applied, err := Apply(s.T().Context(), s.store, []job.Declaration{rec}, config.Jobs{}, nil, func() string { return "" }, false)
	s.Require().NoError(err)
	return applied
}

func (s *widenSuite) TestARunningRowWidenedStaysRunningWithTheNewPath() {
	before, err := s.store.List()
	s.Require().NoError(err)
	want := before[0]
	want.Paths.Write, want.Widenings = []string{"a.go", "b.go", "c.go"}, 1

	applied := s.apply("b.go", "c.go")

	s.Require().Len(applied, 1)
	want.Validated, want.WriteProof = applied[0].Next.Validated, applied[0].Next.WriteProof
	s.Equal(want, applied[0].Next)
	rows, err := s.store.List()
	s.Require().NoError(err)
	s.Equal(want, rows[0])
}

func (s *widenSuite) TestAPathTheRowHoldsChangesNothing() {
	applied := s.apply("a.go")

	s.Require().Len(applied, 1)
	s.Empty(applied[0].Changed)
}

func (s *widenSuite) TestAnUnknownJobIsAUsageError() {
	_, err := WidenedDeclaration(s.store, "nope", []string{"b.go"})

	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
}

// narrowSuite pins the record `job apply --release-write-path` builds from a stored row.
type narrowSuite struct {
	suite.Suite
	store *Store
}

func TestNarrowSuite(t *testing.T) { suite.Run(t, new(narrowSuite)) }

func (s *narrowSuite) SetupTest() {
	row := job.Job{ID: "w", State: job.StateRunning, Agent: &job.Agent{Model: job.Model{Tier: "bounded"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}}
	row.Paths.Write = []string{"a.go", "b.go", "c.go#Run"}
	row.Check = &job.LeaseCheck{Target: "test", Project: "."}
	row.Validation = CheckCommand(*row.Check)
	row.Goals = []job.Goal{{ID: "g", Kind: job.GoalKindPaths, Paths: []string{"a.go"}}}
	s.store = tempStore(s.T(), s.T().TempDir())
	seed(s.T(), s.store, row)
}

func (s *narrowSuite) release(paths ...string) job.Declaration {
	held, err := WidenedDeclaration(s.store, "w", nil)
	s.Require().NoError(err)
	rec, err := NarrowedDeclaration(held, "w", paths)
	s.Require().NoError(err)
	return rec
}

func (s *narrowSuite) TestAReleasedPathLeavesTheRestOfTheSpec() {
	rec := s.release("b.go")

	s.Equal([]string{"a.go", "c.go#Run"}, rec.Paths.Write)
	s.Len(rec.Goals, 1)
	s.NotNil(rec.Check)
}

func (s *narrowSuite) TestADeclarationClaimIsReleasedByItsWholeValue() {
	s.Equal([]string{"a.go", "b.go"}, s.release("c.go#Run").Paths.Write)
}

func (s *narrowSuite) TestAPathTheJobLacksIsAUsageError() {
	held, err := WidenedDeclaration(s.store, "w", nil)
	s.Require().NoError(err)

	_, err = NarrowedDeclaration(held, "w", []string{"nope.go"})

	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *narrowSuite) TestAClaimWithACommaIsOnePath() {
	var paths claimListFlag

	s.Require().NoError(paths.Set("a.go#Run,Stop"))
	s.Require().NoError(paths.Set("b.go,c.go"))

	s.Equal(claimListFlag{"a.go#Run,Stop", "b.go", "c.go"}, paths)
}

// A stacked fork whose VCS cannot be resolved keeps the cause beside the fix.
type stackedHeadSuite struct{ suite.Suite }

func TestStackedHeadSuite(t *testing.T) { suite.Run(t, new(stackedHeadSuite)) }

func (s *stackedHeadSuite) TestStackedHeadPreservesResolutionFailures() {
	repo := testkit.NewRepository(s.T(), nil)
	row := job.Declaration{ID: "child", Delivery: job.DeliveryStacked}

	got, err := StackedHead(s.T().Context(), repo.Root(), row, types.VCSOptions{Name: "not-a-vcs"})

	s.ErrorIs(err, types.ErrVCSUnknown)
	s.ErrorContains(err, "child is stacked with no pull request base")
	s.ErrorContains(err, "name parent_head in the record")
	s.Equal(row, got)
}

func (s *stackedHeadSuite) TestStackedHeadPreservesMetadataFailures() {
	row := job.Declaration{ID: "child", Delivery: job.DeliveryStacked}

	got, err := StackedHead(s.T().Context(), s.T().TempDir(), row, types.VCSOptions{Name: "git"})

	var cause *exec.ExitError
	s.ErrorAs(err, &cause)
	s.ErrorContains(err, "child is stacked with no pull request base")
	s.Equal(row, got)
}
