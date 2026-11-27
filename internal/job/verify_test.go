package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ScriptEvidenceSuite struct {
	suite.Suite
	row  job.Job
	gate job.Postcondition
}

func TestScriptEvidenceSuite(t *testing.T) { suite.Run(t, new(ScriptEvidenceSuite)) }

func (s *ScriptEvidenceSuite) SetupTest() {
	s.row = job.Job{ID: "job-a"}
	s.gate = job.Postcondition{ID: "g", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Script: "p.buzz", Args: []string{"x"}})}
}

func (s *ScriptEvidenceSuite) attempt(lease string, args []string) job.CapturedRun {
	return job.CapturedRun{Found: true, Project: ".", Target: "buzz p.buzz", Lease: lease, ExtraArgs: args}
}

func (s *ScriptEvidenceSuite) TestRunOfThisJobWithTheCheckArgsVerifies() {
	got := verifyGate(s.row, s.gate, "ref", s.attempt("job-a", []string{"x"}), Observed{})
	s.True(got.Verified, "%v", got.Violations)
}

func (s *ScriptEvidenceSuite) TestRunWithOtherArgsDoesNotVerify() {
	got := verifyGate(s.row, s.gate, "ref", s.attempt("job-a", []string{"y"}), Observed{})
	s.False(got.Verified)
}

func (s *ScriptEvidenceSuite) TestRunWithNoArgsDoesNotSatisfyACheckWithArgs() {
	got := verifyGate(s.row, s.gate, "ref", s.attempt("job-a", nil), Observed{})
	s.False(got.Verified)
}

func (s *ScriptEvidenceSuite) TestRunUnderAnotherLeaseNamesThatJob() {
	got := verifyGate(s.row, s.gate, "ref", s.attempt("job-b", []string{"x"}), Observed{})
	s.False(got.Verified)
	s.Contains(strings.Join(got.Violations, "\n"), "another job's evidence")
}

func (s *ScriptEvidenceSuite) TestRunUnderNoLeaseSaysSo() {
	got := verifyGate(s.row, s.gate, "ref", s.attempt("", []string{"x"}), Observed{})
	s.False(got.Verified)
	s.Contains(strings.Join(got.Violations, "\n"), "acted under no lease")
}

func acceptRow() job.Job {
	check := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}}
	return job.Job{
		ID:         "harness/ledger-accept",
		Paths:      job.Paths{Write: []string{"internal/ledger", "cmd/magus/ledger.go", "docs/reference/*.md"}},
		Check:      fixtureCheck(check),
		Validation: CheckCommand(check),
		State:      job.StateRunning,
	}
}

func TestFindingsDelivery(t *testing.T) {
	row := acceptRow()
	row.Delivery = job.DeliveryFindings
	rep := passingResult()
	rep.ChangedPaths = nil
	rep.Findings = []string{"The nested lookup made 54 allocations."}
	seen := claimed(rep)
	grade := func() Status { return VerifyGates(row, rep, passingRun, nil, nil, seen) }
	got := grade()
	require.True(t, got.Verified, "%v", got.Violations)

	seen.Changed = []string{"scratch.go"}
	assert.False(t, grade().Verified)
	seen.Changed = nil
	seen.ChangedKnown = false
	assert.False(t, grade().Verified)
	seen.ChangedKnown = true
	rep.Findings = []string{"  "}
	assert.False(t, grade().Verified)
	rep.Findings = []string{"The nested lookup made 54 allocations."}
	rep.Validation.OutputRef = ""
	assert.False(t, grade().Verified)
}

func passingResult() job.Result {
	return job.Result{
		Schema:       job.Schema{Version: job.ResultAPIVersion},
		Job:          "harness/ledger-accept",
		ChangedPaths: []string{"internal/ledger/report.go", "cmd/magus/ledger.go"},
		Validation: job.ResultValidation{
			Command:   "magus run go::go-test . -- -run Ledger",
			OutputRef: "a1b2c3d4",
		},
		UnresolvedRisks: []string{},
	}
}

// passingRun is what the output store recorded for a passing run of acceptRow's own
// validation, which is what an honest result cites. The zero Attempt is a ref the store
// does not hold.
var passingRun = job.CapturedRun{Found: true, Project: ".", Target: "go-test", Spell: "go"}

// verifyClaim grades a result against a diff that shows exactly what it claims, so a case
// about another rule is not failed by the diff check.
func verifyClaim(row job.Job, rep job.Result, att job.CapturedRun, declared []job.Job) Status {
	return VerifyGates(row, rep, att, nil, declared, claimed(rep))
}

func claimed(rep job.Result) Observed {
	return Observed{Changed: rep.ChangedPaths, ChangedKnown: true, TestSelection: goCheckSelection}
}

func postconditionRow() job.Job {
	return job.Job{
		ID:    "harness/gated",
		Paths: job.Paths{Write: []string{"internal/job"}},
		State: job.StateRunning,
		Postconditions: []job.Postcondition{
			{ID: "unit", Description: "job package tests pass", Check: fixtureCheck(types.LeaseCheck{Target: "go::go-test", Project: "."})},
			{ID: "docs", Description: "docs checks pass", Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "docs"}), DependsOn: []string{"unit"}},
		},
	}
}

func postconditionResult() job.Result {
	return job.Result{
		Schema:       job.Schema{Version: job.ResultAPIVersion},
		Job:          "harness/gated",
		ChangedPaths: []string{"internal/job/verify.go"},
		VerificationEvidence: []job.VerificationEvidence{
			{GateID: "unit", OutputRef: "unit-ref"},
			{GateID: "docs", OutputRef: "docs-ref"},
		},
		UnresolvedRisks: []string{},
	}
}

func postconditionAttempts() []job.PostconditionAttempt {
	return []job.PostconditionAttempt{
		{PostconditionID: "unit", Attempt: job.CapturedRun{Found: true, Ref: "unit-ref", Project: ".", Target: "go-test", Spell: "go"}},
		{PostconditionID: "docs", Attempt: job.CapturedRun{Found: true, Ref: "docs-ref", Project: "docs", Target: "test"}},
	}
}

func TestVerifyTakesAResultInsideTheBoundary(t *testing.T) {
	t.Parallel()

	got := verifyClaim(acceptRow(), passingResult(), passingRun, nil)
	assert.True(t, got.Clean(), "a verified job carries no risk")
	got.UnresolvedRisks = nil
	assert.Equal(t, Status{
		Job:      "harness/ledger-accept",
		Verified: true,
		Command:  "magus run go::go-test . -- -run Ledger",
		// The primary check reports as the gate it is. See the note in lifecycle_test.go.
		Gates: []job.VerificationStatus{{ID: job.PrimaryPostconditionID, OutputRef: passingResult().Validation.OutputRef, Verified: true}},
	}, got)
}

func TestVerifyPostconditionsRequireEvidenceForEveryDeclaredGate(t *testing.T) {
	t.Parallel()

	status := VerifyGates(postconditionRow(), postconditionResult(), job.CapturedRun{}, postconditionAttempts(), nil, claimed(postconditionResult()))
	assert.True(t, status.Verified, status.Violations)
	require.Len(t, status.Gates, 2)
	assert.True(t, status.Gates[0].Verified)
	assert.True(t, status.Gates[1].Verified)
}

func TestVerifyPostconditionsRejectMissingOrWrongEvidence(t *testing.T) {
	t.Parallel()

	rep := postconditionResult()
	rep.VerificationEvidence = rep.VerificationEvidence[:1]
	status := VerifyGates(postconditionRow(), rep, job.CapturedRun{}, postconditionAttempts()[:1], nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), `postcondition "docs"`)

	rep = postconditionResult()
	attempts := postconditionAttempts()
	attempts[1].Attempt.Project = "."
	status = VerifyGates(postconditionRow(), rep, job.CapturedRun{}, attempts, nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "different run")
}

func TestVerifyPostconditionsRejectUndeclaredEvidence(t *testing.T) {
	t.Parallel()

	rep := postconditionResult()
	rep.VerificationEvidence = append(rep.VerificationEvidence, job.VerificationEvidence{GateID: "invented", OutputRef: "invented-ref"})
	attempts := append(postconditionAttempts(), job.PostconditionAttempt{
		PostconditionID: "invented",
		Attempt:         job.CapturedRun{Found: true, Ref: "invented-ref", Project: ".", Target: "go-test"},
	})
	status := VerifyGates(postconditionRow(), rep, job.CapturedRun{}, attempts, nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "undeclared postcondition \"invented\"")
}

// A job with postconditions and no check has no gate for validation.unrun to name, and
// a run reported as missing still fails the job.
func TestVerifyPostconditionsRejectAnUnrunCheckTheJobDoesNotDeclare(t *testing.T) {
	t.Parallel()

	rep := postconditionResult()
	rep.Validation.Unrun = "docker is down"
	status := VerifyGates(postconditionRow(), rep, job.CapturedRun{}, postconditionAttempts(), nil, claimed(rep))
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "declares no check")
}

