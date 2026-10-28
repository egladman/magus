package sessions

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestGateRoundTrip proves a gate verdict written by one producer is read back
// by another through the folded store, the way two worktrees of one repo share
// it.
func TestGateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	start := InvocationStart{Workspace: "/repo", Command: "gate"}
	g := GateResult{
		Target:      "ci",
		Scope:       GateScopeAffected,
		Ref:         "polish",
		Commit:      "abc123",
		Outcome:     OutcomePass,
		Fingerprint: "f1",
		Projects:    []string{".", "docs"},
		Charms:      []string{"quiet"},
		// The MGS1028 debt rides the same record: a gate's cost is not readable from
		// its verdict, and the projects only containment selected are the part of it
		// that could not have changed the verdict.
		UndeclaredSeeds: []string{"."},
	}
	require.NoError(t, RecordGate(dir, g, start))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	rec, ok := LatestGate(fold, "polish", "ci")
	require.True(t, ok)
	assert.Equal(t, g, rec.GateResult)
	assert.WithinDuration(t, time.Now(), rec.At, time.Minute)

	_, ok = LatestGate(fold, "other-branch", "ci")
	assert.False(t, ok, "a record is scoped to its ref")
	_, ok = LatestGate(fold, "polish", "nightly")
	assert.False(t, ok, "and to its target")
}

// TestGateNewestWins proves a later fail supersedes an earlier pass: the
// redundancy check must see a red branch, not the stale green behind it.
func TestGateNewestWins(t *testing.T) {
	dir := t.TempDir()
	start := InvocationStart{Workspace: "/repo"}
	pass := GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: OutcomePass, Fingerprint: "f1"}
	require.NoError(t, RecordGate(dir, pass, start))
	// Records order by (Ts, Session, Seq); a same-millisecond write would tie.
	time.Sleep(5 * time.Millisecond)
	fail := GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c2", Outcome: OutcomeFail, Fingerprint: "f2"}
	require.NoError(t, RecordGate(dir, fail, start))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	rec, ok := LatestGate(fold, "b", "ci")
	require.True(t, ok)
	assert.Equal(t, GateRecord{GateResult: fail, At: rec.At}, rec)
}

// TestLatestGateEmptyStore is the inert state: nothing recorded means no
// finding, so the first gate on a branch always runs.
func TestLatestGateEmptyStore(t *testing.T) {
	fold, err := ReadAll(t.TempDir())
	require.NoError(t, err)
	_, ok := LatestGate(fold, "b", "ci")
	assert.False(t, ok)
}

// TestGateAtMatchesTheCommitOnAnyRef pins the lookup the push rule reads: a verdict at a
// commit answers for that commit whichever branch recorded it, an abbreviation matches
// the full id, and a deferral never counts as a verdict.
func TestGateAtMatchesTheCommitOnAnyRef(t *testing.T) {
	dir := t.TempDir()
	start := InvocationStart{Workspace: "/repo"}
	require.NoError(t, RecordGate(dir, GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "a", Commit: "a89dee5c7f00", Outcome: OutcomePass}, start))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, RecordGate(dir, GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "a89dee5c7f00", Outcome: OutcomeDeferred}, start))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	rec, ok := GateAt(fold, "a89dee5c", "ci")
	require.True(t, ok)
	assert.Equal(t, OutcomePass, rec.Outcome, "the deferral is skipped for the verdict behind it")

	_, ok = GateAt(fold, "0000000", "ci")
	assert.False(t, ok)
	_, ok = GateAt(fold, "", "ci")
	assert.False(t, ok, "no commit is no answer, not a match on everything")
}

// GateScopeSuite pins what a gate record's scope lets it speak for (running-doc entry
// 277): a passing `magus run ci libs/cmdline` read as the branch gate while the last
// `magus affected ci` had failed.
type GateScopeSuite struct {
	suite.Suite
	dir   string
	start InvocationStart
}

func TestGateScopeSuite(t *testing.T) { suite.Run(t, new(GateScopeSuite)) }

func (s *GateScopeSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.start = InvocationStart{Workspace: "/repo"}
}

func (s *GateScopeSuite) record(g GateResult) {
	s.Require().NoError(RecordGate(s.dir, g, s.start))
	// Records order by (Ts, Session, Seq); a same-millisecond write would tie.
	time.Sleep(2 * time.Millisecond)
}

func (s *GateScopeSuite) fold() Fold {
	fold, err := ReadAll(s.dir)
	s.Require().NoError(err)
	return fold
}

func (s *GateScopeSuite) TestAProjectPassDoesNotHideARedBranch() {
	red := GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: OutcomeFail}
	s.record(red)
	s.record(GateResult{Target: "ci", Scope: GateScopeProject, Ref: "b", Commit: "c1", Outcome: OutcomePass, Projects: []string{"libs/cmdline"}})

	rec, ok := LatestGate(s.fold(), "b", "ci")
	s.Require().True(ok)
	s.Equal(red, rec.GateResult, "the affected fail is still the branch's newest verdict")

	rec, ok = GateAt(s.fold(), "c1", "ci")
	s.Require().True(ok)
	s.Equal(red, rec.GateResult, "and the commit's")
}

func (s *GateScopeSuite) TestAProjectPassAloneIsNoBranchVerdict() {
	narrow := GateResult{Target: "ci", Scope: GateScopeProject, Ref: "b", Commit: "c1", Outcome: OutcomePass, Projects: []string{"libs/cmdline"}}
	s.record(narrow)

	_, ok := LatestGate(s.fold(), "b", "ci")
	s.False(ok)

	rec, ok := GateAt(s.fold(), "c1", "ci")
	s.Require().True(ok, "the commit's lookup names what ran instead of reporting nothing")
	s.Equal(narrow, rec.GateResult)
	s.False(rec.BranchVerdict())
}

