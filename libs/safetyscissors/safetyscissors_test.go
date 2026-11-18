package safetyscissors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

type NewSuite struct {
	suite.Suite
	dir string
}

func TestNew(t *testing.T) { suite.Run(t, new(NewSuite)) }

func (s *NewSuite) SetupTest() { s.dir = s.T().TempDir() }

func required() []Option {
	return []Option{
		WithInvocation("inv1", session.InvocationStart{Command: "test"}),
		WithActor(func() job.Actor { return job.Actor{} }),
		WithNotices(func(job.Notice) {}),
		WithCheckSpec(keptChecks{}),
	}
}

// keptChecks stands in for the caller's check spec: a check is whatever JSON it is,
// compared as written.
type keptChecks struct{}

func (keptChecks) Validate(c job.Check) (job.Check, error) { return c, nil }

func (keptChecks) Equal(a, b job.Check) (bool, error) { return a.String() == b.String(), nil }

func (keptChecks) Render(c job.Check) (string, error) { return c.String(), nil }

func (s *NewSuite) open(opts ...Option) *SafetyScissors {
	ss, err := New(s.T().Context(), s.dir, append(required(), opts...)...)
	s.Require().NoError(err)
	return ss
}

// assigning stands in for the caller's tier assignment, which the store leaves to a
// write hook: it records assigned on the row it writes.
func assigning(assigned tier.Assignment) Option {
	return WithJobOptions(job.WithWriteHook(func(_ context.Context, e *job.WriteEvent) error {
		if e.Stage == job.StageTier {
			e.Row.ModelTier = &assigned
		}
		return nil
	}))
}

func (s *NewSuite) put(ss *SafetyScissors, row job.Job) {
	_, err := ss.Jobs().Update(s.T().Context(), row.ID, func(j *job.Job) { *j = row })
	s.Require().NoError(err)
}

// declaration is two tiers, routine below design.
func declaration() tier.Declaration {
	return tier.Declaration{Enum: "Tier", Cases: []string{"routine", "design"}, Bounds: make([]tier.Bounds, 2),
		Skills: []tier.SkillVariant{tier.SkillVariantShort, tier.SkillVariantShort}}
}

func confines() (int, error) { return 100, nil }

func (s *NewSuite) optionProblems(err error) []string {
	var named []string
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		if opt, ok := errors.AsType[*OptionError](e); ok {
			named = append(named, opt.Option)
		}
	}
	return named
}

func (s *NewSuite) TestNewNamesEveryMissingRequiredOptionAtOnce() {
	_, err := New(s.T().Context(), s.dir)
	s.Require().Error(err)
	s.Equal([]string{"WithInvocation", "WithActor", "WithNotices", "WithCheckSpec"}, s.optionProblems(err))
}

func (s *NewSuite) TestNewRefusesACheckSpecWithANilFunction() {
	_, err := New(s.T().Context(), s.dir, append(required(), WithCheckSpec(nil))...)
	s.Require().Error(err)
	s.Equal([]string{"WithCheckSpec"}, s.optionProblems(err))
}

func (s *NewSuite) TestNewRefusesAnEmptyDir() {
	_, err := New(s.T().Context(), " ", required()...)
	s.ErrorContains(err, "needs the directory the stores live in")
}

func (s *NewSuite) TestNewRefusesAnInvalidInvocationID() {
	_, err := New(s.T().Context(), s.dir, append(required(), WithInvocation("../x", session.InvocationStart{}))...)
	_, ok := errors.AsType[*session.InvalidIDError](err)
	s.True(ok, "got %v", err)
}

func (s *NewSuite) TestNewLaysTheStoresOutUnderDir() {
	ss := s.open()
	s.Equal(Dirs{Jobs: filepath.Join(s.dir, "jobs"), Sessions: filepath.Join(s.dir, "sessions"), Activity: filepath.Join(s.dir, "activity")}, ss.Dirs())
	path, err := ss.Jobs().Path()
	s.Require().NoError(err)
	s.Equal(filepath.Join(s.dir, "jobs", "jobs.jsonl"), path)
}