// TestVerifyCIGatePassesOnAGreenGateWithATrivialDelta: a check gate on ci passes on a
// green ci gate when the change since it tiers trivial, with no output ref of its own and
// even though that gate ran before the job was declared.
func TestVerifyCIGatePassesOnAGreenGateWithATrivialDelta(t *testing.T) {
	t.Parallel()

	ciRow := func(target, project string) job.Job {
		row := acceptRow()
		row.Created = 100
		row.Check = fixtureCheck(types.LeaseCheck{Target: target, Project: project})
		return row
	}
	rep := passingResult()
	rep.Validation.OutputRef = ""
	green := GreenGate{Commit: "c1", Projects: []string{".", "docs"}, Tier: check.RiskTrivial}

	seen := claimed(rep)
	seen.GreenGate = green
	status := VerifyGates(ciRow("ci", "."), rep, job.CapturedRun{}, nil, nil, seen)
	assert.True(t, status.Verified, status.Violations)
	assert.Equal(t, []job.VerificationStatus{{ID: job.PrimaryPostconditionID, Verified: true}}, status.Gates)

	for name, tc := range map[string]struct {
		row  job.Job
		gate GreenGate
	}{
		"no green gate":               {ciRow("ci", "."), GreenGate{}},
		"a mechanical delta":          {ciRow("ci", "."), GreenGate{Commit: "c1", Projects: []string{"."}, Tier: check.RiskMechanical}},
		"a scoped delta":              {ciRow("ci", "."), GreenGate{Commit: "c1", Projects: []string{"."}, Tier: check.RiskScoped}},
		"a project the gate skipped":  {ciRow("ci", "site"), green},
		"a check that is not ci":      {ciRow("go::go-test", "."), green},
		"a spell-qualified ci target": {ciRow("go::ci", "."), green},
	} {
		t.Run(name, func(t *testing.T) {
			seen := claimed(rep)
			seen.GreenGate = tc.gate
			status := VerifyGates(tc.row, rep, job.CapturedRun{}, nil, nil, seen)
			assert.False(t, status.Verified)
			assert.Contains(t, strings.Join(status.Violations, "\n"), "carries no output_ref")
		})
	}
}

func TestVerifyRejectsEvidenceCapturedBeforeJobDeclaration(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Created = 100
	attempt := passingRun
	attempt.TimestampMs = 99_999
	status := verifyClaim(row, passingResult(), attempt, nil)
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "before job declaration")
}

// The declaration is stamped in whole seconds, so a run recorded in that same second
// cannot be ordered against it, and a fork followed at once by its check is that case.
func TestVerifyAcceptsEvidenceFromTheSecondOfDeclaration(t *testing.T) {
	t.Parallel()

	for _, ms := range []int64{100_000, 100_999} {
		row := acceptRow()
		row.Created = 100
		attempt := passingRun
		attempt.TimestampMs = ms
		status := verifyClaim(row, passingResult(), attempt, nil)
		assert.True(t, status.Verified, "%d: %v", ms, status.Violations)
	}
}

func TestVerifyPostconditionDependenciesPropagate(t *testing.T) {
	t.Parallel()

	row := postconditionRow()
	row.Postconditions = append(row.Postconditions, job.Postcondition{
		ID: "publish", Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "docs"}), DependsOn: []string{"docs"},
	})
	rep := postconditionResult()
	rep.VerificationEvidence = append(rep.VerificationEvidence, job.VerificationEvidence{GateID: "publish", OutputRef: "publish-ref"})
	attempts := append(postconditionAttempts(), job.PostconditionAttempt{
		PostconditionID: "publish", Attempt: job.CapturedRun{Found: true, Ref: "publish-ref", Project: "docs", Target: "test"},
	})
	attempts[0].Attempt.Failed = true
	status := VerifyGates(row, rep, job.CapturedRun{}, attempts, nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, status.Gates[1].Violations, `depends_on postcondition "unit" has not verified`)
	assert.Contains(t, status.Gates[2].Violations, `depends_on postcondition "docs" has not verified`)
}

func TestVerifyPostconditionsEnforceJobDependencies(t *testing.T) {
	t.Parallel()

	row := postconditionRow()
	row.DependsOn = []string{"upstream"}
	status := VerifyGates(row, postconditionResult(), job.CapturedRun{}, postconditionAttempts(), []job.Job{{ID: "upstream", State: job.StateExited}}, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "upstream")
}

// Every rule reports independently: an orchestrator repartitions for one violation and
// re-runs the check for another, so a verdict that stopped at the first would send it to
// the wrong remedy half the time.
func TestVerifyNamesEveryViolation(t *testing.T) {
	t.Parallel()

	rep := passingResult()
	rep.Job = "harness/other"
	rep.ChangedPaths = append(rep.ChangedPaths, "internal/sessions/store.go")
	rep.Validation.OutputRef = ""

	v := verifyClaim(acceptRow(), rep, job.CapturedRun{}, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 3)
	assert.Contains(t, strings.Join(v.Violations, "\n"), "internal/sessions/store.go")
}

// .gitattributes is regenerated beside a change to any output glob and no job declares it,
// so a result naming it outside the write paths verifies, with a note saying it was passed
// over; any other path outside them is still a violation.
func TestVerifyNotesAMagusMaintainedPathOutsideTheWritePaths(t *testing.T) {
	t.Parallel()

	rep := passingResult()
	rep.ChangedPaths = append(rep.ChangedPaths, ".gitattributes")
	v := verifyClaim(acceptRow(), rep, passingRun, nil)
	assert.True(t, v.Verified, "%q", v.Violations)
	require.Len(t, v.Notes, 1)
	assert.Contains(t, v.Notes[0], `".gitattributes"`)
	assert.Contains(t, v.Notes[0], "magus maintains itself")

	var out strings.Builder
	RenderGates(&out, v)
	assert.Contains(t, out.String(), fmt.Sprintf("note: %s\n", v.Notes[0]))

	rep.ChangedPaths = append(rep.ChangedPaths, "internal/sessions/store.go")
	v = verifyClaim(acceptRow(), rep, passingRun, nil)
	assert.False(t, v.Verified)
	assert.Equal(t, []string{`changed path "internal/sessions/store.go" is outside the job's write paths` +
		` (internal/ledger, cmd/magus/ledger.go, docs/reference/*.md)`}, v.Violations)
}

// The result the coding-agent persona filed on 2026-09-11 and had ACCEPTED: nothing
// changed, a ref from an unrelated codegen run, a self-asserted pass, and two fields
// nobody asked for. Every one of them is now a named rejection, which is the whole of
// what "grade evidence, not assertions" means.
func TestVerifyRefusesTheFabricatedResult(t *testing.T) {
	t.Parallel()

	raw := `{"api_version":"safetyscissors.job-result/v1alpha1","job":"harness/ledger-accept","changed_paths":[],` +
		`"validation":{"command":"magus run go::go-test .","output_ref":"deadbeef","passed":true},` +
		`"unresolved_risks":[],"confidence":"high"}`

	_, err := DecodeResult(strings.NewReader(raw))
	require.Error(t, err, "the unknown members alone stop it at the door")
	assert.Contains(t, err.Error(), "passed")

	// Decoded by hand, as the fields it invented were never read anyway: the rules that
	// would have caught it even in a result shaped correctly.
	rep := passingResult()
	rep.ChangedPaths = nil
	rep.Validation.OutputRef = "deadbeef"
	v := verifyClaim(acceptRow(), rep, job.CapturedRun{Found: true, Project: ".", Target: "generate"}, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 3)
	assert.Contains(t, v.Violations[0], "no changed paths at all")
	assert.Contains(t, v.Violations[1], "nothing in the diff")
	assert.Contains(t, v.Violations[2], "magus run generate .")
	assert.Contains(t, v.Violations[2], "go::go-test")
}

// The stored run's own exit status is the verdict, which is the field a holder no longer
// gets to assert.
func TestVerifyReadsTheStoredRunsOutcome(t *testing.T) {
	t.Parallel()

	failed := passingRun
	failed.Failed = true
	v := verifyClaim(acceptRow(), passingResult(), failed, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "failed")
}

// A passing run of the same check by another lease, in a checkout the two share, proves
// nothing about this job's work.
func TestVerifyRefusesAnotherLeasesRun(t *testing.T) {
	t.Parallel()

	theirs := passingRun
	theirs.Lease = "harness/other"
	v := verifyClaim(acceptRow(), passingResult(), theirs, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], `"harness/other"`)
	assert.Contains(t, v.Violations[0], `"harness/ledger-accept"`)
}

// The holder's own run is its evidence, and so is a run under no lease at all: the
// orchestrator runs checks unleased, which is the run `job wait --integration` grades.
func TestVerifyAcceptsTheHoldersRunAndAnUnleasedOne(t *testing.T) {
	t.Parallel()

	for name, lease := range map[string]string{"the holder's": "harness/ledger-accept", "an unleased": ""} {
		att := passingRun
		att.Lease = lease
		v := verifyClaim(acceptRow(), passingResult(), att, nil)
		assert.True(t, v.Verified, "%s run: %v", name, v.Violations)
	}
}

