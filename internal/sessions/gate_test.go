package sessions

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
)

// TestGateRoundTrip proves a gate verdict written by one producer is read back
// by another through the folded store, the way two worktrees of one repo share
// it.
func TestGateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	start := session.InvocationStart{Workspace: "/repo", Command: "gate"}
	g := GateResult{
		Target:      "ci",
		Scope:       GateScopeAffected,
		Ref:         "polish",
		Commit:      "abc123",
		Outcome:     session.OutcomePass,
		Fingerprint: "f1",
		Projects:    []string{".", "docs"},
		Charms:      []string{"quiet"},
		// The MGS1028 debt rides the same record: a gate's cost is not readable from
		// its verdict, and the projects only containment selected are the part of it
		// that could not have changed the verdict.
		UndeclaredSeeds: []string{"."},
	}
	require.NoError(t, RecordGate(t.Context(), dir, g, start))

	fold, err := session.ReadAll(t.Context(), dir)
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
	start := session.InvocationStart{Workspace: "/repo"}
	pass := GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: session.OutcomePass, Fingerprint: "f1"}
	require.NoError(t, RecordGate(t.Context(), dir, pass, start))
	// Records order by (Ts, Session, Seq); a same-millisecond write would tie.
	time.Sleep(5 * time.Millisecond)
	fail := GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c2", Outcome: session.OutcomeFail, Fingerprint: "f2"}
	require.NoError(t, RecordGate(t.Context(), dir, fail, start))

	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	rec, ok := LatestGate(fold, "b", "ci")
	require.True(t, ok)
	assert.Equal(t, GateRecord{GateResult: fail, At: rec.At}, rec)
}

// TestLatestGateEmptyStore is the inert state: nothing recorded means no
// finding, so the first gate on a branch always runs.
func TestLatestGateEmptyStore(t *testing.T) {
	fold, err := session.ReadAll(t.Context(), t.TempDir())
	require.NoError(t, err)
	_, ok := LatestGate(fold, "b", "ci")
	assert.False(t, ok)
}

// TestGateAtMatchesTheCommitOnAnyRef pins the lookup the push rule reads: a verdict at a
// commit answers for that commit whichever branch recorded it, an abbreviation matches
// the full id, and a deferral never counts as a verdict.
func TestGateAtMatchesTheCommitOnAnyRef(t *testing.T) {
	dir := t.TempDir()
	start := session.InvocationStart{Workspace: "/repo"}
	require.NoError(t, RecordGate(t.Context(), dir, GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "a", Commit: "a89dee5c7f00", Outcome: session.OutcomePass}, start))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, RecordGate(t.Context(), dir, GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "a89dee5c7f00", Outcome: OutcomeDeferred}, start))

	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	rec, ok := GateAt(fold, "a89dee5c", "ci")
	require.True(t, ok)
	assert.Equal(t, session.OutcomePass, rec.Outcome, "the deferral is skipped for the verdict behind it")

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
	start session.InvocationStart
}

func TestGateScopeSuite(t *testing.T) { suite.Run(t, new(GateScopeSuite)) }

func (s *GateScopeSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.start = session.InvocationStart{Workspace: "/repo"}
}

func (s *GateScopeSuite) record(g GateResult) {
	s.Require().NoError(RecordGate(s.T().Context(), s.dir, g, s.start))
	// Records order by (Ts, Session, Seq); a same-millisecond write would tie.
	time.Sleep(2 * time.Millisecond)
}

func (s *GateScopeSuite) fold() session.Fold {
	fold, err := session.ReadAll(s.T().Context(), s.dir)
	s.Require().NoError(err)
	return fold
}

func (s *GateScopeSuite) TestAProjectPassDoesNotHideARedBranch() {
	red := GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: session.OutcomeFail}
	s.record(red)
	s.record(GateResult{Target: "ci", Scope: GateScopeProject, Ref: "b", Commit: "c1", Outcome: session.OutcomePass, Projects: []string{"libs/cmdline"}})

	rec, ok := LatestGate(s.fold(), "b", "ci")
	s.Require().True(ok)
	s.Equal(red, rec.GateResult, "the affected fail is still the branch's newest verdict")

	rec, ok = GateAt(s.fold(), "c1", "ci")
	s.Require().True(ok)
	s.Equal(red, rec.GateResult, "and the commit's")
}