func (s *NewSuite) TestWithDirsMovesOnlyTheFieldsItSets() {
	elsewhere := s.T().TempDir()
	ss := s.open(WithDirs(Dirs{Jobs: elsewhere}))
	s.Equal(Dirs{Jobs: elsewhere, Sessions: filepath.Join(s.dir, "sessions"), Activity: filepath.Join(s.dir, "activity")}, ss.Dirs())
}

func (s *NewSuite) TestWithDirsRefusesJobsAndSessionsInOneDirectory() {
	shared := s.T().TempDir()
	_, err := New(s.T().Context(), s.dir, append(required(), WithDirs(Dirs{Jobs: shared, Sessions: shared}))...)
	s.Equal([]string{"WithDirs"}, s.optionProblems(err))
}

func (s *NewSuite) unconfigured(err error, option string) {
	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, "got %v", err)
	s.Equal(option, refused.Option)
}

func (s *NewSuite) TestAnUnconfiguredPartNamesItsOption() {
	ss := s.open()
	_, err := ss.Slots()
	s.unconfigured(err, "WithSlots")
	_, err = ss.Machine()
	s.unconfigured(err, "WithMachine")
	_, err = ss.Tiers()
	s.unconfigured(err, "WithTiers")
	_, err = ss.Sandbox()
	s.unconfigured(err, "WithSandbox")
}

func (s *NewSuite) TestACommandWithNoSandboxIsRefused() {
	_, err := s.open().Command(s.T().Context(), "", "true")
	s.unconfigured(err, "WithSandbox")
}

func (s *NewSuite) TestASpawnWithNoTiersIsRefused() {
	s.unconfigured(s.open().SpawnCeiling(s.T().Context(), job.Job{ID: "c", Parent: "p"}), "WithTiers")
}

func (s *NewSuite) TestABudgetBelowOneIsRefused() {
	_, err := New(s.T().Context(), s.dir, append(required(), WithSlots(0), WithMachine(0, 4))...)
	s.Equal([]string{"WithSlots", "WithMachine"}, s.optionProblems(err))
}

func (s *NewSuite) TestConfiguredBudgetsAreReturned() {
	ss := s.open(WithSlots(3), WithMachine(1024, 2))
	slots, err := ss.Slots()
	s.Require().NoError(err)
	s.Equal(3, slots.Capacity())
	_, err = ss.Machine()
	s.NoError(err)
}

func (s *NewSuite) TestARefusedTierDeclarationIsNewsError() {
	_, err := New(s.T().Context(), s.dir, append(required(), WithTiers(tier.Declaration{Enum: "Tier"}))...)
	_, ok := errors.AsType[*tier.DeclarationError](err)
	s.True(ok, "got %v", err)
}

func (s *NewSuite) TestSessionsFoldWhatTheWriterAppended() {
	at := time.UnixMilli(1_700_000_000_000)
	ss := s.open(WithClock(func() time.Time { return at }))
	s.Require().NoError(ss.Session().Append(s.T().Context(), session.KindTargetResult, nil))
	fold, err := ss.Sessions(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 2)
	s.Equal(session.KindInvocationStart, fold.Records[0].Kind)
	s.Equal(at.UnixMilli(), fold.Records[1].Ts)
}

func (s *NewSuite) TestAnEmptyActivityLogHasNoCalls() {
	var calls []journal.Call
	for c, err := range s.open().Calls(s.T().Context(), 10) {
		s.Require().NoError(err)
		calls = append(calls, c)
	}
	s.Empty(calls)
}

func (s *NewSuite) TestAChildRankedAboveItsParentIsRefused() {
	ss := s.open(WithTiers(declaration()), assigning(tier.Assignment{Name: "routine", Rank: 0}))
	s.put(ss, job.Job{ID: "p", State: job.StateRunning})

	err := ss.SpawnCeiling(s.T().Context(), job.Job{ID: "c", Parent: "p", ModelTier: &tier.Assignment{Name: "design", Rank: 1}})
	_, ok := errors.AsType[*tier.AboveParentError](err)
	s.True(ok, "got %v", err)
	s.NoError(ss.SpawnCeiling(s.T().Context(), job.Job{ID: "c", Parent: "p", ModelTier: &tier.Assignment{Name: "routine", Rank: 0}}))
}