// The args a run forwarded after `--` are part of what it ran: a run with another flag does
// not stand in for the check. Another -run pattern selects tests inside the same packages,
// so it does. A record with no args is not compared.
func TestVerifyComparesTheRunsRecordedArgs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		ok   bool
	}{
		{name: "the check's own args", args: []string{"-run", "Ledger"}, ok: true},
		{name: "a record with none", args: nil, ok: true},
		{name: "a run scoped elsewhere", args: []string{"-run", "Other"}, ok: true},
		{name: "a run with more args", args: []string{"-run", "Ledger", "-short"}, ok: false},
	} {
		att := passingRun
		att.ExtraArgs = tc.args
		v := verifyClaim(acceptRow(), passingResult(), att, nil)
		assert.Equal(t, tc.ok, v.Verified, "%s: %v", tc.name, v.Violations)
		if !tc.ok {
			require.Len(t, v.Violations, 1, tc.name)
			assert.Contains(t, v.Violations[0], "-- "+strings.Join(tc.args, " "), "the message shows the args the run forwarded")
		}
	}
}

// A go test run scoped with -run or -skip is a run of the check's target and project over
// the same packages, so it is evidence, and the grade notes the scope. A scoped run of
// another target or project, or one with another flag, is still another run.
func TestVerifyGradesAScopedRunOfTheCheck(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Check = fixtureCheck(types.LeaseCheck{Target: "go::go-test", Project: "."})
	scoped := passingRun
	scoped.ExtraArgs = []string{"-run", "Spawn|Waiter"}

	v := verifyClaim(row, passingResult(), scoped, nil)
	assert.True(t, v.Verified, "%q", v.Violations)
	require.Len(t, v.Notes, 1)
	assert.Equal(t, "postcondition \"check\" was graded on output ref \"a1b2c3d4\", a run scoped with `-- -run Spawn|Waiter`:"+
		" -run and -skip select tests inside the packages `magus run go::go-test .` runs, so it ran every package the check does", v.Notes[0])
	require.Len(t, v.Gates, 1)
	assert.Equal(t, job.VerificationStatus{ID: "check", Verified: true, OutputRef: "a1b2c3d4", Scope: "-- -run Spawn|Waiter"}, v.Gates[0])
	var out strings.Builder
	RenderGates(&out, v)
	assert.Contains(t, out.String(), "  [met] check, a run scoped with `-- -run Spawn|Waiter`\nnote: postcondition \"check\" was graded on output ref \"a1b2c3d4\", a run scoped")

	for name, att := range map[string]job.CapturedRun{
		"another target":  {Found: true, Project: ".", Target: "lint", Spell: "go", ExtraArgs: []string{"-run", "Spawn"}},
		"another project": {Found: true, Project: "docs", Target: "go-test", Spell: "go", ExtraArgs: []string{"-run", "Spawn"}},
		"another flag":    {Found: true, Project: ".", Target: "go-test", Spell: "go", ExtraArgs: []string{"-run", "Spawn", "-short"}},
	} {
		v := verifyClaim(row, passingResult(), att, nil)
		assert.False(t, v.Verified, name)
		assert.Contains(t, strings.Join(v.Violations, "\n"), "different run", name)
		assert.Empty(t, v.Notes, name)
	}

	lint := types.LeaseCheck{Target: "go::lint", Project: "."}
	assert.False(t, ranCheck(lint, job.CapturedRun{Found: true, Project: ".", Target: "lint", Spell: "go", ExtraArgs: []string{"-run", "X"}}, nil, goCheckSelection),
		"-run scopes go test only")
}

func TestWithoutTestSelectionDropsEverySpelling(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"-short", "-count=1"},
		withoutTestSelection([]string{"-run", "A", "-short", "--skip=B", "-test.run", "C", "-count=1", "--test.skip", "D"}))
	assert.Equal(t, []string{}, withoutTestSelection(nil))
	assert.Equal(t, []string{"run", "---run", "x"}, withoutTestSelection([]string{"run", "---run", "x"}), "not a flag go test reads")
}

// The check is the postcondition named check: a result may file its evidence under
// verification_evidence
// by that id instead of validation, and its snapshot grades it the same.
func TestVerifyTakesTheChecksEvidenceUnderItsPostconditionID(t *testing.T) {
	t.Parallel()

	rep := passingResult()
	ref := rep.Validation.OutputRef
	rep.VerificationEvidence = []job.VerificationEvidence{{GateID: job.PrimaryPostconditionID, OutputRef: ref}}
	snapshots := []job.PostconditionAttempt{{PostconditionID: job.PrimaryPostconditionID, Attempt: passingRun}}

	v := VerifyGates(acceptRow(), rep, passingRun, snapshots, nil, claimed(rep))
	assert.True(t, v.Verified, "%q", v.Violations)
	assert.Equal(t, []job.VerificationStatus{{ID: job.PrimaryPostconditionID, OutputRef: ref, Verified: true}}, v.Gates)

	only := rep
	only.Validation.OutputRef = ""
	v = VerifyGates(acceptRow(), only, job.CapturedRun{}, snapshots, nil, claimed(only))
	assert.True(t, v.Verified, "evidence filed only under verification_evidence: %q", v.Violations)

	two := rep
	two.Validation.OutputRef = "other-ref"
	v = VerifyGates(acceptRow(), two, passingRun, snapshots, nil, claimed(two))
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations, `the result names output ref "other-ref" for postcondition "check" in validation and "a1b2c3d4" in verification_evidence; name one run`)
}

// A row with no check has nothing to bind evidence to, and accepting it anyway is how a
// ref from any run at all passes for evidence.
func TestVerifyRefusesARowWithNoCheckToBindTo(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Check, row.Validation = job.Check{}, ""

	v := verifyClaim(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "declares no check and no postcondition")
}

// A read-only row with no check is graded by the result its holder filed. The
// wait that collects it passes it and carries its risks.
func TestVerifyPassesAReadOnlyRowItsHolderGraded(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "survey", ReadOnly: true, State: job.StateExited}
	rep := job.Result{Job: "survey", UnresolvedRisks: []string{"the cache layer was not read"}}

	v := VerifyGates(row, rep, job.CapturedRun{}, nil, nil, Observed{})

	assert.True(t, v.Verified, "%v", v.Violations)
	assert.Equal(t, rep.UnresolvedRisks, v.UnresolvedRisks)
}

// The binary's spelling is a way of running one target, so it may not decide whether the
// evidence binds; nor may a check's args when the run recorded none.
func TestCheckBindsOnIdentityNotSpelling(t *testing.T) {
	t.Parallel()

	att := job.CapturedRun{Found: true, Project: "internal/ledger", Target: "test"}
	for _, line := range []string{
		"magus run test internal/ledger",
		"./magus run test internal/ledger",
		"magus run test internal/ledger -- -run Ledger",
	} {
		c, err := ParseCheckCommand(line)
		require.NoError(t, err, line)
		assert.True(t, ranCheck(c, att, nil, goCheckSelection), line)
	}

	c, err := ParseCheckCommand("magus run test cmd/magus")
	require.NoError(t, err)
	assert.False(t, ranCheck(c, att, nil, goCheckSelection), "another project is another run")
}

// A script check binds to the run `magus buzz --record` stored for that script, and to no
// target run or other script.
func TestScriptCheckBindsToItsRecordedRun(t *testing.T) {
	t.Parallel()

	c := types.LeaseCheck{Script: "probes/key.buzz"}
	assert.True(t, ranCheck(c, job.CapturedRun{Found: true, Project: ".", Target: "buzz probes/key.buzz"}, []string{"rw"}, goCheckSelection))
	assert.True(t, ranCheck(c, job.CapturedRun{Found: true, Project: ".", Target: "buzz ./probes/key.buzz"}, nil, goCheckSelection), "one path spelled two ways")
	assert.False(t, ranCheck(c, job.CapturedRun{Found: true, Project: ".", Target: "buzz probes/other.buzz"}, nil, goCheckSelection), "another script")
	assert.False(t, ranCheck(c, job.CapturedRun{Found: true, Project: ".", Target: "test"}, nil, goCheckSelection), "a target run")
}

// Passing tests and a passing main are different facts, so a test check binds to the
// `buzz -t` run alone and a script check to the main run alone.
func TestScriptTestCheckBindsOnlyToItsTestRun(t *testing.T) {
	t.Parallel()

	main := job.CapturedRun{Found: true, Project: ".", Target: "buzz probes/key.buzz"}
	tests := job.CapturedRun{Found: true, Project: ".", Target: "buzz -t probes/key.buzz"}
	script := types.LeaseCheck{Script: "probes/key.buzz"}
	tested := types.LeaseCheck{Script: "probes/key.buzz", Test: true}

	assert.True(t, ranCheck(tested, tests, nil, goCheckSelection))
	assert.False(t, ranCheck(tested, main, nil, goCheckSelection), "a main run is not evidence of passing tests")
	assert.True(t, ranCheck(script, main, nil, goCheckSelection))
	assert.False(t, ranCheck(script, tests, nil, goCheckSelection), "a test run is not evidence of a passing main")
	assert.False(t, ranCheck(tested, job.CapturedRun{Found: true, Project: ".", Target: "buzz -t probes/other.buzz"}, nil, goCheckSelection), "another script")
}

