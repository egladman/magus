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
		SchemaVersion: ResultSchemaVersion,
		Job:           "harness/ledger-accept",
		ChangedPaths:  []string{"internal/ledger/report.go", "cmd/magus/ledger.go"},
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

func completionGateRow() types.Job {
	return types.Job{
		ID:         "harness/gated",
		WritePaths: []string{"internal/job"},
		State:      types.StateRunning,
		CompletionGates: []types.CompletionGate{
			{ID: "unit", Description: "job package tests pass", Check: types.LeaseCheck{Target: "go::go-test", Project: "."}},
			{ID: "docs", Description: "docs checks pass", Check: types.LeaseCheck{Target: "test", Project: "docs"}, DependsOn: []string{"unit"}},
		},
	}
}

func completionGateResult() types.JobResult {
	return types.JobResult{
		SchemaVersion: ResultSchemaVersion,
		Job:           "harness/gated",
		ChangedPaths:  []string{"internal/job/verify.go"},
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
	}, Verify(acceptRow(), passingResult(), passingRun, nil))
}

func TestVerifyCompletionGatesRequireEvidenceForEveryDeclaredGate(t *testing.T) {
	t.Parallel()

	status := VerifyGates(completionGateRow(), completionGateResult(), types.JobAttempt{}, completionGateAttempts(), nil)
	assert.True(t, status.Verified, status.Violations)
	require.Len(t, status.Gates, 2)
	assert.True(t, status.Gates[0].Verified)
	assert.True(t, status.Gates[1].Verified)
}

func TestVerifyCompletionGatesRejectMissingOrWrongEvidence(t *testing.T) {
	t.Parallel()

	rep := completionGateResult()
	rep.GateEvidence = rep.GateEvidence[:1]
	status := VerifyGates(completionGateRow(), rep, types.JobAttempt{}, completionGateAttempts()[:1], nil)
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), `completion gate "docs"`)

	rep = completionGateResult()
	attempts := completionGateAttempts()
	attempts[1].Attempt.Project = "."
	status = VerifyGates(completionGateRow(), rep, types.JobAttempt{}, attempts, nil)
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "different run")
}

func TestVerifyCompletionGatesRejectUndeclaredEvidence(t *testing.T) {
	t.Parallel()

	rep := completionGateResult()
	rep.GateEvidence = append(rep.GateEvidence, types.GateEvidence{GateID: "invented", OutputRef: "invented-ref"})
	attempts := append(completionGateAttempts(), types.JobGateAttempt{
		GateID:  "invented",
		Attempt: types.JobAttempt{Found: true, Ref: "invented-ref", Project: ".", Target: "go-test"},
	})
	status := VerifyGates(completionGateRow(), rep, types.JobAttempt{}, attempts, nil)
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "undeclared completion gate \"invented\"")
}

func TestVerifyRejectsEvidenceCapturedBeforeJobDeclaration(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Created = 100
	attempt := passingRun
	attempt.TimestampMs = 99_999
	status := Verify(row, passingResult(), attempt, nil)
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), "before job declaration")
}

func TestVerifyCompletionGateDependenciesPropagate(t *testing.T) {
	t.Parallel()

	row := completionGateRow()
	row.CompletionGates = append(row.CompletionGates, types.CompletionGate{
		ID: "publish", Check: types.LeaseCheck{Target: "test", Project: "docs"}, DependsOn: []string{"docs"},
	})
	rep := completionGateResult()
	rep.GateEvidence = append(rep.GateEvidence, types.GateEvidence{GateID: "publish", OutputRef: "publish-ref"})
	attempts := append(completionGateAttempts(), types.JobGateAttempt{
		GateID: "publish", Attempt: types.JobAttempt{Found: true, Ref: "publish-ref", Project: "docs", Target: "test"},
	})
	attempts[0].Attempt.Failed = true
	status := VerifyGates(row, rep, types.JobAttempt{}, attempts, nil)
	assert.False(t, status.Verified)
	assert.Contains(t, status.Gates[1].Violations, `depends_on completion gate "unit" has not verified`)
	assert.Contains(t, status.Gates[2].Violations, `depends_on completion gate "docs" has not verified`)
}

