package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobProblemErrCarriesItsCodeAndFix(t *testing.T) {
	p := JobProblem{Code: WritePathIsDirectory, Field: "paths.write[0]", Message: `"internal/job" is a directory`, Fix: "list the files"}

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
	err := JobProblems{{Code: OutputOutsideWritePaths, Message: "x", Fix: "add it"}}.Error()

	var diag *DiagnosticError
	require.ErrorAs(t, err, &diag)
	assert.Equal(t, "x: add it", diag.Msg)
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