// A CHARM is part of a run's identity, not a spelling of it. The store records what was
// invoked, so the charmless `generate` that GATES drift and the `generate:rw` that WRITES
// it are two runs; accepting either for the other made a drift gate satisfiable by the
// run that produces the drift.
func TestCheckBindsOnCharm(t *testing.T) {
	t.Parallel()

	written := job.CapturedRun{Found: true, Project: ".", Target: "generate:rw"}
	gated := job.CapturedRun{Found: true, Project: ".", Target: "generate"}

	charmless, err := ParseCheckCommand("magus run generate .")
	require.NoError(t, err)
	assert.False(t, ranCheck(charmless, written, nil, goCheckSelection), "a written run is not evidence of a gated one")
	assert.True(t, ranCheck(charmless, gated, nil, goCheckSelection))

	rw, err := ParseCheckCommand("magus run generate:rw .")
	require.NoError(t, err)
	assert.True(t, ranCheck(rw, written, nil, goCheckSelection))
	assert.False(t, ranCheck(rw, gated, nil, goCheckSelection), "a gated run is not evidence of a written one")
}

// A check is resolved the way `magus run` resolves it, so in a workspace whose
// default_charms is rw, running the check verbatim records `test:rw` and satisfies it. The
// charm set is still compared after resolution: a run without the defaults, of another
// target or in another project is another run.
func TestCheckBindsUnderDefaultCharms(t *testing.T) {
	t.Parallel()

	defaults := []string{types.CharmReadWrite}
	cases := []struct {
		name  string
		check string
		att   job.CapturedRun
		want  bool
	}{
		{"the default charm applied", "magus run test .", job.CapturedRun{Project: ".", Target: "test:rw"}, true},
		{"an explicit rw", "magus run test:rw .", job.CapturedRun{Project: ".", Target: "test:rw"}, true},
		{"its own charm stacked on the default", "magus run test:verify .", job.CapturedRun{Project: ".", Target: "test:verify,rw"}, true},
		{"narrowing args, which the store does not record", "magus run test . -- -run X", job.CapturedRun{Project: ".", Target: "test:rw"}, true},
		{"a spell filter", "magus run go::go-test .", job.CapturedRun{Project: ".", Target: "go-test:rw", Spell: "go"}, true},
		{"ci, which never runs with rw", "magus run ci .", job.CapturedRun{Project: ".", Target: "ci"}, true},
		{"a different target", "magus run test .", job.CapturedRun{Project: ".", Target: "lint:rw"}, false},
		{"a different project", "magus run test docs", job.CapturedRun{Project: ".", Target: "test:rw"}, false},
		{"a run without the defaults", "magus run test .", job.CapturedRun{Project: ".", Target: "test"}, false},
		{"an extra charm", "magus run test .", job.CapturedRun{Project: ".", Target: "test:rw,verify"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := ParseCheckCommand(tc.check)
			require.NoError(t, err)
			tc.att.Found = true
			assert.Equal(t, tc.want, ranCheck(c, tc.att, defaults, goCheckSelection))
		})
	}
}

// A check opting out of default_charms is the charmless drift gate an rw-default workspace
// still needs: only a --no-default-charms run satisfies it, and the command served for it
// carries the flag.
func TestCheckOptsOutOfDefaultCharms(t *testing.T) {
	t.Parallel()

	defaults := []string{types.CharmReadWrite}
	gated := job.CapturedRun{Found: true, Project: ".", Target: "generate"}
	written := job.CapturedRun{Found: true, Project: ".", Target: "generate:rw"}

	optOut := types.LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true}
	assert.True(t, ranCheck(optOut, gated, defaults, goCheckSelection), "the --no-default-charms run")
	assert.False(t, ranCheck(optOut, written, defaults, goCheckSelection), "a written run is not evidence of a gated one")

	plain := types.LeaseCheck{Target: "generate", Project: "."}
	assert.True(t, ranCheck(plain, written, defaults, goCheckSelection), "a plain check still binds the default-charm run")
	assert.False(t, ranCheck(plain, gated, defaults, goCheckSelection))

	assert.Equal(t, "magus run generate . --no-default-charms", CheckCommand(optOut))
	assert.Equal(t, "magus run generate .", CheckCommand(plain))
}

// The opt-out is a field on the check record: a check line carrying the flag sets it, and
// a declared record keeps it.
func TestCheckOptOutIsARecordField(t *testing.T) {
	t.Parallel()

	parsed, err := parseLeaseCheck("generate . --no-default-charms")
	require.NoError(t, err)
	assert.True(t, parsed.NoDefaultCharms)

	decl, _, err := decodeDeclaration(declaration(`"id":"adj/drift"`,
		`"check":{"target":"generate","project":".","no_default_charms":true}`))
	require.NoError(t, err)
	var row job.Job
	decl.Apply(&row)
	require.True(t, !row.Check.IsZero())
	assert.True(t, checkIn(t, row.Check).NoDefaultCharms)
	assert.Equal(t, "magus run generate . --no-default-charms", validationLine(row))
}

// A rejected run names the check as it resolved, so the reader sees which charm set the
// evidence had to carry.
func TestCheckGateUnderDefaultCharms(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "fix/job-store"}
	gate := job.Postcondition{ID: "check", Kind: job.PostconditionKindCheck, Expect: job.ExpectPassed, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."})}
	seen := Observed{DefaultCharms: []string{types.CharmReadWrite}}

	status := verifyGate(row, gate, "out57a24bec47f2", job.CapturedRun{Found: true, Project: ".", Target: "test:rw"}, seen)
	assert.True(t, status.Verified, status.Violations)

	status = verifyGate(row, gate, "out57a24bec47f2", job.CapturedRun{Found: true, Project: ".", Target: "lint:rw"}, seen)
	assert.False(t, status.Verified)
	require.Len(t, status.Violations, 1)
	assert.Contains(t, status.Violations[0], "this postcondition's check runs `magus run test:rw .`")
}

// A run the job wrote after grades other files, so its pass is not this job's; the run is
// named by attempt id, which no later run of the same step takes over.
func TestCheckGateRefusesARunTheJobWroteAfter(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "fix/job-store"}
	gate := job.Postcondition{ID: "check", Kind: job.PostconditionKindCheck, Expect: job.ExpectPassed, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."})}
	run := job.CapturedRun{Found: true, Ref: "out57a24bec47f2", AttemptID: "att1", Project: ".", Target: "test"}

	status := verifyGate(row, gate, run.Ref, run, Observed{Stale: map[string]string{"out57a24bec47f2": "the ref, not the run"}})
	assert.True(t, status.Verified, status.Violations)

	status = verifyGate(row, gate, run.Ref, run, Observed{Stale: map[string]string{"att1": "a.go changed after it started"}})
	assert.False(t, status.Verified)
	require.Len(t, status.Violations, 1)
	assert.Contains(t, status.Violations[0], "predates the job's last write")
	assert.Contains(t, status.Violations[0], "a.go changed after it started")
}

// A directory declaration covers what is under it and a glob covers only what it matches.
// The second half is the one worth pinning: falling back to a glob's literal prefix would
// accept exactly the writes the declaration excludes.
func TestVerifyReadsDeclarationsAsWrittenIn(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		path string
		want bool
	}{
		"under a declared directory":  {"internal/ledger/store.go", true},
		"the declared file itself":    {"cmd/magus/ledger.go", true},
		"matching a declared glob":    {"docs/reference/lease.md", true},
		"below a declared glob":       {"docs/reference/manpage/magus-run.md", false},
		"a sibling of a declared dir": {"internal/ledgers/store.go", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := passingResult()
			rep.ChangedPaths = []string{tc.path}
			v := verifyClaim(acceptRow(), rep, passingRun, nil)
			assert.Equal(t, tc.want, v.Verified, v.Violations)
		})
	}
}

// A lease over the whole tree covers every path in it. The declaration cleans to ".", and
// reading that as covering nothing made every reported path a violation.
func TestVerifyReadsARootDeclarationAsTheWholeTree(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Paths.Write = []string{"."}
	assert.True(t, verifyClaim(row, passingResult(), passingRun, nil).Verified)

	row.Paths.Write = []string{""}
	assert.False(t, verifyClaim(row, passingResult(), passingRun, nil).Verified, "a blank declaration claims nothing")
}

// The deny list is read at acceptance too: a path can be inside the owned write paths and still
// be one the row was told to leave alone.
func TestVerifyRejectsADeniedPath(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Paths.Deny = []string{"internal/ledger/store.go"}
	rep := passingResult()
	rep.ChangedPaths = []string{"internal/ledger/store.go"}

	v := verifyClaim(row, rep, passingRun, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "denied")
}

// A descendant the ledger does not carry is a branch of the plan nobody is tracking,
// which is the fact the field was added to surface.
func TestVerifyRejectsADescendantNoRowDeclares(t *testing.T) {
	t.Parallel()

	rep := passingResult()
	rep.Descendants = []string{"harness/ledger-accept/child", "harness/ghost"}
	declared := []job.Job{{ID: "harness/ledger-accept/child"}}

	v := verifyClaim(acceptRow(), rep, passingRun, declared)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "harness/ghost")
}