func TestVerifyCompletionGatesEnforceJobDependencies(t *testing.T) {
	t.Parallel()

	row := completionGateRow()
	row.DependsOn = []string{"upstream"}
	status := VerifyGates(row, completionGateResult(), types.JobAttempt{}, completionGateAttempts(), []types.Job{{ID: "upstream", State: types.StateExited}})
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

	v := Verify(acceptRow(), rep, types.JobAttempt{}, nil)
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

	// Decoded by hand, as the fields it invented were never read anyway: the two rules
	// that would have caught it even in a result shaped correctly.
	rep := passingResult()
	rep.ChangedPaths = nil
	rep.Validation.OutputRef = "deadbeef"
	v := Verify(acceptRow(), rep, types.JobAttempt{Found: true, Project: ".", Target: "generate"}, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 2)
	assert.Contains(t, v.Violations[0], "no changed paths at all")
	assert.Contains(t, v.Violations[1], "magus run generate .")
	assert.Contains(t, v.Violations[1], "go::go-test")
}

// The stored run's own exit status is the verdict, which is the field a holder no longer
// gets to assert.
func TestVerifyReadsTheStoredRunsOutcome(t *testing.T) {
	t.Parallel()

	failed := passingRun
	failed.Failed = true
	v := Verify(acceptRow(), passingResult(), failed, nil)
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

	v := Verify(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "declares no completion gate")
}

// A charm, the binary's spelling and the args after `--` are all ways of running one
// target, so none of them may decide whether the evidence binds.
func TestCheckBindsOnIdentityNotSpelling(t *testing.T) {
	t.Parallel()

	att := types.JobAttempt{Found: true, Project: "internal/ledger", Target: "test"}
	for _, line := range []string{
		"magus run test internal/ledger",
		"./magus run test:rw internal/ledger",
		"magus run test internal/ledger -- -run Ledger",
	} {
		c, err := types.ParseLeaseRunLine(line)
		require.NoError(t, err, line)
		assert.True(t, bindsTo(c, att), line)
	}

	c, err := types.ParseLeaseRunLine("magus run test cmd/magus")
	require.NoError(t, err)
	assert.False(t, bindsTo(c, att), "another project is another run")
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
			v := Verify(acceptRow(), rep, passingRun, nil)
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
	assert.True(t, Verify(row, passingResult(), passingRun, nil).Verified)

	row.WritePaths = []string{""}
	assert.False(t, Verify(row, passingResult(), passingRun, nil).Verified, "a blank declaration claims nothing")
}

// The deny list is read at acceptance too: a path can be inside the owned lane and still
// be one the row was told to leave alone.
func TestVerifyRejectsADeniedPath(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.DenyPaths = []string{"internal/ledger/store.go"}
	rep := passingResult()
	rep.ChangedPaths = []string{"internal/ledger/store.go"}

	v := Verify(row, rep, passingRun, nil)
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

	v := Verify(acceptRow(), rep, passingRun, declared)
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

	v := Verify(row, passingResult(), passingRun, nil)
	assert.False(t, v.Verified)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "already pass")
}

// The ref is the whole of the evidence, so a ref that no longer resolves is a rejection
// rather than a detail: the root cannot reopen a run that is not there.
func TestVerifyRejectsEvidenceTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()

	v := Verify(acceptRow(), passingResult(), types.JobAttempt{}, nil)
	assert.False(t, v.Verified)
	assert.Contains(t, v.Violations[0], "a1b2c3d4")
}

// A read-only lease has no write set, so any claimed write is a violation of a boundary
// the row declared by being read-only rather than by listing paths.
func TestVerifyRefusesWritesFromAReadOnlyLease(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.ReadOnly, row.WritePaths = true, nil

	v := Verify(row, passingResult(), passingRun, nil)
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
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
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
