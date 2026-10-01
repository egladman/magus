package job

import (
	"reflect"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func acceptRow() types.Job {
	check := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}}
	return types.Job{
		ID:         "harness/ledger-accept",
		WritePaths: []string{"internal/ledger", "cmd/magus/ledger.go", "docs/reference/*.md"},
		Check:      &check,
		Validation: check.String(),
		State:      types.StateRunning,
	}
}

func passingResult() types.JobResult {
	return types.JobResult{
		Schema:       types.Schema{Version: types.JobResultSchemaVersion},
		Job:          "harness/ledger-accept",
		ChangedPaths: []string{"internal/ledger/report.go", "cmd/magus/ledger.go"},
		Validation: types.JobResultValidation{
			Command:   "magus run go::go-test . -- -run Ledger",
			OutputRef: "a1b2c3d4",
		},
		UnresolvedRisks: []string{},
	}
}

// passingRun is what the output store recorded for a passing run of acceptRow's own
// validation, which is what an honest result cites. The zero Attempt is a ref the store
// does not hold.
var passingRun = types.JobAttempt{Found: true, Project: ".", Target: "go-test", Spell: "go"}

// verifyClaim grades a result against a diff that shows exactly what it claims, so a case
// about another rule is not failed by the diff check.
func verifyClaim(row types.Job, rep types.JobResult, att types.JobAttempt, declared []types.Job) Status {
	return VerifyGates(row, rep, att, nil, declared, claimed(rep))
}

func claimed(rep types.JobResult) Observed {
	return Observed{Changed: rep.ChangedPaths, ChangedKnown: true}
}

func completionGateRow() types.Job {
	return types.Job{
		ID:         "harness/gated",
		WritePaths: []string{"internal/job"},
		State:      types.StateRunning,
		Goals: []types.CompletionGate{
			{ID: "unit", Description: "job package tests pass", Check: types.LeaseCheck{Target: "go::go-test", Project: "."}},
			{ID: "docs", Description: "docs checks pass", Check: types.LeaseCheck{Target: "test", Project: "docs"}, DependsOn: []string{"unit"}},
		},
	}
}

func completionGateResult() types.JobResult {
	return types.JobResult{
		Schema:       types.Schema{Version: types.JobResultSchemaVersion},
		Job:          "harness/gated",
		ChangedPaths: []string{"internal/job/verify.go"},
		GateEvidence: []types.GateEvidence{
			{GateID: "unit", OutputRef: "unit-ref"},
			{GateID: "docs", OutputRef: "docs-ref"},
		},
		UnresolvedRisks: []string{},
	}
}

func completionGateAttempts() []types.JobGateAttempt {
	return []types.JobGateAttempt{
		{GateID: "unit", Attempt: types.JobAttempt{Found: true, Ref: "unit-ref", Project: ".", Target: "go-test", Spell: "go"}},
		{GateID: "docs", Attempt: types.JobAttempt{Found: true, Ref: "docs-ref", Project: "docs", Target: "test"}},
	}
}

func TestVerifyTakesAResultInsideTheBoundary(t *testing.T) {
	t.Parallel()

	assert.Equal(t, Status{
		Job:      "harness/ledger-accept",
		Verified: true,
		Risks:    []string{},
		Command:  "magus run go::go-test . -- -run Ledger",
		// The primary check reports as the gate it is. See the note in lifecycle_test.go.
		Gates: []types.GateStatus{{ID: types.PrimaryCompletionGateID, OutputRef: passingResult().Validation.OutputRef, Verified: true}},
	}, verifyClaim(acceptRow(), passingResult(), passingRun, nil))
}

func TestVerifyGoalsRequireEvidenceForEveryDeclaredGate(t *testing.T) {
	t.Parallel()

	status := VerifyGates(completionGateRow(), completionGateResult(), types.JobAttempt{}, completionGateAttempts(), nil, claimed(completionGateResult()))
	assert.True(t, status.Verified, status.Violations)
	require.Len(t, status.Gates, 2)
	assert.True(t, status.Gates[0].Verified)
	assert.True(t, status.Gates[1].Verified)
}

func TestVerifyGoalsRejectMissingOrWrongEvidence(t *testing.T) {
	t.Parallel()

	rep := completionGateResult()
	rep.GateEvidence = rep.GateEvidence[:1]
	status := VerifyGates(completionGateRow(), rep, types.JobAttempt{}, completionGateAttempts()[:1], nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), `goal "docs"`)

	rep = completionGateResult()
	attempts := completionGateAttempts()
	attempts[1].Attempt.Project = "."
	status = VerifyGates(completionGateRow(), rep, types.JobAttempt{}, attempts, nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "different run")
}