// A row somebody already verified is not re-verified: the second verdict overwrites the
// first without anybody being told the first existed.
func TestVerifyRefusesARowThatIsAlreadyClosed(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.State = job.StatePass

	v := verifyClaim(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "already pass")
}

// The ref is the whole of the evidence, so a ref that no longer resolves is a rejection
// rather than a detail: the root cannot reopen a run that is not there.
func TestVerifyRejectsEvidenceTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()

	v := verifyClaim(acceptRow(), passingResult(), job.CapturedRun{}, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "a1b2c3d4")
}

// A read-only lease has no write set, so any claimed write is a violation of a boundary
// the row declared by being read-only rather than by listing paths.
func TestVerifyRefusesWritesFromAReadOnlyLease(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.ReadOnly, row.Paths.Write = true, nil

	v := verifyClaim(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "read-only")
}

// The schema is what a host compiles a holder's response format against, and it is
// generated: a field on one side and not the other means the generator has not been run,
// so a result would validate against a shape the verifier does not read.
func TestResultSchemaMatchesTheStruct(t *testing.T) {
	t.Parallel()

	var schema, validation struct {
		Properties map[string]codec.RawMessage `json:"properties"`
	}
	require.NoError(t, codec.Unmarshal([]byte(ResultSchema), &schema))
	require.NoError(t, codec.Unmarshal(schema.Properties["validation"], &validation))

	assert.ElementsMatch(t, jsonFields(job.Result{}), keys(schema.Properties))
	assert.ElementsMatch(t, jsonFields(job.ResultValidation{}), keys(validation.Properties))
}