func (s *GateScopeSuite) TestAProjectPassAloneIsNoBranchVerdict() {
	narrow := GateResult{Target: "ci", Scope: GateScopeProject, Ref: "b", Commit: "c1", Outcome: session.OutcomePass, Projects: []string{"libs/cmdline"}}
	s.record(narrow)

	_, ok := LatestGate(s.fold(), "b", "ci")
	s.False(ok)

	rec, ok := GateAt(s.fold(), "c1", "ci")
	s.Require().True(ok, "the commit's lookup names what ran instead of reporting nothing")
	s.Equal(narrow, rec.GateResult)
	s.False(rec.BranchVerdict())
}

func (s *GateScopeSuite) TestAProjectFailIsARedBranch() {
	s.record(GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: session.OutcomePass})
	red := GateResult{Target: "ci", Scope: GateScopeProject, Ref: "b", Commit: "c1", Outcome: session.OutcomeFail, Projects: []string{"libs/cmdline"}}
	s.record(red)

	rec, ok := LatestGate(s.fold(), "b", "ci")
	s.Require().True(ok)
	s.Equal(red, rec.GateResult)
}

func (s *GateScopeSuite) TestAnEntryWithoutAScopeReadsAsProjectScope() {
	w, err := session.Open(s.T().Context(), s.dir, NewID(), s.start)
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindGateResult, map[string]string{"target": "ci", "ref": "b", "commit": "c1", "outcome": session.OutcomePass}))

	_, ok := LatestGate(s.fold(), "b", "ci")
	s.False(ok, "a pre-scope pass is not the branch gate")

	rec, ok := GateAt(s.fold(), "c1", "ci")
	s.Require().True(ok)
	s.True(rec.Scope.Unscoped(), "and a reader can say why")
	s.False(rec.BranchVerdict())
}

func (s *GateScopeSuite) TestRecordGateRefusesAResultWithNoScope() {
	err := RecordGate(s.T().Context(), s.dir, GateResult{Target: "ci", Ref: "b", Commit: "c1", Outcome: session.OutcomePass}, s.start)
	s.Require().Error(err)
	s.Contains(err.Error(), `want "affected" or "project"`)
	s.Empty(s.fold().Records, "nothing is written, not even the start record")
}

func (s *GateScopeSuite) TestAnUnknownScopeIsAnError() {
	var g GateResult
	s.Error(codec.Unmarshal([]byte(`{"target":"ci","scope":"branch"}`), &g))
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
func (s *FailuresOwedSuite) results(inv string, results ...session.TargetResult) {
	w, err := session.Open(s.T().Context(), s.dir, inv, session.InvocationStart{Workspace: "/repo"})
	s.Require().NoError(err)
	for _, r := range results {
		s.Require().NoError(w.Append(s.T().Context(), session.KindTargetResult, r))
	}
	time.Sleep(2 * time.Millisecond)
}

func (s *FailuresOwedSuite) gate(inv, outcome string) GateRecord {
	s.Require().NoError(RecordGate(s.T().Context(), s.dir, GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: outcome, Inv: inv},
		session.InvocationStart{Workspace: "/repo"}))
	time.Sleep(2 * time.Millisecond)
	fold, err := ReadGateResults(s.T().Context(), s.dir)
	s.Require().NoError(err)
	rec, ok := LatestGate(fold, "b", "ci")
	s.Require().True(ok)
	return rec
}

func (s *FailuresOwedSuite) owed(gate GateRecord) []types.TargetRef {
	fold, err := session.FoldKind(s.T().Context(), s.dir, session.KindTargetResult)
	s.Require().NoError(err)
	return FailuresOwed(gate, fold)
}