func (s *GateScopeSuite) TestAProjectFailIsARedBranch() {
	s.record(GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: OutcomePass})
	red := GateResult{Target: "ci", Scope: GateScopeProject, Ref: "b", Commit: "c1", Outcome: OutcomeFail, Projects: []string{"libs/cmdline"}}
	s.record(red)

	rec, ok := LatestGate(s.fold(), "b", "ci")
	s.Require().True(ok)
	s.Equal(red, rec.GateResult)
}

func (s *GateScopeSuite) TestAnEntryWithoutAScopeReadsAsProjectScope() {
	w, err := Open(s.dir, NewID(), s.start)
	s.Require().NoError(err)
	s.Require().NoError(w.Append(KindGateResult, map[string]string{"target": "ci", "ref": "b", "commit": "c1", "outcome": OutcomePass}))

	_, ok := LatestGate(s.fold(), "b", "ci")
	s.False(ok, "a pre-scope pass is not the branch gate")

	rec, ok := GateAt(s.fold(), "c1", "ci")
	s.Require().True(ok)
	s.True(rec.Scope.Unscoped(), "and a reader can say why")
	s.False(rec.BranchVerdict())
}

func (s *GateScopeSuite) TestRecordGateRefusesAResultWithNoScope() {
	err := RecordGate(s.dir, GateResult{Target: "ci", Ref: "b", Commit: "c1", Outcome: OutcomePass}, s.start)
	s.Require().Error(err)
	s.Contains(err.Error(), `want "affected" or "project"`)
	s.Empty(s.fold().Records, "nothing is written, not even the start record")
}

func (s *GateScopeSuite) TestAnUnknownScopeIsAnError() {
	var g GateResult
	s.Error(json.Unmarshal([]byte(`{"target":"ci","scope":"branch"}`), &g))
}

// FailuresOwedSuite pins which targets a red gate still owes a pass of its own, the record
// the guard refuses an unchanged repeat of that gate on.
type FailuresOwedSuite struct {
	suite.Suite
	dir string
}

func TestFailuresOwedSuite(t *testing.T) { suite.Run(t, new(FailuresOwedSuite)) }

func (s *FailuresOwedSuite) SetupTest() { s.dir = s.T().TempDir() }

// results files one invocation's target results, then waits out the millisecond so the
// next record sorts after them.
func (s *FailuresOwedSuite) results(inv string, results ...TargetResult) {
	w, err := Open(s.dir, inv, InvocationStart{Workspace: "/repo"})
	s.Require().NoError(err)
	for _, r := range results {
		s.Require().NoError(w.Append(KindTargetResult, r))
	}
	time.Sleep(2 * time.Millisecond)
}

func (s *FailuresOwedSuite) gate(inv, outcome string) GateRecord {
	s.Require().NoError(RecordGate(s.dir, GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: outcome, Inv: inv},
		InvocationStart{Workspace: "/repo"}))
	time.Sleep(2 * time.Millisecond)
	fold, err := ReadGateResults(s.dir)
	s.Require().NoError(err)
	rec, ok := LatestGate(fold, "b", "ci")
	s.Require().True(ok)
	return rec
}

func (s *FailuresOwedSuite) owed(gate GateRecord) []GateFailure {
	fold, err := ReadKind(s.dir, KindTargetResult)
	s.Require().NoError(err)
	return FailuresOwed(gate, fold)
}

func (s *FailuresOwedSuite) TestARedGateOwesEachTargetItFailedButItself() {
	s.results("red",
		TargetResult{Project: ".", Target: "go-test", Outcome: OutcomeFail},
		TargetResult{Project: "docs", Target: "lint", Outcome: OutcomeFail},
		TargetResult{Project: "docs", Target: "format", Outcome: OutcomePass},
		TargetResult{Project: ".", Target: "ci", Outcome: OutcomeFail},
	)
	red := s.gate("red", OutcomeFail)

	s.Equal([]GateFailure{{Project: ".", Target: "go-test"}, {Project: "docs", Target: "lint"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestALaterPassOfTheTargetClearsIt() {
	s.results("red",
		TargetResult{Project: ".", Target: "go-test", Outcome: OutcomeFail},
		TargetResult{Project: "docs", Target: "lint", Outcome: OutcomeFail},
	)
	red := s.gate("red", OutcomeFail)
	s.results("narrow", TargetResult{Project: ".", Target: "go-test", Outcome: OutcomePass})

	s.Equal([]GateFailure{{Project: "docs", Target: "lint"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAPassInAnotherProjectClearsNothing() {
	s.results("red", TargetResult{Project: ".", Target: "go-test", Outcome: OutcomeFail})
	red := s.gate("red", OutcomeFail)
	s.results("narrow", TargetResult{Project: "libs/cmdline", Target: "go-test", Outcome: OutcomePass})

	s.Equal([]GateFailure{{Project: ".", Target: "go-test"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAPassBeforeTheGateClearsNothing() {
	s.results("earlier", TargetResult{Project: ".", Target: "go-test", Outcome: OutcomePass})
	s.results("red", TargetResult{Project: ".", Target: "go-test", Outcome: OutcomeFail})
	red := s.gate("red", OutcomeFail)

	s.Equal([]GateFailure{{Project: ".", Target: "go-test"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAGreenGateOwesNothing() {
	s.results("green", TargetResult{Project: ".", Target: "go-test", Outcome: OutcomeFail})
	green := s.gate("green", OutcomePass)

	s.Empty(s.owed(green))
}