func TestVerifyGoalsRejectUndeclaredEvidence(t *testing.T) {
	t.Parallel()

	rep := completionGateResult()
	rep.GateEvidence = append(rep.GateEvidence, types.GateEvidence{GateID: "invented", OutputRef: "invented-ref"})
	attempts := append(completionGateAttempts(), types.JobGateAttempt{
		GateID:  "invented",
		Attempt: types.JobAttempt{Found: true, Ref: "invented-ref", Project: ".", Target: "go-test"},
	})
	status := VerifyGates(completionGateRow(), rep, types.JobAttempt{}, attempts, nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "undeclared goal \"invented\"")
}

// TestVerifyCIGatePassesOnAGreenGateWithATrivialDelta: a check gate on ci passes on a
// green ci gate when the change since it tiers trivial, with no output ref of its own and
// even though that gate ran before the job was declared.
func TestVerifyCIGatePassesOnAGreenGateWithATrivialDelta(t *testing.T) {
	t.Parallel()

	ciRow := func(target, project string) types.Job {
		row := acceptRow()
		row.Created = 100
		row.Check = &types.LeaseCheck{Target: target, Project: project}
		return row
	}
	rep := passingResult()
	rep.Validation.OutputRef = ""
	green := GreenGate{Commit: "c1", Projects: []string{".", "docs"}, Tier: types.RiskTrivial}

	seen := claimed(rep)
	seen.GreenGate = green
	status := VerifyGates(ciRow("ci", "."), rep, types.JobAttempt{}, nil, nil, seen)
	assert.True(t, status.Verified, status.Violations)
	assert.Equal(t, []types.GateStatus{{ID: types.PrimaryCompletionGateID, Verified: true}}, status.Gates)

	for name, tc := range map[string]struct {
		row  types.Job
		gate GreenGate
	}{
		"no green gate":               {ciRow("ci", "."), GreenGate{}},
		"a mechanical delta":          {ciRow("ci", "."), GreenGate{Commit: "c1", Projects: []string{"."}, Tier: types.RiskMechanical}},
		"a scoped delta":              {ciRow("ci", "."), GreenGate{Commit: "c1", Projects: []string{"."}, Tier: types.RiskScoped}},
		"a project the gate skipped":  {ciRow("ci", "site"), green},
		"a check that is not ci":      {ciRow("go::go-test", "."), green},
		"a spell-qualified ci target": {ciRow("go::ci", "."), green},
	} {
		t.Run(name, func(t *testing.T) {
			seen := claimed(rep)
			seen.GreenGate = tc.gate
			status := VerifyGates(tc.row, rep, types.JobAttempt{}, nil, nil, seen)
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

func TestVerifyCompletionGateDependenciesPropagate(t *testing.T) {
	t.Parallel()

	row := completionGateRow()
	row.Goals = append(row.Goals, types.CompletionGate{
		ID: "publish", Check: types.LeaseCheck{Target: "test", Project: "docs"}, DependsOn: []string{"docs"},
	})
	rep := completionGateResult()
	rep.GateEvidence = append(rep.GateEvidence, types.GateEvidence{GateID: "publish", OutputRef: "publish-ref"})
	attempts := append(completionGateAttempts(), types.JobGateAttempt{
		GateID: "publish", Attempt: types.JobAttempt{Found: true, Ref: "publish-ref", Project: "docs", Target: "test"},
	})
	attempts[0].Attempt.Failed = true
	status := VerifyGates(row, rep, types.JobAttempt{}, attempts, nil, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, status.Gates[1].Violations, `depends_on goal "unit" has not verified`)
	assert.Contains(t, status.Gates[2].Violations, `depends_on goal "docs" has not verified`)
}

func TestVerifyGoalsEnforceJobDependencies(t *testing.T) {
	t.Parallel()

	row := completionGateRow()
	row.DependsOn = []string{"upstream"}
	status := VerifyGates(row, completionGateResult(), types.JobAttempt{}, completionGateAttempts(), []types.Job{{ID: "upstream", State: types.StateExited}}, Observed{})
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

	v := verifyClaim(acceptRow(), rep, types.JobAttempt{}, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 3)
	assert.Contains(t, strings.Join(v.Violations, "\n"), "internal/sessions/store.go")
}

// The result the coding-agent persona filed on 2026-09-11 and had ACCEPTED: nothing
// changed, a ref from an unrelated codegen run, a self-asserted pass, and two fields
// nobody asked for. Every one of them is now a named rejection, which is the whole of
// what "grade evidence, not assertions" means.
func TestVerifyRefusesTheFabricatedResult(t *testing.T) {
	t.Parallel()

	raw := `{"schema_version":2,"job":"harness/ledger-accept","changed_paths":[],` +
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
	v := verifyClaim(acceptRow(), rep, types.JobAttempt{Found: true, Project: ".", Target: "generate"}, nil)
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

// A row with no check has nothing to bind evidence to, and accepting it anyway is how a
// ref from any run at all passes for evidence.
func TestVerifyRefusesARowWithNoCheckToBindTo(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Check, row.Validation = nil, ""

	v := verifyClaim(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "declares no check and no goal")
}

// The binary's spelling and the args after `--` are ways of running one target, so
// neither may decide whether the evidence binds.
func TestCheckBindsOnIdentityNotSpelling(t *testing.T) {
	t.Parallel()

	att := types.JobAttempt{Found: true, Project: "internal/ledger", Target: "test"}
	for _, line := range []string{
		"magus run test internal/ledger",
		"./magus run test internal/ledger",
		"magus run test internal/ledger -- -run Ledger",
	} {
		c, err := types.ParseLeaseRunLine(line)
		require.NoError(t, err, line)
		assert.True(t, bindsTo(c, att, nil), line)
	}

	c, err := types.ParseLeaseRunLine("magus run test cmd/magus")
	require.NoError(t, err)
	assert.False(t, bindsTo(c, att, nil), "another project is another run")
}

// A CHARM is part of a run's identity, not a spelling of it. The store records what was
// invoked, so the charmless `generate` that GATES drift and the `generate:rw` that WRITES
// it are two runs; accepting either for the other made a drift gate satisfiable by the
// run that produces the drift.
func TestCheckBindsOnCharm(t *testing.T) {
	t.Parallel()

	written := types.JobAttempt{Found: true, Project: ".", Target: "generate:rw"}
	gated := types.JobAttempt{Found: true, Project: ".", Target: "generate"}

	charmless, err := types.ParseLeaseRunLine("magus run generate .")
	require.NoError(t, err)
	assert.False(t, bindsTo(charmless, written, nil), "a written run is not evidence of a gated one")
	assert.True(t, bindsTo(charmless, gated, nil))

	rw, err := types.ParseLeaseRunLine("magus run generate:rw .")
	require.NoError(t, err)
	assert.True(t, bindsTo(rw, written, nil))
	assert.False(t, bindsTo(rw, gated, nil), "a gated run is not evidence of a written one")
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
		att   types.JobAttempt
		want  bool
	}{
		{"the default charm applied", "magus run test .", types.JobAttempt{Project: ".", Target: "test:rw"}, true},
		{"an explicit rw", "magus run test:rw .", types.JobAttempt{Project: ".", Target: "test:rw"}, true},
		{"its own charm stacked on the default", "magus run test:verify .", types.JobAttempt{Project: ".", Target: "test:verify,rw"}, true},
		{"narrowing args, which the store does not record", "magus run test . -- -run X", types.JobAttempt{Project: ".", Target: "test:rw"}, true},
		{"a spell filter", "magus run go::go-test .", types.JobAttempt{Project: ".", Target: "go-test:rw", Spell: "go"}, true},
		{"ci, which never runs with rw", "magus run ci .", types.JobAttempt{Project: ".", Target: "ci"}, true},
		{"a different target", "magus run test .", types.JobAttempt{Project: ".", Target: "lint:rw"}, false},
		{"a different project", "magus run test docs", types.JobAttempt{Project: ".", Target: "test:rw"}, false},
		{"a run without the defaults", "magus run test .", types.JobAttempt{Project: ".", Target: "test"}, false},
		{"an extra charm", "magus run test .", types.JobAttempt{Project: ".", Target: "test:rw,verify"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := types.ParseLeaseRunLine(tc.check)
			require.NoError(t, err)
			tc.att.Found = true
			assert.Equal(t, tc.want, bindsTo(c, tc.att, defaults))
		})
	}
}

// A check opting out of default_charms is the charmless drift gate an rw-default workspace
// still needs: only a --no-default-charms run satisfies it, and the command served for it
// carries the flag.
func TestCheckOptsOutOfDefaultCharms(t *testing.T) {
	t.Parallel()

	defaults := []string{types.CharmReadWrite}
	gated := types.JobAttempt{Found: true, Project: ".", Target: "generate"}
	written := types.JobAttempt{Found: true, Project: ".", Target: "generate:rw"}

	optOut := types.LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true}
	assert.True(t, bindsTo(optOut, gated, defaults), "the --no-default-charms run")
	assert.False(t, bindsTo(optOut, written, defaults), "a written run is not evidence of a gated one")

	plain := types.LeaseCheck{Target: "generate", Project: "."}
	assert.True(t, bindsTo(plain, written, defaults), "a plain check still binds the default-charm run")
	assert.False(t, bindsTo(plain, gated, defaults))

	assert.Equal(t, "magus run generate . --no-default-charms", optOut.String())
	assert.True(t, strings.HasSuffix(gateCommand(optOut), " run generate . --no-default-charms"), gateCommand(optOut))
	assert.True(t, strings.HasSuffix(gateCommand(plain), " run generate ."), gateCommand(plain))
}

// The opt-out is a field on the check record and has one spelling: a check line carrying
// the flag is refused with the field named, and a declared record keeps it.
func TestCheckOptOutIsARecordField(t *testing.T) {
	t.Parallel()

	_, err := types.ParseLeaseCheck("generate . --no-default-charms")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_default_charms: true")

	decl, err := DecodeDeclaration(declaration(`"id":"adj/drift"`,
		`"check":{"target":"generate","project":".","no_default_charms":true}`))
	require.NoError(t, err)
	var row types.Job
	decl.Apply(&row)
	require.NotNil(t, row.Check)
	assert.True(t, row.Check.NoDefaultCharms)
	assert.Equal(t, "magus run generate . --no-default-charms", row.Validation)
}

// A rejected run names the check as it resolved, so the reader sees which charm set the
// evidence had to carry.
func TestCheckGateUnderDefaultCharms(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "fix/job-store"}
	gate := types.CompletionGate{ID: "check", Kind: types.GateKindCheck, Expect: types.ExpectPassed, Check: types.LeaseCheck{Target: "test", Project: "."}}
	seen := Observed{DefaultCharms: []string{types.CharmReadWrite}}

	status := verifyGate(row, gate, "out57a24bec47f2", types.JobAttempt{Found: true, Project: ".", Target: "test:rw"}, seen)
	assert.True(t, status.Verified, status.Violations)

	status = verifyGate(row, gate, "out57a24bec47f2", types.JobAttempt{Found: true, Project: ".", Target: "lint:rw"}, seen)
	assert.False(t, status.Verified)
	require.Len(t, status.Violations, 1)
	assert.Contains(t, status.Violations[0], "this goal's check runs `magus run test:rw .`")
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
	row.WritePaths = []string{"."}
	assert.True(t, verifyClaim(row, passingResult(), passingRun, nil).Verified)

	row.WritePaths = []string{""}
	assert.False(t, verifyClaim(row, passingResult(), passingRun, nil).Verified, "a blank declaration claims nothing")
}

// The deny list is read at acceptance too: a path can be inside the owned write paths and still
// be one the row was told to leave alone.
func TestVerifyRejectsADeniedPath(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.DenyPaths = []string{"internal/ledger/store.go"}
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
	declared := []types.Job{{ID: "harness/ledger-accept/child"}}

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
	row.State = types.StatePass

	v := verifyClaim(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "already pass")
}

// The ref is the whole of the evidence, so a ref that no longer resolves is a rejection
// rather than a detail: the root cannot reopen a run that is not there.
func TestVerifyRejectsEvidenceTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()

	v := verifyClaim(acceptRow(), passingResult(), types.JobAttempt{}, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "a1b2c3d4")
}

// A read-only lease has no write set, so any claimed write is a violation of a boundary
// the row declared by being read-only rather than by listing paths.
func TestVerifyRefusesWritesFromAReadOnlyLease(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.ReadOnly, row.WritePaths = true, nil

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
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(ResultSchema), &schema))
	require.NoError(t, json.Unmarshal(schema.Properties["validation"], &validation))

	assert.ElementsMatch(t, jsonFields(types.JobResult{}), keys(schema.Properties))
	assert.ElementsMatch(t, jsonFields(types.JobResultValidation{}), keys(validation.Properties))
}

