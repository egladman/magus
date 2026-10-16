package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobProblemErrCarriesItsCodeAndFix(t *testing.T) {
	p := JobProblem{Code: WritePathIsDirectory, Field: "write_paths[0]", Message: `"internal/job" is a directory`, Fix: "list the files"}

	err := p.Err()

	require.ErrorIs(t, err, WritePathIsDirectory)
	assert.Equal(t, `"internal/job" is a directory: list the files`, err.Msg)
}

func TestJobProblemErrWithoutAFixIsTheMessage(t *testing.T) {
	assert.Equal(t, "bad", JobProblem{Code: JobRecordInvalid, Message: "bad"}.Err().Msg)
}

func TestJobProblemsErrIsNilWhenEmpty(t *testing.T) {
	require.NoError(t, JobProblems(nil).Err())
	require.NoError(t, JobValidation{}.Err())
}

func TestJobProblemsErrJoinsEveryProblemInRecordOrder(t *testing.T) {
	ps := JobProblems{
		{Code: JobRecordInvalid, Field: "id", Message: "first"},
		{Code: JobGoalInvalid, Field: "goals[0].id", Message: "second"},
	}

	err := JobValidation{Problems: ps}.Err()

	require.ErrorIs(t, err, JobRecordInvalid)
	require.ErrorIs(t, err, JobGoalInvalid)
	var joined interface{ Unwrap() []error }
	require.True(t, errors.As(err, &joined))
	got := joined.Unwrap()
	require.Len(t, got, 2)
	assert.Contains(t, got[0].Error(), "first")
	assert.Contains(t, got[1].Error(), "second")
}

func TestWaivableIsOnlyTheThreeBreadthCodes(t *testing.T) {
	for _, c := range AllDiagnosticCodes() {
		want := c == WritePathTooBroad || c == OutputOutsideWritePaths || c == GateNotRun
		assert.Equalf(t, want, Waivable(c), "Waivable(%s)", c)
	}
}

func TestWaiveMatchesOnCodeAndSubject(t *testing.T) {
	broad := JobProblem{Code: WritePathTooBroad, Subject: "**/*.go"}
	output := JobProblem{Code: OutputOutsideWritePaths, Subject: "manpage/magus.1"}
	gate := JobProblem{Code: GateNotRun, Subject: "lint ."}
	ps := JobProblems{broad, output, gate}

	kept, waived := ps.Waive([]JobWaiver{
		{Code: OutputOutsideWritePaths, Subject: "manpage/magus.1", Reason: "the root regenerates it"},
		{Code: GateNotRun, Subject: "test .", Reason: "a different gate"},
		{Code: WritePathTooBroad, Subject: "manpage/magus.1", Reason: "a different code's subject"},
	})

	assert.Equal(t, JobProblems{broad, gate}, kept)
	assert.Equal(t, JobProblems{output}, waived)
}

func TestWaiveNeverWaivesAnUnwaivableCode(t *testing.T) {
	ps := JobProblems{{Code: JobRecordInvalid, Field: "id", Subject: "x"}}

	kept, waived := ps.Waive([]JobWaiver{{Code: JobRecordInvalid, Subject: "x", Reason: "no"}})

	assert.Equal(t, ps, kept)
	assert.Empty(t, waived)
}
