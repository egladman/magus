package job

import (
	"bytes"
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderValidationNumbersEveryProblemWithItsFix(t *testing.T) {
	t.Parallel()

	v := types.JobValidation{
		Job: types.Job{ID: "j"},
		Problems: types.JobProblems{
			{Code: types.JobCheckUnrunnable, Field: "check.target", Message: "no such target", Fix: "name a real one"},
			{Code: types.JobUngraded, Message: "nothing grades the job"},
		},
	}
	var buf bytes.Buffer
	RenderValidation(&buf, v, false)
	assert.Equal(t, "2 problems:\n"+
		"  1. [MGS3040] check.target: no such target\n"+
		"     fix: name a real one\n"+
		"  2. [MGS3039] nothing grades the job\n", buf.String(), "a record-wide problem has no field, and one without a fix has no fix line")
}

func TestApplyValidationsGroupsApplysProblemsByRecord(t *testing.T) {
	t.Parallel()

	err := errors.Join(
		prefixed("b", types.JobProblems{{Code: types.JobUngraded, Message: "nothing grades the job", Fix: "add a check"}}).Error(),
		prefixed("a", types.JobProblems{{Code: types.JobCheckUnrunnable, Message: "no such target"}, {Code: types.JobUngraded, Message: "no grade"}}).Error(),
	)
	got, ok := ApplyValidations(err, []string{"a", "b"})

	assert.True(t, ok)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Job.ID, "records come back in the order the ids name them")
	require.Len(t, got[0].Problems, 2)
	assert.Equal(t, types.JobProblem{Code: types.JobCheckUnrunnable, Message: "no such target"}, got[0].Problems[0])
	assert.Equal(t, "nothing grades the job: add a check", got[1].Problems[0].Message, "the fix rides inside the message")

	_, ok = ApplyValidations(errors.Join(err, errors.New("the store could not be read")), []string{"a", "b"})
	assert.False(t, ok, "an error that is no record's problem is reported as it is")
}

func TestRenderValidationDryRunPrintsTheRecordItWouldStore(t *testing.T) {
	t.Parallel()

	v := types.JobValidation{Job: types.Job{ID: "j", WritePaths: []string{"a.go"}}}
	var buf bytes.Buffer
	RenderValidation(&buf, v, true)
	out := buf.String()
	assert.Contains(t, out, "no problems\nwould store:\n")
	assert.Contains(t, out, `"id": "j"`)

	buf.Reset()
	RenderValidation(&buf, v, false)
	assert.Empty(t, buf.String(), "a real run with nothing to say prints nothing")
}

func TestRenderValidationSummarizesDeviationsAndKeepsUnchecked(t *testing.T) {
	t.Parallel()

	v := types.JobValidation{
		Job: types.Job{
			ID:         "j",
			Deviations: []types.Deviation{{Code: types.OutputOutsideWritePaths, Subject: "docs/x.md", Reason: "the root regenerates it"}},
		},
		Permitted: types.JobProblems{{Code: types.OutputOutsideWritePaths, Field: "write_paths", Subject: "docs/x.md", Message: "x regenerates docs/x.md"}},
		Unchecked: []string{"write path breadth: the workspace did not load"},
	}
	var buf bytes.Buffer
	RenderValidation(&buf, v, false)
	assert.Equal(t, "permitted 1 (recorded deviations; -o json includes the details)\n"+
		"not checked:\n"+
		"  write path breadth: the workspace did not load\n", buf.String())
}

func TestRenderValidationKeepsLargeDeviationListsOutOfText(t *testing.T) {
	t.Parallel()

	v := types.JobValidation{Job: types.Job{ID: "j"}}
	for range 64 {
		v.Permitted = append(v.Permitted, types.JobProblem{
			Code: types.OutputOutsideWritePaths, Subject: "docs/x.md", Message: "generated output is integrated by the root",
		})
	}
	var buf bytes.Buffer
	RenderValidation(&buf, v, false)
	assert.Equal(t, "permitted 64 (recorded deviations; -o json includes the details)\n", buf.String())
}

func TestRenderValidationCountsWhatFixWouldApply(t *testing.T) {
	t.Parallel()

	v := types.JobValidation{Problems: types.JobProblems{
		{Code: types.GateNotRun, Message: "nothing runs lint", Edit: &types.JobEdit{AddGoal: &types.Goal{ID: "lint"}}},
		{Code: types.WritePathIsDirectory, Message: "cli is a directory"},
	}}
	var buf bytes.Buffer
	RenderValidation(&buf, v, false)
	assert.Equal(t, "2 problems:\n"+
		"  1. [MGS3045] nothing runs lint\n"+
		"  2. [MGS3018] cli is a directory\n"+
		"--fix would apply 1 of them\n", buf.String())
}

func TestApplyValidationsKeepsEachProblemsEdit(t *testing.T) {
	t.Parallel()

	edit := &types.JobEdit{AddWritePath: "docs/x.md"}
	err := prefixed("a", types.JobProblems{{Code: types.OutputOutsideWritePaths, Message: "x regenerates docs/x.md", Edit: edit}}).Error()

	got, ok := ApplyValidations(err, []string{"a"})

	require.True(t, ok)
	assert.Same(t, edit, got[0].Problems[0].Edit)
}