// jsonFields is the wire name of every field a struct serializes, which is the set the
// schema has to describe.
func jsonFields(v any) []string {
	var out []string
	for _, f := range reflect.VisibleFields(reflect.TypeOf(v)) {
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous || opts == "unknown" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestVerifyGatesGradesTheFootprintAgainstDeclarationClaims(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "unit", Created: 1, WritePaths: []string{"run.go#executeStages"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"run.go"}}
	placed := func(decl string) types.RegionChange {
		return types.RegionChange{File: types.FileChange{Path: "run.go"}, Side: types.RegionNew, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
	}
	seen := func(regions ...types.RegionChange) Observed {
		return Observed{Changed: []string{"run.go"}, ChangedKnown: true, ChangedFrom: "abc1234", Regions: regions, RegionsKnown: true}
	}
	const outside = "the diff since abc1234 changed run.go#func RunCI() {, outside every declaration the job claims in run.go (executeStages)"

	inside := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, seen(placed("func (m *Magus) executeStages() {"), placed("")))
	assert.NotContains(t, inside.Violations, outside)
	assert.Empty(t, inside.FootprintUnclaimed, "its own declaration and the preamble")

	wandered := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, seen(placed("func RunCI() {")))
	assert.Contains(t, wandered.Violations, outside)
	assert.Equal(t, []string{"run.go#func RunCI() {"}, wandered.FootprintUnclaimed)

	unread := Observed{Changed: []string{"run.go"}, ChangedKnown: true, RegionsReason: "git does not report changed regions (RegionReporter)"}
	blind := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, unread)
	assert.Contains(t, blind.Violations, "job unit claims declarations and its footprint is not known (git does not report changed regions (RegionReporter)), so no claim could be checked")

	whole := row
	whole.WritePaths = []string{"run.go"}
	assert.NotContains(t, VerifyGates(whole, rep, types.JobAttempt{}, nil, []types.Job{whole}, unread).Violations,
		"job unit claims declarations and its footprint is not known (git does not report changed regions (RegionReporter)), so no claim could be checked",
		"a job claiming no declaration is not graded on its footprint")
}

