package job

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

type JobSuite struct{ suite.Suite }

func TestJob(t *testing.T) { suite.Run(t, new(JobSuite)) }

func (s *JobSuite) refusesID(id, rule string) {
	s.T().Helper()
	var refused *IDError
	s.Require().ErrorAs(ValidateID(id), &refused, id)
	s.Equal(rule, refused.Rule, id)
	s.False(ValidID(id), id)
}

func (s *JobSuite) TestValidateIDNamesTheRuleAnIDBreaks() {
	s.NoError(ValidateID("area/fix-parser.v2"))
	s.refusesID("", "an id is not empty")
	s.refusesID("a..b", "an id never contains ..")
	s.refusesID("a:b", "an id never contains ':'")
	s.refusesID("Fix", "an id is lowercase letters, digits, '-', '.' and '/' between segments")
	s.refusesID("a//b", "'/' goes between segments: an id neither starts nor ends with it, nor doubles it")
	s.refusesID("area/.git", "no segment of an id starts with '.' or '-'")
}

func (s *JobSuite) TestAnIDIsAtMostMaxIDLen() {
	s.NoError(ValidateID(strings.Repeat("a", MaxIDLen)))
	s.refusesID(strings.Repeat("a", MaxIDLen+1), fmt.Sprintf("an id is at most %d characters", MaxIDLen))
}

func (s *JobSuite) TestParseLeaseCheckRefusesWithACheckError() {
	c, err := ParseLeaseCheck("test cmd/magus/ -- -run X")
	s.Require().NoError(err)
	s.Equal(LeaseCheck{Target: "test", Project: "cmd/magus", Args: []string{"-run", "X"}}, c)
	c, err = ParseLeaseCheck("generate --no-default-charms")
	s.Require().NoError(err)
	s.Equal(LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true}, c)
	s.refusesCheck("")
	s.refusesCheck("-o json test .")
	s.refusesCheck("test . extra")
}

func (s *JobSuite) refusesCheck(line string) {
	s.T().Helper()
	_, err := ParseLeaseCheck(line)
	var refused *CheckError
	s.Require().ErrorAs(err, &refused, line)
	s.Equal(line, refused.Line)
}

func (s *JobSuite) TestATimeoutIsEmptyOrAPositiveDuration() {
	d, err := ParseTimeout(" ")
	s.Require().NoError(err)
	s.Zero(d)
	d, err = ParseTimeout("45m")
	s.Require().NoError(err)
	s.Equal(45*time.Minute, d)
	_, err = ParseTimeout("-1h")
	var refused *TimeoutError
	s.Require().ErrorAs(err, &refused)
	s.Equal("-1h", refused.Timeout)
}

func (s *JobSuite) TestAPauseLapsesAtADurationOrAnInstant() {
	now := time.Unix(1000, 0)
	at, err := ParsePauseUntil("2h", now)
	s.Require().NoError(err)
	s.Equal(int64(1000+7200), at)
	at, err = ParsePauseUntil("1970-01-01T00:20:00Z", now)
	s.Require().NoError(err)
	s.Equal(int64(1200), at)
	_, err = ParsePauseUntil("0s", now)
	var refused *PauseUntilError
	s.Require().ErrorAs(err, &refused)
	s.Contains(refused.Error(), "is not ahead of now")
	_, err = ParsePauseUntil("tomorrow", now)
	s.Require().ErrorAs(err, &refused)
	s.Equal("tomorrow", refused.Until)
}

func (s *JobSuite) TestAClaimEvidenceRefusalIsTyped() {
	_, err := ParseClaimEvidence("hunch:x")
	var refused *ClaimEvidenceError
	s.Require().ErrorAs(err, &refused)
	s.Equal("hunch:x", refused.Because)
}

func (s *JobSuite) TestAGoalResolvesItsKindAndExpectation() {
	s.Equal(Goal{ID: "g", Kind: GoalKindCheck, Expect: ExpectPassed}, Goal{ID: "g"}.Resolve())
	s.Equal(Goal{ID: "g", Kind: GoalKindPaths, Expect: ExpectChanged}, Goal{ID: "g", Kind: GoalKindPaths}.Resolve())
	s.Equal(ExpectAbsent, Goal{Kind: GoalKindSymbol, Expect: ExpectAbsent}.Resolve().Expect)
}

func (s *JobSuite) TestACheckGoalsSubjectIsTheCheckAsParseLeaseCheckReadsIt() {
	s.Equal([]string{"test cmd/magus -- -run X"},
		Goal{Kind: GoalKindCheck, Check: LeaseCheck{Target: "test", Project: "cmd/magus", Args: []string{"-run", "X"}}}.Subject())
	s.Equal([]string{"probes/key.buzz"}, Goal{Kind: GoalKindCheck, Check: LeaseCheck{Script: "probes/key.buzz", Test: true}}.Subject())
	s.Equal([]string{"lint ."}, Goal{Kind: GoalKindCheck, Check: LeaseCheck{Target: "lint"}}.Subject())
	s.Nil(Goal{Kind: GoalKindCheck}.Subject())
}

