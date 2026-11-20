package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

func TestJobProblemErrCarriesItsCodeAndHint(t *testing.T) {
	p := JobProblem{Code: WritePathIsDirectory, Field: "paths.write[0]", Message: `"internal/job" is a directory`, Hint: "list the files"}

	err := p.Error()

	require.ErrorIs(t, err, WritePathIsDirectory)
	assert.Equal(t, `"internal/job" is a directory: list the files`, err.Msg)
}

func TestJobProblemErrWithoutAHintIsTheMessage(t *testing.T) {
	assert.Equal(t, "bad", JobProblem{Code: JobRecordInvalid, Message: "bad"}.Error().Msg)
}

func TestJobProblemRemediesEncodeAsDataOnlyWhenSet(t *testing.T) {
	bare, err := codec.Marshal(JobProblem{Code: JobRecordInvalid, Message: "bad", Hint: "fix it"})
	require.NoError(t, err)
	assert.NotContains(t, string(bare), "postcondition")
	assert.NotContains(t, string(bare), "deviation")

	p := JobProblem{
		Code: GateNotRun, Message: "m", Hint: "f",
		Deviation: &job.Deviation{Code: job.Code(GateNotRun), Subject: "test .", Reason: "github-actions runs it"},
	}
	enc, err := codec.Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(enc), `"deviation":{"code":"MGS3045","subject":"test .","reason":"github-actions runs it"}`)

	var back JobProblem
	require.NoError(t, codec.Unmarshal(enc, &back))
	assert.Equal(t, p, back)
}

func TestJobProblemsErrIsNilWhenEmpty(t *testing.T) {
	require.NoError(t, JobProblems(nil).Error())
	require.NoError(t, JobValidation{}.Error())
}

func TestJobProblemsErrJoinsEveryProblemInRecordOrder(t *testing.T) {
	ps := JobProblems{
		{Code: JobRecordInvalid, Field: "id", Message: "first"},
		{Code: JobPostconditionInvalid, Field: "postconditions[0].id", Message: "second"},
	}

	err := JobValidation{Problems: ps}.Error()

	require.ErrorIs(t, err, JobRecordInvalid)
	require.ErrorIs(t, err, JobPostconditionInvalid)
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

	kept, permitted := ps.Permit([]job.Deviation{
		{Code: job.Code(OutputOutsideWritePaths), Subject: "manpage/magus.1", Reason: "the root regenerates it"},
		{Code: job.Code(GateNotRun), Subject: "test .", Reason: "a different gate"},
		{Code: job.Code(WritePathTooBroad), Subject: "manpage/magus.1", Reason: "a different code's subject"},
	})

	assert.Equal(t, JobProblems{broad, gate}, kept)
	assert.Equal(t, JobProblems{output}, permitted)
}

func TestPermitNeverPermitsAnUnpermissibleCode(t *testing.T) {
	ps := JobProblems{{Code: JobRecordInvalid, Field: "id", Subject: "x"}}

	kept, permitted := ps.Permit([]job.Deviation{{Code: job.Code(JobRecordInvalid), Subject: "x", Reason: "no"}})

	assert.Equal(t, ps, kept)
	assert.Empty(t, permitted)
}

func TestJobProblemsErrKeepsEachProblem(t *testing.T) {
	err := JobProblems{{Code: OutputOutsideWritePaths, Message: "x", Hint: "add it"}}.Error()

	var diag *DiagnosticError
	require.ErrorAs(t, err, &diag)
	assert.Equal(t, "x: add it", diag.Msg)
}

func TestDoubleStarPermitsEveryRegeneratedOutputSubject(t *testing.T) {
	w := job.Deviation{Code: job.Code(OutputOutsideWritePaths), Subject: "**", Reason: "r"}
	for _, subject := range []string{
		"**/gen/*test/*.go",
		"internal/interp/bindings/gen/*.go !internal/interp/bindings/gen/*_test.go !internal/interp/bindings/gen/boundary_*.go",
		"docs/gen/**",
		"benchmarks/agent/reports/**/*.md",
		"MAGUS.md",
		"manpage/*.1",
	} {
		assert.Truef(t, DeviationPermits(w, JobProblem{Code: OutputOutsideWritePaths, Subject: subject}), "%q", subject)
	}
}