func TestUnclaimedFootprint(t *testing.T) {
	t.Parallel()

	placed := func(path, decl string) types.RegionChange {
		return types.RegionChange{File: types.FileChange{Path: path}, Side: types.RegionNew, Lines: [2]int{1, 2}, Declaration: decl, Driver: "golang"}
	}
	unplaced := types.RegionChange{File: types.FileChange{Path: "run.go"}, Side: types.RegionNew, Lines: [2]int{4, 4}}
	for _, tc := range []struct {
		name       string
		writePaths []string
		regions    []types.RegionChange
		want       []string
	}{
		{
			name:       "inside the claimed declaration",
			writePaths: []string{"run.go#executeStages"},
			regions:    []types.RegionChange{placed("run.go", "func (m *Magus) executeStages(ctx context.Context) error {")},
		},
		{
			name:       "a neighbour is outside, once however many hunks land in it",
			writePaths: []string{"run.go#executeStages"},
			regions:    []types.RegionChange{placed("run.go", "func RunCI() {"), placed("run.go", "func RunCI() {")},
			want:       []string{"run.go#func RunCI() {"},
		},
		{
			name:       "the preamble is exempt",
			writePaths: []string{"run.go#executeStages"},
			regions:    []types.RegionChange{placed("run.go", "")},
		},
		{
			name:       "a line no driver placed is the whole file",
			writePaths: []string{"run.go#executeStages"},
			regions:    []types.RegionChange{unplaced},
			want:       []string{"run.go"},
		},
		{
			name:       "a file also claimed whole is not graded by declaration",
			writePaths: []string{"run.go#executeStages", "."},
			regions:    []types.RegionChange{placed("run.go", "func RunCI() {")},
		},
		{
			name:       "a file claimed by no entry is the path rules' to report",
			writePaths: []string{"run.go#executeStages"},
			regions:    []types.RegionChange{placed("order.go", "func Order() {")},
		},
		{
			name:       "any of several claims on the file names it",
			writePaths: []string{"run.go#executeStages", "run.go#RunCI"},
			regions:    []types.RegionChange{placed("run.go", "func RunCI() {")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, unclaimedFootprint(tc.writePaths, tc.regions))
		})
	}
}