// jsonFields is the wire name of every field a struct serializes, which is the set the
// schema has to describe.
func jsonFields(v any) []string {
	var out []string
	for _, f := range reflect.VisibleFields(reflect.TypeOf(v)) {
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous || !f.IsExported() || name == "-" || opts == "unknown" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func keys(m map[string]codec.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestVerifyGatesGradesTheFootprintAgainstDeclarationClaims(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "unit", Created: 1, Paths: job.Paths{Write: []string{"run.go#executeStages"}}, Check: fixtureCheck(types.LeaseCheck{Target: "go-test", Project: "."})}
	rep := job.Result{Job: "unit", ChangedPaths: []string{"run.go"}}
	placed := func(decl string) job.RegionChange {
		return job.RegionChange{File: job.FileChange{Path: "run.go"}, Side: job.RegionNew, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
	}
	seen := func(regions ...job.RegionChange) Observed {
		return Observed{Changed: []string{"run.go"}, ChangedKnown: true, ChangedFrom: "abc1234", Regions: regions, RegionsKnown: true}
	}
	const outside = "the diff since abc1234 changed run.go#func RunCI() {, outside every declaration the job claims in run.go (executeStages)"

	inside := VerifyGates(row, rep, job.CapturedRun{}, nil, []job.Job{row}, seen(placed("func (m *Magus) executeStages() {"), placed("")))
	assert.NotContains(t, inside.Violations, outside)
	assert.Empty(t, inside.FootprintUnclaimed, "its own declaration and the preamble")

	wandered := VerifyGates(row, rep, job.CapturedRun{}, nil, []job.Job{row}, seen(placed("func RunCI() {")))
	assert.Contains(t, wandered.Violations, outside)
	assert.Equal(t, []string{"run.go#func RunCI() {"}, wandered.FootprintUnclaimed)

	unread := Observed{Changed: []string{"run.go"}, ChangedKnown: true, RegionsReason: "git does not report changed regions (RegionReporter)"}
	blind := VerifyGates(row, rep, job.CapturedRun{}, nil, []job.Job{row}, unread)
	assert.Contains(t, blind.Violations, "job unit claims declarations and its footprint is not known (git does not report changed regions (RegionReporter)), so no claim could be checked")

	whole := row
	whole.Paths.Write = []string{"run.go"}
	assert.NotContains(t, VerifyGates(whole, rep, job.CapturedRun{}, nil, []job.Job{whole}, unread).Violations,
		"job unit claims declarations and its footprint is not known (git does not report changed regions (RegionReporter)), so no claim could be checked",
		"a job claiming no declaration is not graded on its footprint")
}

// A deny path naming a declaration is graded against the footprint, the way a declaration
// claim is: a change elsewhere in the file is no violation, a change to the named one is.
func TestVerifyGatesGradesTheFootprintAgainstDeclarationDenies(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "unit", Created: 1, Paths: job.Paths{Write: []string{"run.go"}, Deny: []string{"run.go#executeStages"}}, Check: fixtureCheck(types.LeaseCheck{Target: "go-test", Project: "."})}
	rep := job.Result{Job: "unit", ChangedPaths: []string{"run.go"}}
	placed := func(decl string) job.RegionChange {
		return job.RegionChange{File: job.FileChange{Path: "run.go"}, Side: job.RegionNew, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
	}
	unplaced := job.RegionChange{File: job.FileChange{Path: "run.go"}, Side: job.RegionNew, Lines: [2]int{4, 4}}
	verify := func(seen Observed) []string {
		return VerifyGates(row, rep, job.CapturedRun{}, nil, []job.Job{row}, seen).Violations
	}
	seen := func(regions ...job.RegionChange) Observed {
		return Observed{Changed: []string{"run.go"}, ChangedKnown: true, ChangedFrom: "abc1234", Regions: regions, RegionsKnown: true}
	}
	denied := func(violations []string) []string {
		return slices.DeleteFunc(slices.Clone(violations), func(v string) bool { return !strings.Contains(v, "denied") })
	}

	assert.Empty(t, denied(verify(seen(placed("func RunCI() {"), placed("")))), "another declaration and the preamble")
	assert.Equal(t, []string{"the diff since abc1234 changed run.go#func (m *Magus) executeStages() {, which the job is denied (run.go#executeStages)"},
		denied(verify(seen(placed("func RunCI() {"), placed("func (m *Magus) executeStages() {")))))
	assert.Equal(t, []string{"the diff since abc1234 changed run.go, which the job is denied (run.go#executeStages)"},
		denied(verify(seen(unplaced))), "a region no driver placed could be the declaration")
	assert.Equal(t, []string{`changed path "run.go" has declarations the job is denied (run.go#executeStages) and its footprint is not known (no regions), so the deny could not be checked`},
		denied(verify(Observed{Changed: []string{"run.go"}, ChangedKnown: true, RegionsReason: "no regions"})))

	whole := row
	whole.Paths.Deny = []string{"run.go"}
	assert.Equal(t, []string{`changed path "run.go" is one the job is denied (run.go)`},
		denied(VerifyGates(whole, rep, job.CapturedRun{}, nil, []job.Job{whole}, seen(placed("func RunCI() {"))).Violations))
}

func TestUnclaimedFootprint(t *testing.T) {
	t.Parallel()

	placed := func(path, decl string) job.RegionChange {
		return job.RegionChange{File: job.FileChange{Path: path}, Side: job.RegionNew, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
	}
	unplaced := job.RegionChange{File: job.FileChange{Path: "run.go"}, Side: job.RegionNew, Lines: [2]int{4, 4}}
	for _, tc := range []struct {
		name       string
		writePaths []string
		releases   []job.Release
		regions    []job.RegionChange
		want       []string
	}{
		{
			name:       "a pattern covers a file whole beside a claim on it",
			writePaths: []string{"**/*.go", "run.go#executeStages"},
			regions:    []job.RegionChange{placed("run.go", "func RunCI() {")},
		},
		{
			name:       "a pattern no longer covers a file its row released",
			writePaths: []string{"**/*.go", "run.go#executeStages"},
			releases:   []job.Release{{Path: "run.go", Digest: "sha256:ab"}},
			regions:    []job.RegionChange{placed("run.go", "func RunCI() {"), placed("order.go", "func Order() {")},
			want:       []string{"run.go#func RunCI() {"},
		},
		{
			name:       "inside the claimed declaration",
			writePaths: []string{"run.go#executeStages"},
			regions:    []job.RegionChange{placed("run.go", "func (m *Magus) executeStages(ctx context.Context) error {")},
		},
		{
			name:       "a neighbour is outside, once however many hunks land in it",
			writePaths: []string{"run.go#executeStages"},
			regions:    []job.RegionChange{placed("run.go", "func RunCI() {"), placed("run.go", "func RunCI() {")},
			want:       []string{"run.go#func RunCI() {"},
		},
		{
			name:       "the preamble is exempt",
			writePaths: []string{"run.go#executeStages"},
			regions:    []job.RegionChange{placed("run.go", "")},
		},
		{
			name:       "a line no driver placed is the whole file",
			writePaths: []string{"run.go#executeStages"},
			regions:    []job.RegionChange{unplaced},
			want:       []string{"run.go"},
		},
		{
			name:       "a file also claimed whole is not graded by declaration",
			writePaths: []string{"run.go#executeStages", "."},
			regions:    []job.RegionChange{placed("run.go", "func RunCI() {")},
		},
		{
			name:       "a file claimed by no entry is the path rules' to report",
			writePaths: []string{"run.go#executeStages"},
			regions:    []job.RegionChange{placed("order.go", "func Order() {")},
		},
		{
			name:       "any of several claims on the file names it",
			writePaths: []string{"run.go#executeStages", "run.go#RunCI"},
			regions:    []job.RegionChange{placed("run.go", "func RunCI() {")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, unclaimedFootprint(job.Job{Paths: job.Paths{Write: tc.writePaths}, Releases: tc.releases}, tc.regions))
		})
	}
}

// integrationRow seeds a job checked by `test .` with one more check postcondition, in state.
func integrationRow(t *testing.T, state job.State) *Store {
	t.Helper()
	s := tempStore(t, t.TempDir())
	_, err := s.Update(t.Context(), "w", func(u *job.Job) {
		job.Declaration{ID: "w", Paths: job.Paths{Write: []string{"a.go"}}, Check: forkCheck(), Postconditions: []job.Postcondition{
			{ID: "lint", Check: fixtureCheck(types.LeaseCheck{Target: "lint", Project: "docs"})},
		}}.Apply(u)
		u.State = state
	})
	require.NoError(t, err)
	return s
}

// runs resolves each ref to a recorded run of target in project, failed when named so.
func runs(byRef map[string][2]string, failed ...string) AttemptResolver {
	return func(_ context.Context, ref string) (job.CapturedRun, error) {
		run, ok := byRef[ref]
		if !ok {
			return job.CapturedRun{}, nil
		}
		return job.CapturedRun{Found: true, Ref: ref, TimestampMs: time.Now().Add(time.Minute).UnixMilli(),
			Target: run[0], Project: run[1], Failed: slices.Contains(failed, ref)}, nil
	}
}

func integrationEvidence() job.Result {
	return job.Result{Job: "w", Validation: job.ResultValidation{OutputRef: "out1"},
		VerificationEvidence: []job.VerificationEvidence{{GateID: "lint", OutputRef: "out2"}}}
}

// A job that already passed where it was written gains an integration grade from runs in
// the merged tree, and its state stays pass.
func TestWaitIntegrationRecordsBesideAPassedJob(t *testing.T) {
	t.Parallel()

	s := integrationRow(t, job.StatePass)
	resolve := runs(map[string][2]string{"out1": {"test", "."}, "out2": {"lint", "docs"}})
	grade, err := WaitIntegration(t.Context(), s, "w", integrationEvidence(), resolve, "/merged", nil)
	require.NoError(t, err)
	assert.True(t, grade.Verified, "%+v", grade.Gates)
	assert.Equal(t, []string{"check", "lint"}, []string{grade.Gates[0].ID, grade.Gates[1].ID})

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, job.StatePass, rows[0].State)
	require.NotNil(t, rows[0].Integration)
	assert.Equal(t, "/merged", rows[0].Integration.Checkout)
	assert.NotZero(t, rows[0].Integration.At)
	assert.True(t, rows[0].Integration.Verified)
}

// A failing run in the merged tree records verified=false, names the postcondition, and moves
// nothing else.
func TestWaitIntegrationRecordsAFailureWithoutMovingTheState(t *testing.T) {
	t.Parallel()

	s := integrationRow(t, job.StateExited)
	resolve := runs(map[string][2]string{"out1": {"test", "."}, "out2": {"lint", "docs"}}, "out2")
	grade, err := WaitIntegration(t.Context(), s, "w", integrationEvidence(), resolve, "/merged", nil)
	require.NoError(t, err)
	assert.False(t, grade.Verified)
	assert.True(t, grade.Gates[0].Verified)
	assert.Contains(t, strings.Join(grade.Gates[1].Violations, "\n"), "failed")

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	assert.Equal(t, job.StateExited, rows[0].State)
	assert.False(t, rows[0].Integration.Verified)
}

func TestWaitIntegrationRefusesEvidenceForAPostconditionItDoesNotHold(t *testing.T) {
	t.Parallel()

	s := integrationRow(t, job.StatePass)
	rep := integrationEvidence()
	rep.VerificationEvidence = append(rep.VerificationEvidence, job.VerificationEvidence{GateID: "nope", OutputRef: "out3"})
	_, err := WaitIntegration(t.Context(), s, "w", rep, runs(nil), "/merged", nil)
	require.ErrorContains(t, err, `postcondition "nope"`)
	rows, err := s.List(t.Context())
	require.NoError(t, err)
	assert.Nil(t, rows[0].Integration, "a refused grade records nothing")
}

// The integrator's validators run the checks in its tree under their own leases, so a
// run under another lease grades integration; plain wait still refuses it.
func TestWaitIntegrationAcceptsARunUnderAnotherLease(t *testing.T) {
	t.Parallel()

	s := integrationRow(t, job.StatePass)
	byRef := runs(map[string][2]string{"out1": {"test", "."}, "out2": {"lint", "docs"}})
	resolve := func(ctx context.Context, ref string) (job.CapturedRun, error) {
		att, err := byRef(ctx, ref)
		att.Lease = "root/validate"
		return att, err
	}
	grade, err := WaitIntegration(t.Context(), s, "w", integrationEvidence(), resolve, "/merged", nil)
	require.NoError(t, err)
	assert.True(t, grade.Verified, "%+v", grade.Gates)

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	att, err := resolve(t.Context(), "out1")
	require.NoError(t, err)
	plain := verifyGate(rows[0], rows[0].EffectivePostconditions()[0], "out1", att, Observed{})
	assert.False(t, plain.Verified)
	assert.Contains(t, strings.Join(plain.Violations, "\n"), `"root/validate"`)
}

// Symbol postconditions a merge can break are graded against the integrator's graph, and `changed`,
// which is the holder's diff, is not graded there at all.
func TestWaitIntegrationGradesSymbolPostconditionsAgainstTheCheckoutsGraph(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	_, err := s.Update(t.Context(), "w", func(u *job.Job) {
		job.Declaration{ID: "w", Paths: job.Paths{Write: []string{"a.go"}}, Postconditions: []job.Postcondition{
			{ID: "moved", Kind: job.PostconditionKindSymbol, Symbols: []string{"Api"}},
			{ID: "shape", Kind: job.PostconditionKindSymbol, Expect: job.ExpectSignature, Symbols: []string{"Api"}, Signature: "func Api()"},
			{ID: "gone", Kind: job.PostconditionKindSymbol, Expect: job.ExpectAbsent, Symbols: []string{"Legacy"}},
		}}.Apply(u)
		u.State = job.StatePass
	})
	require.NoError(t, err)

	merged := postconditionGraph{{id: "symbol:go a/Api().", label: "Api", file: "a.go", signature: "func Api(n int)"}}
	grade, err := WaitIntegration(t.Context(), s, "w", job.Result{Job: "w"}, runs(nil), "/merged", GraphSymbols(merged))
	require.NoError(t, err)
	assert.False(t, grade.Verified)
	require.Len(t, grade.Gates, 2, "the changed postcondition belongs to the holder's checkout")
	wantShape := grade.Gates[0]
	wantShape.ID = "shape"
	wantShape.Violations = []string{"\"Api\" is declared `func Api(n int)`, and this postcondition expects `func Api()`"}
	assert.Equal(t, wantShape, grade.Gates[0])
	assert.Equal(t, job.VerificationStatus{ID: "gone", Verified: true}, grade.Gates[1])

	blind, err := WaitIntegration(t.Context(), s, "w", job.Result{Job: "w"}, runs(nil), "/merged", nil)
	require.NoError(t, err)
	assert.False(t, blind.Verified, "a graph nobody read never passes a symbol postcondition")
	assert.Contains(t, strings.Join(blind.Gates[1].Violations, "\n"), "could not read the symbol graph")
}

func TestVerifyGradesAnIntegrationRowOnTheIntegratorsRuns(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "integrate", Integrates: []string{"a", "b"}, Paths: job.Paths{Write: []string{"a.go", "b.go"}}, State: job.StateRunning}
	rep := job.Result{
		Schema:               job.Schema{Version: job.ResultAPIVersion},
		Job:                  "integrate",
		ChangedPaths:         []string{"a.go", "MAGUS.md"},
		VerificationEvidence: []job.VerificationEvidence{{GateID: "go-test", OutputRef: "test-ref"}, {GateID: "lint", OutputRef: "lint-ref"}},
	}
	attempts := []job.PostconditionAttempt{
		{PostconditionID: "go-test", Attempt: job.CapturedRun{Found: true, Ref: "test-ref", Project: ".", Target: "go-test"}},
		{PostconditionID: "lint", Attempt: job.CapturedRun{Found: true, Ref: "lint-ref", Project: ".", Target: "lint"}},
	}
	status := VerifyGates(row, rep, job.CapturedRun{}, attempts, nil, Observed{Changed: []string{"a.go", "b.go", "c.go"}, ChangedKnown: true})
	assert.True(t, status.Verified, "the merge range is the change, and the integrator's runs are declared evidence: %v", status.Violations)
	require.Len(t, status.Gates, 2)

	rep.VerificationEvidence = rep.VerificationEvidence[:1]
	status = VerifyGates(row, rep, job.CapturedRun{}, attempts[:1], nil, Observed{})
	assert.False(t, status.Verified, "the integrator's lint run is required, not optional")
	assert.Contains(t, strings.Join(status.Violations, "\n"), `postcondition "lint"`)
}

func TestIntegratorPostconditionsLeaveADeclaredPostconditionOnTheTarget(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "integrate", Integrates: []string{"a"}, Check: fixtureCheck(types.LeaseCheck{Target: "go-test", Project: "."})}
	postconditions, err := integratorPostconditions(row)
	require.NoError(t, err)
	require.Len(t, postconditions, 1)
	assert.Equal(t, "lint", postconditions[0].ID)
	none, err := integratorPostconditions(acceptRow())
	require.NoError(t, err)
	assert.Empty(t, none, "a job that integrates nothing holds only what it declares")
}