func (s *NewSuite) TestAChildWithNoParentIsBoundedByNothing() {
	ss := s.open(WithTiers(declaration()))
	s.NoError(ss.SpawnCeiling(s.T().Context(), job.Job{ID: "c", ModelTier: &tier.Assignment{Name: "design", Rank: 1}}))
}

func (s *NewSuite) TestASpawnUnderAnUnknownParentIsRefused() {
	ss := s.open(WithTiers(declaration()))
	s.ErrorIs(ss.SpawnCeiling(s.T().Context(), job.Job{ID: "c", Parent: "gone"}), job.ErrUnknownJob)
}

func (s *NewSuite) TestATierRecordedAtAnotherRankIsRefused() {
	ss := s.open(WithTiers(declaration()), assigning(tier.Assignment{Name: "routine", Rank: 1}))
	s.put(ss, job.Job{ID: "p", State: job.StateRunning})
	err := ss.SpawnCeiling(s.T().Context(), job.Job{ID: "c", Parent: "p"})
	refused, ok := errors.AsType[*AssignmentError](err)
	s.Require().True(ok, "got %v", err)
	s.Equal("p", refused.Job)
}

func (s *NewSuite) TestARequiredSandboxOnAHostThatCannotConfineFailsNew() {
	cannot := func() (int, error) { return 0, &sandbox.UnsupportedError{} }
	_, err := New(s.T().Context(), s.dir, append(required(), WithSandbox(s.dir, nil, sandbox.WithKernelProbe(cannot)))...)
	_, ok := errors.AsType[*sandbox.UnconfinedError](err)
	s.True(ok, "got %v", err)
}

func (s *NewSuite) TestABestEffortSandboxOnAHostThatCannotConfineOpens() {
	cannot := func() (int, error) { return 0, &sandbox.UnsupportedError{} }
	ss := s.open(WithSandbox(s.dir, nil, sandbox.WithKernelProbe(cannot),
		sandbox.WithMode(sandbox.BestEffort), sandbox.WithUnconfined(func(_ context.Context, _ error) {})))
	_, err := ss.Sandbox()
	s.NoError(err)
}

func (s *NewSuite) TestACommandUnderAnUnknownLeaseIsRefused() {
	ss := s.open(WithSandbox(s.dir, nil, sandbox.WithKernelProbe(confines)))
	_, err := ss.Command(s.T().Context(), "gone", "true")
	s.ErrorIs(err, job.ErrUnknownJob)
}

func (s *NewSuite) TestACommandUnderAnEndedLeaseIsRefused() {
	ss := s.open(WithSandbox(s.dir, nil, sandbox.WithKernelProbe(confines)))
	s.put(ss, job.Job{ID: "w", Parent: "root", State: job.StatePass})
	_, err := ss.Command(s.T().Context(), "w", "true")
	refused, ok := errors.AsType[*LeaseNotLiveError](err)
	s.Require().True(ok, "got %v", err)
	s.Equal(job.StatePass, refused.State)
}

func (s *NewSuite) TestACommandUnderALiveLeaseIsUnstarted() {
	ss := s.open(WithSandbox(s.dir, []sandbox.Bind{{Path: s.dir, Mode: "rw"}}, sandbox.WithKernelProbe(confines)))
	s.put(ss, job.Job{ID: "w", Parent: "root", State: job.StateRunning, Paths: job.Paths{Write: []string{"out/**"}}})
	cmd, err := ss.Command(s.T().Context(), "w", "true")
	s.Require().NoError(err)
	s.Nil(cmd.Process)
}

func ExampleNew() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "safetyscissors-example")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ss, err := New(ctx, dir,
		WithInvocation("inv1", session.InvocationStart{Command: "example"}),
		WithActor(func() job.Actor { return job.Actor{} }),
		WithNotices(func(job.Notice) {}),
		WithCheckSpec(keptChecks{}),
		WithSlots(2),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	slots, err := ss.Slots()
	fmt.Println(slots.Capacity(), err)

	_, err = ss.Command(ctx, "", "go", "version")
	var unconfigured *UnconfiguredError
	fmt.Println(errors.As(err, &unconfigured), unconfigured.Option)
	// Output:
	// 2 <nil>
	// true WithSandbox
}