func (s *FailuresOwedSuite) TestARedGateOwesEachTargetItFailedButItself() {
	s.results("red",
		session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomeFail},
		session.TargetResult{Project: "docs", Target: "lint", Outcome: session.OutcomeFail},
		session.TargetResult{Project: "docs", Target: "format", Outcome: session.OutcomePass},
		session.TargetResult{Project: ".", Target: "ci", Outcome: session.OutcomeFail},
	)
	red := s.gate("red", session.OutcomeFail)

	s.Equal([]types.TargetRef{{Project: ".", Target: "go-test"}, {Project: "docs", Target: "lint"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestALaterPassOfTheTargetClearsIt() {
	s.results("red",
		session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomeFail},
		session.TargetResult{Project: "docs", Target: "lint", Outcome: session.OutcomeFail},
	)
	red := s.gate("red", session.OutcomeFail)
	s.results("narrow", session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomePass})

	s.Equal([]types.TargetRef{{Project: "docs", Target: "lint"}}, s.owed(red))
}

// A standalone run files `lint:rw`; the gate filed `lint`.
func (s *FailuresOwedSuite) TestAPassUnderACharmClearsTheBareTarget() {
	s.results("red",
		session.TargetResult{Project: ".", Target: "lint", Outcome: session.OutcomeFail},
		session.TargetResult{Project: "docs", Target: "lint", Outcome: session.OutcomeFail},
	)
	red := s.gate("red", session.OutcomeFail)
	s.results("narrow", session.TargetResult{Project: ".", Target: "lint:rw", Outcome: session.OutcomePass, Replayed: true})

	s.Equal([]types.TargetRef{{Project: "docs", Target: "lint"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAFailUnderACharmOwesTheBareTarget() {
	s.results("red", session.TargetResult{Project: ".", Target: "lint:rw", Outcome: session.OutcomeFail})
	red := s.gate("red", session.OutcomeFail)

	s.Equal([]types.TargetRef{{Project: ".", Target: "lint"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAPassOfAnotherTargetUnderACharmClearsNothing() {
	s.results("red", session.TargetResult{Project: ".", Target: "lint", Outcome: session.OutcomeFail})
	red := s.gate("red", session.OutcomeFail)
	s.results("narrow", session.TargetResult{Project: ".", Target: "generate:rw", Outcome: session.OutcomePass})

	s.Equal([]types.TargetRef{{Project: ".", Target: "lint"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAPassInAnotherProjectClearsNothing() {
	s.results("red", session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomeFail})
	red := s.gate("red", session.OutcomeFail)
	s.results("narrow", session.TargetResult{Project: "libs/cmdline", Target: "go-test", Outcome: session.OutcomePass})

	s.Equal([]types.TargetRef{{Project: ".", Target: "go-test"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAPassBeforeTheGateClearsNothing() {
	s.results("earlier", session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomePass})
	s.results("red", session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomeFail})
	red := s.gate("red", session.OutcomeFail)

	s.Equal([]types.TargetRef{{Project: ".", Target: "go-test"}}, s.owed(red))
}

func (s *FailuresOwedSuite) TestAGreenGateOwesNothing() {
	s.results("green", session.TargetResult{Project: ".", Target: "go-test", Outcome: session.OutcomeFail})
	green := s.gate("green", session.OutcomePass)

	s.Empty(s.owed(green))
}

type HarnessWriteSuite struct {
	suite.Suite
	dir string
}

func TestHarnessWriteSuite(t *testing.T) { suite.Run(t, new(HarnessWriteSuite)) }

func (s *HarnessWriteSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *HarnessWriteSuite) record(paths ...string) {
	s.Require().NoError(RecordHarnessWrite(s.T().Context(), s.dir, HarnessWrite{Paths: paths}, session.InvocationStart{Workspace: "/repo"}))
}

func (s *HarnessWriteSuite) latest() (HarnessWriteRecord, bool) {
	fold, err := session.FoldKind(s.T().Context(), s.dir, KindHarnessWrite)
	s.Require().NoError(err)
	return LatestHarnessWrite(fold)
}

func (s *HarnessWriteSuite) TestAWriteReadsBackSortedAndDeduplicated() {
	s.record("b", "a", "b")
	rec, ok := s.latest()
	s.Require().True(ok)
	s.Equal([]string{"a", "b"}, rec.Paths)
}

func (s *HarnessWriteSuite) TestAnEmptyWriteIsRefused() {
	err := RecordHarnessWrite(s.T().Context(), s.dir, HarnessWrite{}, session.InvocationStart{Workspace: "/repo"})
	s.Error(err)
	_, ok := s.latest()
	s.False(ok)
}

func (s *HarnessWriteSuite) TestARepeatAppendsANewerRecord() {
	s.record("a")
	first, _ := s.latest()
	time.Sleep(2 * time.Millisecond)
	s.record("a")
	second, ok := s.latest()
	s.Require().True(ok)
	s.True(second.At.After(first.At))
	s.Equal(first.Paths, second.Paths)
}

func TestLatestRepairSuite(t *testing.T) { suite.Run(t, new(LatestRepairSuite)) }

// LatestRepairSuite pins the record a ci run's refusal of an uncommitted
// repair reads.
type LatestRepairSuite struct {
	suite.Suite
	dir string
}

func (s *LatestRepairSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *LatestRepairSuite) record(g GateResult) {
	s.Require().NoError(RecordGate(s.T().Context(), s.dir, g, session.InvocationStart{Workspace: "/repo"}))
	// Records order by (Ts, Session, Seq); a same-millisecond write would tie.
	time.Sleep(2 * time.Millisecond)
}

func (s *LatestRepairSuite) fold() session.Fold {
	fold, err := session.ReadAll(s.T().Context(), s.dir)
	s.Require().NoError(err)
	return fold
}

func (s *LatestRepairSuite) repaired(ref, commit string) GateResult {
	return GateResult{
		Target: "ci", Scope: GateScopeAffected, Ref: ref, Commit: commit, Outcome: session.OutcomeFail,
		RepairedFiles: []types.RepairedFile{{Path: "a.txt", Digest: "d1"}},
	}
}

func (s *LatestRepairSuite) TestRepairedFilesRoundTrip() {
	s.record(s.repaired("b", "c1"))

	rec, ok := LatestRepair(s.fold(), "ci")
	s.Require().True(ok)
	s.Equal([]types.RepairedFile{{Path: "a.txt", Digest: "d1"}}, rec.RepairedFiles)
}

func (s *LatestRepairSuite) TestALaterPassWithoutRepairsDoesNotHideIt() {
	s.record(s.repaired("b", "c1"))
	s.record(GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c2", Outcome: session.OutcomePass})

	rec, ok := LatestRepair(s.fold(), "ci")
	s.Require().True(ok)
	s.Equal("c1", rec.Commit)
}

func (s *LatestRepairSuite) TestAnyRefIsRead() {
	s.record(s.repaired("feature", "c1"))

	rec, ok := LatestRepair(s.fold(), "ci")
	s.Require().True(ok)
	s.Equal("feature", rec.Ref)
}

func (s *LatestRepairSuite) TestTheNewestRepairWins() {
	s.record(s.repaired("b", "c1"))
	s.record(s.repaired("b", "c2"))

	rec, ok := LatestRepair(s.fold(), "ci")
	s.Require().True(ok)
	s.Equal("c2", rec.Commit)
}

func (s *LatestRepairSuite) TestNoRepairRecordedIsNone() {
	s.record(GateResult{Target: "ci", Scope: GateScopeAffected, Ref: "b", Commit: "c1", Outcome: session.OutcomeFail})

	_, ok := LatestRepair(s.fold(), "ci")
	s.False(ok)
}

type GateFoldSuite struct{ suite.Suite }

func TestGateFold(t *testing.T) { suite.Run(t, new(GateFoldSuite)) }

// The gate read folds only gate verdicts, and a gate result appended later is seen by
// the next read.
func (s *GateFoldSuite) TestReadGateResultsFoldsOnlyGateVerdicts() {
	t := s.T()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []session.Record{{
		V: session.APIVersion, Invocation: "a", Seq: 1, Kind: session.KindAgentEvent, Ts: 1,
		Payload: fmt.Appendf(nil, `{"host":"h","event":"file.read","ref":"1","at":1,"text":%q}`, "x.go"),
	}})
	s.Require().NoError(RecordGate(s.T().Context(), dir, GateResult{Target: "ci", Scope: GateScopeAffected, Commit: "abc123", Outcome: session.OutcomePass}, session.InvocationStart{}))

	_, err := session.FoldAgentEvents(s.T().Context(), dir)
	s.Require().NoError(err)
	gates, err := ReadGateResults(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(gates.Records, 1)
	s.Equal(KindGateResult, gates.Records[0].Kind)

	s.Require().NoError(RecordGate(s.T().Context(), dir, GateResult{Target: "ci", Scope: GateScopeAffected, Commit: "def456", Outcome: session.OutcomeFail}, session.InvocationStart{}))
	gates, err = ReadGateResults(s.T().Context(), dir)
	s.Require().NoError(err)
	rec, ok := GateAt(gates, "def456", "ci")
	s.Require().True(ok)
	s.Equal(session.OutcomeFail, rec.Outcome)
}