func (s *JobSuite) TestEffectiveGoalsLeadWithThePrimaryCheck() {
	row := Job{Check: &LeaseCheck{Target: "test", Project: "."}, Goals: []Goal{{ID: "docs", Kind: GoalKindPaths, Paths: []string{"docs"}}}}
	goals := row.EffectiveGoals()
	s.Require().Len(goals, 2)
	s.Equal(PrimaryGoalID, goals[0].ID)
	s.Equal(ExpectPassed, goals[0].Expect)
	s.Equal(ExpectChanged, goals[1].Expect, "the declared goal comes back resolved")
	s.Len(Job{}.EffectiveGoals(), 0)
}

func (s *JobSuite) TestStatesSortLiveEditingAndTerminal() {
	s.True(StateExited.Live())
	s.False(StateExited.Editing(), "an exited job holds its checkout and claims no paths")
	s.True(StateRunning.Editing())
	s.True(StateNoReturn.Terminal())
	s.False(State("").Live(), "a job with no state has not said it is live")
	s.False(ValidState(""))
}

func (s *JobSuite) TestCloneSharesNothing() {
	row := Job{
		Paths:     Paths{Write: []string{"a"}},
		Check:     &LeaseCheck{Target: "test", Args: []string{"-run", "X"}},
		ModelTier: &tier.Assignment{Name: "routine", Shared: []string{"x"}},
		Attempt:   &CapturedRun{ExtraArgs: []string{"-v"}},
		Rejection: &Rejection{Violations: []string{"v"}},
	}
	c := row.Clone()
	c.Paths.Write[0] = "b"
	c.Check.Args[0] = "-count"
	c.Check.Target = "lint"
	c.ModelTier.Shared[0] = "y"
	c.Attempt.ExtraArgs[0] = "-race"
	c.Rejection.Violations[0] = "w"
	s.Equal("a", row.Paths.Write[0])
	s.Equal(LeaseCheck{Target: "test", Args: []string{"-run", "X"}}, *row.Check)
	s.Equal("x", row.ModelTier.Shared[0])
	s.Equal("-v", row.Attempt.ExtraArgs[0])
	s.Equal("v", row.Rejection.Violations[0])
	s.Nil(Job{}.Clone().Paths.Write, "nil stays nil")
}

func (s *JobSuite) TestSpecChangedSinceTreatsZeroAsUnknown() {
	row := Job{Generation: 3}
	s.True(row.SpecChangedSince(2))
	s.False(row.SpecChangedSince(3))
	s.False(row.SpecChangedSince(0))
	s.False(Job{}.SpecChangedSince(2))
}

func (s *JobSuite) TestAQueuedJobIsNeverStale() {
	dep := Job{ID: "dep", State: StateRunning, Updated: 10}
	queued := Job{ID: "q", State: StateDeclared, DependsOn: []string{"dep"}, Updated: 10}
	s.False(StaleAt([]Job{dep, queued}, queued, 10_000, time.Minute))
	dep.State = StatePass
	dep.Updated = 50
	since, waiting := IdleSince([]Job{dep, queued}, queued)
	s.False(waiting)
	s.Equal(int64(50), since, "the clock starts at the dependency's last update")
	s.True(StaleAt([]Job{dep, queued}, queued, 50+60, time.Minute))
	s.False(StaleAt([]Job{dep, queued}, queued, 50+60, 0), "a zero window flags nothing")
}

