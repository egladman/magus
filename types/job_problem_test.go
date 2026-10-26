package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

func TestJobProblemErrCarriesItsCodeAndFix(t *testing.T) {
	p := JobProblem{Code: WritePathIsDirectory, Field: "write_paths[0]", Message: `"internal/job" is a directory`, Fix: "list the files"}

	err := p.Error()

	require.ErrorIs(t, err, WritePathIsDirectory)
	assert.Equal(t, `"internal/job" is a directory: list the files`, err.Msg)
}

func TestJobProblemErrWithoutAFixIsTheMessage(t *testing.T) {
	assert.Equal(t, "bad", JobProblem{Code: JobRecordInvalid, Message: "bad"}.Error().Msg)
}

func TestJobProblemsErrIsNilWhenEmpty(t *testing.T) {
	require.NoError(t, JobProblems(nil).Error())
	require.NoError(t, JobValidation{}.Error())
}

func TestJobProblemsErrJoinsEveryProblemInRecordOrder(t *testing.T) {
	ps := JobProblems{
		{Code: JobRecordInvalid, Field: "id", Message: "first"},
		{Code: JobGoalInvalid, Field: "goals[0].id", Message: "second"},
	}

	err := JobValidation{Problems: ps}.Error()

	require.ErrorIs(t, err, JobRecordInvalid)
	require.ErrorIs(t, err, JobGoalInvalid)
	var joined interface{ Unwrap() []error }
	require.True(t, errors.As(err, &joined))
	got := joined.Unwrap()
	require.Len(t, got, 2)
	assert.Contains(t, got[0].Error(), "first")
	assert.Contains(t, got[1].Error(), "second")
}

func TestPermissibleIsOnlyTheThreeBreadthCodes(t *testing.T) {
	for _, c := range AllDiagnosticCodes() {
		want := c == WritePathTooBroad || c == OutputOutsideWritePaths || c == GateNotRun
		assert.Equalf(t, want, Permissible(c), "Permissible(%s)", c)
	}
}

func TestPermitMatchesOnCodeAndSubject(t *testing.T) {
	broad := JobProblem{Code: WritePathTooBroad, Subject: "**/*.go"}
	output := JobProblem{Code: OutputOutsideWritePaths, Subject: "manpage/magus.1"}
	gate := JobProblem{Code: GateNotRun, Subject: "lint ."}
	ps := JobProblems{broad, output, gate}

	kept, permitted := ps.Permit([]Deviation{
		{Code: OutputOutsideWritePaths, Subject: "manpage/magus.1", Reason: "the root regenerates it"},
		{Code: GateNotRun, Subject: "test .", Reason: "a different gate"},
		{Code: WritePathTooBroad, Subject: "manpage/magus.1", Reason: "a different code's subject"},
	})

	assert.Equal(t, JobProblems{broad, gate}, kept)
	assert.Equal(t, JobProblems{output}, permitted)
}

func TestPermitNeverPermitsAnUnpermissibleCode(t *testing.T) {
	ps := JobProblems{{Code: JobRecordInvalid, Field: "id", Subject: "x"}}

	kept, permitted := ps.Permit([]Deviation{{Code: JobRecordInvalid, Subject: "x", Reason: "no"}})

	assert.Equal(t, ps, kept)
	assert.Empty(t, permitted)
}

func TestJobProblemsErrKeepsEachProblem(t *testing.T) {
	edit := &JobEdit{AddWritePath: "docs/x.md"}
	err := JobProblems{{Code: OutputOutsideWritePaths, Message: "x", Fix: "add it", Edit: edit}}.Error()

	var carried *JobProblemError
	require.ErrorAs(t, err, &carried)
	assert.Same(t, edit, carried.Problem.Edit)
	var diag *DiagnosticError
	require.ErrorAs(t, err, &diag)
	assert.Equal(t, "x: add it", diag.Msg)
}

func TestJobEditApplyChangesTheRecordOnce(t *testing.T) {
	goal := Goal{ID: "lint", Kind: GoalKindCheck, Check: LeaseCheck{Target: "lint", Project: "."}}
	deviation := Deviation{Code: OutputOutsideWritePaths, Subject: "gen/*.go !gen/*_test.go", Reason: "the integrator regenerates it"}
	edits := []JobEdit{{AddWritePath: "docs/x.md"}, {AddGoal: &goal}, {AddDeviation: &deviation}}
	var d Declaration

	for _, e := range edits {
		assert.True(t, e.Apply(&d), "%s changes the record", e)
		assert.False(t, e.Apply(&d), "%s a second time changes nothing", e)
	}
	assert.Equal(t, Declaration{WritePaths: []string{"docs/x.md"}, Goals: []Goal{goal}, Deviations: []Deviation{deviation}}, d)
	assert.False(t, JobEdit{AddGoal: &Goal{ID: "lint"}}.Apply(&d), "a goal id the record uses is left alone")
	assert.False(t, JobEdit{}.Apply(&d))
}

func TestDoubleStarPermitsEveryRegeneratedOutputSubject(t *testing.T) {
	w := Deviation{Code: OutputOutsideWritePaths, Subject: "**", Reason: "r"}
	for _, subject := range []string{
		"**/gen/*test/*.go",
		"internal/interp/bindings/gen/*.go !internal/interp/bindings/gen/*_test.go !internal/interp/bindings/gen/boundary_*.go",
		"docs/gen/**",
		"benchmarks/agent/reports/**/*.md",
		"MAGUS.md",
		"manpage/*.1",
	} {
		assert.Truef(t, w.Permits(JobProblem{Code: OutputOutsideWritePaths, Subject: subject}), "%q", subject)
	}
}

func TestJobEditRoundTripsThroughJSON(t *testing.T) {
	p := JobProblem{Code: GateNotRun, Message: "m", Fix: "f", Edit: &JobEdit{AddGoal: &Goal{ID: "lint", Kind: GoalKindCheck, Check: LeaseCheck{Target: "lint", Project: "."}}}}

	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var back JobProblem
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, p.Edit, back.Edit)

	raw, err = json.Marshal(JobProblem{Code: GateNotRun})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "edit", "a problem with no edit carries no member")
}