// A check the environment kept from running is filed with its cause and
// fails, naming it: it is never a pass nobody earned.
func TestVerifyFailsACheckFiledAsUnrunNamingTheCause(t *testing.T) {
	t.Parallel()

	rep := passingResult()
	rep.Validation = job.ResultValidation{Command: acceptRow().Validation, Unrun: "node_modules is missing"}
	v := verifyClaim(acceptRow(), rep, job.CapturedRun{}, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Gates, 1)
	assert.Equal(t, []string{"did not run: node_modules is missing; it fails until the check runs where that is fixed"}, v.Gates[0].Violations)
}

// A worker never waits for a seat: a check it files as owed for want of one is a normal
// result, passed with the postcondition marked owed for the integration. Any other reason, a postcondition
// that is not a check, and an integration filing its own checks as owed are refused.
func TestVerifyPassesAWorkerWhoseCheckIsOwed(t *testing.T) {
	t.Parallel()

	owed := func(reason string) job.Result {
		rep := passingResult()
		rep.Validation = job.ResultValidation{Command: acceptRow().Validation, Owed: reason}
		return rep
	}
	v := verifyClaim(acceptRow(), owed(OwedNoSeat), job.CapturedRun{}, nil)
	assert.Equal(t, Status{Job: acceptRow().ID, Verified: true, UnresolvedRisks: []string{}, Command: acceptRow().Validation, Gates: []job.VerificationStatus{{ID: "check", Owed: OwedNoSeat}},
		Notes: []string{"postcondition \"check\" is owed (no seat): the integration that merges harness/ledger-accept runs it"}}, v)
	var out strings.Builder
	RenderGates(&out, v)
	assert.Contains(t, out.String(), "harness/ledger-accept: 0 of 1 postcondition(s) met\n  [owed] check (no seat)\n")

	v = verifyClaim(acceptRow(), owed("slow"), job.CapturedRun{}, nil)
	assert.False(t, v.Verified)
	assert.Equal(t, []string{"postcondition \"check\": files its run as owed because \"slow\", and the one reason a run is owed is \"no seat\""}, v.Violations)

	integration := job.Job{ID: "integrate", Integrates: []string{"a"}, Check: acceptRow().Check}
	rep := owed(OwedNoSeat)
	rep.Job = "integrate"
	v = VerifyGates(integration, rep, job.CapturedRun{}, nil, nil, Observed{})
	assert.False(t, v.Verified)
	assert.Contains(t, strings.Join(v.Violations, "\n"), "postcondition \"check\": an integration runs the checks it owns: its check waits for a seat itself")
	assert.Contains(t, strings.Join(v.Violations, "\n"), "raises budget_wait or frees a holder")
}

func TestAnIntegrationOwingACheckIsToldWhatClearsMemoryToo(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "integrate", Integrates: []string{"a"}, Check: acceptRow().Check}
	rep := passingResult()
	rep.Job = "integrate"
	rep.Validation = job.ResultValidation{Command: acceptRow().Validation, Owed: OwedNoSeat}
	v := VerifyGates(row, rep, job.CapturedRun{}, nil, nil, Observed{})
	assert.Contains(t, strings.Join(v.Violations, "\n"), "after MGS3053, which no seat clears, frees memory and runs the check again")
}

// A worker that found no build seat files its checks as owed, and the integration that
// merges its branch runs each one its own full go-test and lint do not already cover.
func TestOwedPostconditionsRunEveryCheckAMergedWorkerOwes(t *testing.T) {
	t.Parallel()

	scoped := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"./pkg", "-run", "Alpha"}}
	worker := job.Job{ID: "a", Check: fixtureCheck(scoped), Postconditions: []job.Postcondition{
		{ID: "lint", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "lint", Project: "."})},
		{ID: "buzz", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "buzz-test", Project: "docs"})},
	}}
	owed := &job.Result{
		Validation:           job.ResultValidation{Command: "magus run go::go-test . -- ./pkg -run Alpha", Owed: OwedNoSeat},
		VerificationEvidence: []job.VerificationEvidence{{GateID: "lint", Owed: OwedNoSeat}, {GateID: "buzz", Owed: OwedNoSeat}},
	}
	paid := job.Job{ID: "b", Check: fixtureCheck(types.LeaseCheck{Target: "buzz-test", Project: "console"})}
	elsewhere := worker
	elsewhere.ID = "c"
	tries := job.NewAttempts([]job.Attempt{
		filedTry("a", owed),
		filedTry("b", &job.Result{Validation: job.ResultValidation{OutputRef: "out1"}}),
		filedTry("c", owed),
	})
	row := job.Job{ID: "integrate", Integrates: []string{"a", "b"}}
	held, err := integratorPostconditions(row)
	require.NoError(t, err)

	got, err := owedPostconditions(row, []job.Job{worker, paid, elsewhere}, tries, held)
	require.NoError(t, err)
	want := []job.Postcondition{
		{ID: "buzz-test-docs", Kind: job.PostconditionKindCheck, Expect: job.ExpectPassed, Check: fixtureCheck(types.LeaseCheck{Target: "buzz-test", Project: "docs"}),
			Accept: []job.Basis{job.BasisWitnessed}},
	}
	assert.Equal(t, want, got, "the scoped go-test and lint ride the full runs; a check paid for and a branch not merged add nothing")
	none, err := owedPostconditions(job.Job{ID: "w"}, []job.Job{worker}, tries, nil)
	require.NoError(t, err)
	assert.Empty(t, none, "a job that integrates nothing owes nothing")
}

// narrowedRunSuite pins a file-scoped postcondition grading what the row changed.
type narrowedRunSuite struct {
	suite.Suite
}

func TestNarrowedRun(t *testing.T) { suite.Run(t, new(narrowedRunSuite)) }

func (s *narrowedRunSuite) row(args ...string) job.Job {
	row := acceptRow()
	row.Postconditions = []job.Postcondition{job.Postcondition{ID: "lint", Check: fixtureCheck(types.LeaseCheck{Target: "lint", Project: ".", NoDefaultCharms: true, Args: args})}.Resolve()}
	return row
}

func (s *narrowedRunSuite) grade(row job.Job, seen Observed, args ...string) Status {
	seen.TestSelection = goCheckSelection
	check := job.CapturedRun{Found: true, Target: "go-test", Project: ".", ExtraArgs: []string{"-run", "Ledger"}}
	lint := job.CapturedRun{Found: true, Target: "lint", Project: ".", Ref: "out2", ExtraArgs: args}
	rep := passingResult()
	rep.VerificationEvidence = []job.VerificationEvidence{{GateID: "lint", OutputRef: "out2"}}
	return VerifyGates(row, rep, check, []job.PostconditionAttempt{{PostconditionID: "lint", Attempt: lint}}, nil, seen)
}

func (s *narrowedRunSuite) seen(changed ...string) Observed {
	return Observed{Changed: changed, ChangedKnown: true, changedFiles: changed}
}

func (s *narrowedRunSuite) TestRunOverTheChangedFilesPassesAWiderPostcondition() {
	row := s.row("cmd/magus/ledger.go", "internal/ledger/report.go", "internal/ledger/report_test.go", "internal/ledger/other_test.go")

	v := s.grade(row, s.seen("internal/ledger/report.go", "cmd/magus/ledger.go"), "cmd/magus/ledger.go", "internal/ledger/report.go")

	s.True(v.Verified, "%v", v.Violations)
	s.Contains(strings.Join(v.Notes, "\n"), `postcondition "lint" was graded on output ref "out2", a run over the files this job changed`)
}

func (s *narrowedRunSuite) TestPostconditionWithNoArgsTakesARunOverTheChangedFiles() {
	v := s.grade(s.row(), s.seen("internal/ledger/report.go", "cmd/magus/ledger.go"), "cmd/magus/ledger.go", "internal/ledger/report.go")

	s.True(v.Verified, "%v", v.Violations)
}

func (s *narrowedRunSuite) TestRunMissingAChangedFileIsRefused() {
	v := s.grade(s.row("cmd/magus/ledger.go", "internal/ledger/report.go"), s.seen("internal/ledger/report.go", "cmd/magus/ledger.go"), "cmd/magus/ledger.go")

	s.False(v.Verified)
	s.Contains(strings.Join(v.Violations, "\n"), "leaves out what this job changed: internal/ledger/report.go")
}

func (s *narrowedRunSuite) TestRunOverThePostconditionsArgsMissingAChangedFileIsRefused() {
	v := s.grade(s.row("cmd/magus/ledger.go"), s.seen("internal/ledger/report_test.go", "cmd/magus/ledger.go"), "cmd/magus/ledger.go")

	s.False(v.Verified, "the postcondition's own list is no answer for a file the row went on to change")
	s.Contains(strings.Join(v.Violations, "\n"), "internal/ledger/report_test.go")
}