func (s *JobSuite) TestAJobBlockedOnADependencyClaimsNoPaths() {
	a := Job{ID: "a", State: StateRunning, Paths: Paths{Write: []string{"internal/job"}}}
	b := Job{ID: "b", State: StateRunning, Paths: Paths{Write: []string{"internal/job/store.go"}}, DependsOn: []string{"c"}}
	list := NewList([]Job{a, b})
	s.Empty(list.Overlaps)
	s.Equal([]Block{{Job: "b", On: "c"}}, list.Blocked)
	s.Equal("blocked on c which is undeclared", list.Blocked[0].String())
	b.DependsOn = nil
	s.Equal([]Overlap{{JobA: "a", JobB: "b", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job/store.go"}}},
		NewList([]Job{a, b}).Overlaps)
}

func (s *JobSuite) TestAnEmptyListServesNoJobsRatherThanNull() {
	s.Equal([]Job{}, NewList(nil).Jobs)
}

func (s *JobSuite) TestFlagNamesOverdueOrphanedAndStaleJobs() {
	parent := Job{ID: "p", State: StatePass, Updated: 0}
	child := Job{ID: "p/c", Parent: "p", State: StateRunning, Deadline: 5, Updated: 0}
	list := NewList([]Job{parent, child}).Flag(10, time.Second)
	s.Equal([]string{"p/c"}, list.Overdue)
	s.Equal([]string{"p/c"}, list.Orphans)
	s.Equal([]string{"p/c"}, list.Stale)
}

func (s *JobSuite) TestPathsIntersectByPrefixAndClaim() {
	s.True(PathsIntersect("internal/job", "internal/job/store.go"))
	s.True(PathsIntersect("**/*.go", "docs/a.md"), "a pattern with no literal prefix claims the tree")
	s.False(PathsIntersect("internal/guard/*_test.go", "internal/guard/guard.go"))
	s.False(PathsIntersect("run.go#A", "run.go#B"))
	s.True(PathsIntersect("run.go#A", "run.go"))
	s.False(PathsIntersect("", "run.go"), "an entry naming nothing claims nothing")
}

func (s *JobSuite) TestPathWithinOnlyNarrows() {
	s.True(PathWithin("internal/job/store.go", "internal/job"))
	s.True(PathWithin("internal/job/*_test.go", "internal/**/*.go"))
	s.True(PathWithin("run.go#A", "run.go"))
	s.False(PathWithin("run.go", "run.go#A"))
	s.False(PathWithin("internal/**", "internal/*.go"), "two globs of another shape are never within")
}

func (s *JobSuite) TestAnIntegrationBranchIsOneGitTakes() {
	s.True(validIntegratedBranch("job/fix-parser"))
	s.False(validIntegratedBranch(""))
	s.False(validIntegratedBranch("-x"), "git would read it as an option")
	s.False(validIntegratedBranch("a..b"))
	s.False(validIntegratedBranch("a.lock"))
	s.False(validIntegratedBranch("a/"))
	s.False(validIntegratedBranch("a@{1}"))
	s.False(validIntegratedBranch("a b"))
	s.False(validIntegratedBranch("a~1"))
}

func (s *JobSuite) TestAServerRunHasNoAgentAndTheServersOrigin() {
	s.True(Job{RegisteredBy: Origin{EntryPoint: EntryPointServer}}.ServerRun())
	s.False(Job{RegisteredBy: Origin{EntryPoint: EntryPointServer}, Agent: Instructed("x")}.ServerRun())
}

func (s *JobSuite) TestAReadOnlyJobWithNoCheckHoldsNoBuildSeat() {
	s.True(Job{ReadOnly: true}.HoldsNoBuildSeat())
	s.False(Job{ReadOnly: true, Check: &LeaseCheck{Target: "test"}}.HoldsNoBuildSeat())
	s.False(Job{}.HoldsNoBuildSeat())
}

func (s *JobSuite) TestApplyLeavesValidationToTheStore() {
	row := Job{Validation: "kept"}
	Declaration{ID: "a", Check: &LeaseCheck{Target: "test", Project: "cmd/magus/"}}.Apply(&row)
	s.Equal(&LeaseCheck{Target: "test", Project: "cmd/magus"}, row.Check)
	s.Equal("kept", row.Validation)
}

const declared = `{"api_version":"magus.job/v1alpha1","id":"a","later":{"x":[1]}}`

func (s *JobSuite) TestADeclarationKeepsUnknownMembersThroughEncodingJSON() {
	var d Declaration
	s.Require().NoError(json.Unmarshal([]byte(declared), &d))
	s.Equal("a", d.ID)
	s.Equal(map[string]codec.RawMessage{"later": codec.RawMessage(`{"x":[1]}`)}, d.Unknown)
	out, err := json.Marshal(d)
	s.Require().NoError(err)
	s.Contains(string(out), `"later":{"x":[1]}`)
	var back Declaration
	s.Require().NoError(codec.Unmarshal(out, &back))
	s.Equal(d.Unknown, back.Unknown)
}

func (s *JobSuite) TestAStrictDecodeRefusesAnAuthoredDeclarationsUnknownMember() {
	var d Declaration
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(declared), &d), codec.ErrUnknownField)
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(`{"api_version":"magus.job/v1alpha1","id":"a","check":{"target":"t","x":1}}`), &d),
		codec.ErrUnknownField, "and a nested one")
	s.Require().NoError(codec.UnmarshalStrict([]byte(`{"api_version":"magus.job/v1alpha1","id":"a"}`), &d))
	s.Nil(d.Unknown)
}

func (s *JobSuite) TestAResultKeepsUnknownMembersToo() {
	var r Result
	s.Require().NoError(json.Unmarshal([]byte(`{"api_version":"v","job":"a","changed_paths":[],"unresolved_risks":[],"confidence":"high"}`), &r))
	s.Equal(map[string]codec.RawMessage{"confidence": codec.RawMessage(`"high"`)}, r.Unknown)
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(`{"api_version":"v","confidence":"high"}`), &r), codec.ErrUnknownField)
}
