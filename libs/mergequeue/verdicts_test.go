package mergequeue

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollSeesOnlyFinishedVerdictsEachOnceAndTheDoneMarker(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()
	reader := &VerdictDir{Path: path, Follow: true}
	fresh, done, err := reader.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, fresh)
	assert.False(t, done)

	exported := ""
	writer := &VerdictDir{Path: path, Export: func(_ context.Context, file, _, cand string) error {
		exported = cand
		return os.WriteFile(file, []byte("candidate"), 0o644)
	}}
	require.NoError(t, writer.Record(ctx, Verdict{Change: change("pr-1"), Decision: DecisionMerge, Candidate: "s1"}))
	require.NoError(t, os.Mkdir(filepath.Join(path, ".pr-3-123"), 0o755)) // a Record in progress
	fresh, done, err = reader.Poll(ctx)
	require.NoError(t, err)
	assert.False(t, done)
	require.Len(t, fresh, 1)
	assert.Equal(t, "s1", exported, "a green verdict carries its candidate")
	assert.Equal(t, filepath.Join(path, "pr-1", CandidateFile), fresh[0].CandidateFile)

	require.NoError(t, writer.Record(ctx, waiting("2")))
	require.NoError(t, writer.MarkDone())
	fresh, done, err = reader.Poll(ctx)
	require.NoError(t, err)
	assert.True(t, done)
	require.Len(t, fresh, 1, "only what appeared since the last poll")
	assert.Equal(t, "2", fresh[0].Change.ID)
	assert.Empty(t, fresh[0].CandidateFile)
}

func waiting(id string) Verdict {
	return Verdict{Change: change(id), Decision: DecisionWait, Code: CodeBehind, Reason: "r"}
}

func TestRecordRefusesAnIDThatEscapesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts")
	err := (&VerdictDir{Path: path}).Record(context.Background(), waiting(".."))
	require.ErrorContains(t, err, `change id ".."`)
	assert.DirExists(t, filepath.Dir(path), "the parent survives")
}

func TestPollRefusesAVerdictFiledUnderAnotherID(t *testing.T) {
	path := t.TempDir()
	require.NoError(t, (&VerdictDir{Path: path}).Record(context.Background(), waiting("1")))
	require.NoError(t, os.Rename(filepath.Join(path, "1"), filepath.Join(path, "2")))
	_, _, err := (&VerdictDir{Path: path}).Poll(context.Background())
	require.ErrorContains(t, err, `holds the verdict on "1"`)
}

func planWith(t *testing.T, ids ...string) Plan {
	t.Helper()
	p, err := ReadPlan(bytes.NewReader(planBytes(t, ids...)))
	require.NoError(t, err)
	return p
}

func TestWritePlanIsIdempotentAndRefusesADifferentPlan(t *testing.T) {
	d := &VerdictDir{Path: filepath.Join(t.TempDir(), "verdicts")}
	require.NoError(t, d.WritePlan(planWith(t, "1", "2")))
	require.NoError(t, d.WritePlan(planWith(t, "1", "2")), "every --only job of one run writes the same plan")
	require.ErrorContains(t, d.WritePlan(planWith(t, "3")), "holds a different plan")

	got, ok, err := d.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, planWith(t, "1", "2"), got)
	entries, err := os.ReadDir(d.Path)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file left behind")
}

func TestPlanWithoutFollowReadsOnce(t *testing.T) {
	_, ok, err := (&VerdictDir{Path: t.TempDir()}).Plan(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPlanWhileFollowingWaitsForThePlanOrTheDoneMarker(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	plan := planWith(t, "1")
	written := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		written <- (&VerdictDir{Path: path}).WritePlan(plan)
	}()
	got, ok, err := (&VerdictDir{Path: path, Follow: true, Interval: time.Millisecond}).Plan(ctx)
	require.NoError(t, err)
	require.NoError(t, <-written)
	require.True(t, ok)
	assert.Equal(t, plan, got)

	empty := t.TempDir()
	require.NoError(t, (&VerdictDir{Path: empty}).MarkDone())
	_, ok, err = (&VerdictDir{Path: empty, Follow: true, Interval: time.Millisecond}).Plan(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "complete without a plan")
}