// A changed file another verified run read is no file the check left out.
func (s *narrowedRunSuite) TestAFileAnotherVerifiedRunReadPassesTheCheck() {
	check := types.LeaseCheck{Target: "format", Project: ".", Args: []string{"spells/x.buzz"}}
	row := s.row("internal/job/landings.go", "spells/x.buzz")
	row.Check, row.Validation = fixtureCheck(check), CheckCommand(check)
	seen := s.seen("spells/x.buzz", "internal/job/landings.go")
	seen.TestSelection = goCheckSelection
	seen.namedFiles = map[string]bool{"spells/x.buzz": true}
	row.Paths.Write = []string{"internal/job/landings.go", "spells/x.buzz"}
	rep := passingResult()
	rep.ChangedPaths = []string{"spells/x.buzz", "internal/job/landings.go"}
	rep.Validation.Command = row.Validation
	rep.VerificationEvidence = []job.VerificationEvidence{{GateID: "lint", OutputRef: "out2"}}
	ran :=job.CapturedRun{Found: true, Target: "format", Project: ".", ExtraArgs: []string{"spells/x.buzz"}}
	lint := job.CapturedRun{Found: true, Target: "lint", Project: ".", Ref: "out2", ExtraArgs: []string{"internal/job/landings.go", "spells/x.buzz"}}

	v := VerifyGates(row, rep, ran, []job.PostconditionAttempt{{PostconditionID: "lint", Attempt: lint}}, nil, seen)

	s.True(v.Verified, "%v", v.Violations)
	s.Contains(strings.Join(v.Notes, "\n"), `without the changed files internal/job/landings.go, which postcondition(s) lint read`)

	lint.ExtraArgs = []string{"spells/x.buzz"}
	v = VerifyGates(row, rep, ran, []job.PostconditionAttempt{{PostconditionID: "lint", Attempt: lint}}, nil, seen)
	s.False(v.Verified, "a changed file no run read still fails")
}

func (s *narrowedRunSuite) TestUnknownChangeIsRefused() {
	v := s.grade(s.row("cmd/magus/ledger.go", "internal/ledger/report.go"), Observed{ChangedKnown: true}, "cmd/magus/ledger.go")

	s.False(v.Verified, "a run narrowed against nothing anyone read cannot pass")
}

func (s *narrowedRunSuite) TestWholeRunStillPasses() {
	v := s.grade(s.row("cmd/magus/ledger.go"), s.seen("internal/ledger/report.go", "cmd/magus/ledger.go"))

	s.True(v.Verified, "%v", v.Violations)
}

func (s *narrowedRunSuite) TestStaleRunIsStillRefused() {
	seen := s.seen("internal/ledger/report.go", "cmd/magus/ledger.go")
	seen.Stale = map[string]string{"out2": "the changed paths read now as the run read them"}

	v := s.grade(s.row(), seen, "cmd/magus/ledger.go", "internal/ledger/report.go")

	s.False(v.Verified)
	s.Contains(strings.Join(v.Violations, "\n"), "predates the job's last write")
}

func (s *narrowedRunSuite) TestNestedProjectReadsItsOwnFiles() {
	c := types.LeaseCheck{Target: "lint", Project: "libs/x", Args: []string{"a.go", "b.go"}}
	run := job.CapturedRun{Found: true, Target: "lint", Project: "libs/x", ExtraArgs: []string{"a.go"}}

	s.True(ranOver(c, run, nil, []string{"libs/x/a.go", "cli/other.go"}, nil, goCheckSelection), "a file outside the project is not this postcondition's")
	s.False(ranOver(c, run, nil, []string{"libs/x/b.go"}, nil, goCheckSelection))
}

func (s *narrowedRunSuite) TestCheckOverFilesGetsTheSameAnswer() {
	c := types.LeaseCheck{Target: "format", Project: ".", Args: []string{"a.go", "b.go"}}
	run := job.CapturedRun{Found: true, Target: "format", Project: ".", ExtraArgs: []string{"a.go"}}

	s.True(ranOver(c, run, nil, []string{"a.go"}, s.named("a.go", "b.go")(c), goCheckSelection))
}

// named writes files into a checkout and answers [namedFiles] over a check.
func (s *narrowedRunSuite) named(files ...string) func(types.LeaseCheck, ...string) map[string]bool {
	tree := s.T().TempDir()
	for _, f := range files {
		s.Require().NoError(os.WriteFile(filepath.Join(tree, f), nil, 0o644))
	}
	return func(c types.LeaseCheck, changed ...string) map[string]bool {
		return namedFiles(tree, []job.Postcondition{{Kind: job.PostconditionKindCheck, Check: fixtureCheck(c)}}, changed)
	}
}

func (s *narrowedRunSuite) TestAnArgumentNamingNoFileIsComparedVerbatim() {
	c := types.LeaseCheck{Target: "deploy-check", Project: ".", Args: []string{"staging"}}
	run := job.CapturedRun{Found: true, Target: "deploy-check", Project: ".", ExtraArgs: []string{"a.go"}}

	s.False(ranOver(c, run, nil, []string{"a.go"}, s.named("a.go")(c, "a.go"), goCheckSelection))
}

func (s *narrowedRunSuite) TestAFileTheRowDeletedStillNamesAFile() {
	c := types.LeaseCheck{Target: "format", Project: ".", Args: []string{"a.go", "gone.go"}}
	run := job.CapturedRun{Found: true, Target: "format", Project: ".", ExtraArgs: []string{"a.go"}}

	s.True(ranOver(c, run, nil, []string{"a.go"}, s.named("a.go")(c, "gone.go"), goCheckSelection))
}

func (s *narrowedRunSuite) TestADirectoryIsNotAFile() {
	c := types.LeaseCheck{Target: "format", Project: ".", Args: []string{"pkg"}}
	run := job.CapturedRun{Found: true, Target: "format", Project: ".", ExtraArgs: []string{"pkg/a.go"}}

	s.False(ranOver(c, run, nil, []string{"pkg/a.go"}, s.named()(c), goCheckSelection))
}

func (s *narrowedRunSuite) TestAnArgumentResolvesAgainstItsProject() {
	c := types.LeaseCheck{Target: "format", Project: "libs/x", Args: []string{"a.go"}}

	s.Equal(map[string]bool{"libs/x/a.go": true}, s.named()(c, "libs/x/a.go"))
}

func (s *narrowedRunSuite) TestNoArgCheckThatIsNotLintStaysWhole() {
	vet := types.LeaseCheck{Target: "go-vet", Project: "."}
	narrowed := job.CapturedRun{Found: true, Target: "go-vet", Project: ".", ExtraArgs: []string{"a.go"}}

	s.False(ranOver(vet, narrowed, nil, []string{"a.go"}, nil, goCheckSelection))
}

func (s *narrowedRunSuite) TestRunWithAFlagIsAnotherRun() {
	c := types.LeaseCheck{Target: "lint", Project: ".", Args: []string{"a.go"}}
	run := job.CapturedRun{Found: true, Target: "lint", Project: ".", ExtraArgs: []string{"--fix", "a.go"}}

	s.False(ranOver(c, run, nil, []string{"a.go"}, nil, goCheckSelection))
}

func (s *narrowedRunSuite) TestScriptArgsStayItsArgv() {
	c := types.LeaseCheck{Script: "probes/key.buzz", Args: []string{"a.go", "b.go"}}
	run := job.CapturedRun{Found: true, Target: "buzz probes/key.buzz", Project: ".", ExtraArgs: []string{"a.go"}}

	s.False(ranOver(c, run, nil, []string{"a.go"}, nil, goCheckSelection))
}

func (s *narrowedRunSuite) TestPackageArgumentDoesNotScopeGoTest() {
	c := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"./pkg", "./other"}}
	run := job.CapturedRun{Found: true, Target: "go-test", Project: ".", ExtraArgs: []string{"./pkg"}}

	s.False(ranOver(c, run, nil, []string{"pkg/a.go"}, nil, goCheckSelection))
}

func (s *narrowedRunSuite) TestRowFilesPreferWhatLanded() {
	tree := s.T().TempDir()
	for _, f := range []string{"own.go", "claimed.go", "gen.go", "merged.go"} {
		s.Require().NoError(os.WriteFile(filepath.Join(tree, f), []byte("x\n"), 0o644))
	}
	seen := Observed{Changed: []string{"own.go", "merged.go"}, ChangedKnown: true, Landed: []string{"own.go", "gen.go", "deleted.go"}, LandedKnown: true}
	result := job.Result{ChangedPaths: []string{"claimed.go"}}
	generated := func(p string) bool { return p == "gen.go" }

	s.Equal([]string{"claimed.go", "own.go"}, rowFiles(tree, seen, result, generated), "a merged line's file is not the row's")

	seen.LandedKnown = false
	s.Equal([]string{"claimed.go", "merged.go", "own.go"}, rowFiles(tree, seen, result, generated), "the diff answers when nothing read the branch")
}
