package verdicts

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
)

func change(id string) mergequeue.Change {
	return mergequeue.Change{ID: id, Head: strings.Repeat("a", 40)}
}

func TestPollSeesOnlyFinishedVerdictsEachOnceAndTheDoneMarker(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()
	reader := &Dir{Path: path, Follow: true}
	fresh, done, err := reader.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, fresh)
	assert.False(t, done)

	exported := ""
	writer := &Dir{Path: path, Export: func(_ context.Context, file, _, stage string) error {
		exported = stage
		return os.WriteFile(file, []byte("bundle"), 0o644)
	}}
	require.NoError(t, writer.Record(ctx, mergequeue.Verdict{Change: change("pr-1"), Decision: mergequeue.DecisionLand, Stage: "s1"}))
	require.NoError(t, os.Mkdir(filepath.Join(path, ".pr-3-123"), 0o755)) // a Record in progress
	fresh, done, err = reader.Poll(ctx)
	require.NoError(t, err)
	assert.False(t, done)
	require.Len(t, fresh, 1)
	assert.Equal(t, "s1", exported, "a green verdict carries its stage")
	assert.Equal(t, filepath.Join(path, "pr-1", BundleFile), fresh[0].Bundle)

	require.NoError(t, writer.Record(ctx, mergequeue.Verdict{Change: change("2"), Decision: mergequeue.DecisionWait}))
	require.NoError(t, writer.MarkDone())
	fresh, done, err = reader.Poll(ctx)
	require.NoError(t, err)
	assert.True(t, done)
	require.Len(t, fresh, 1, "only what appeared since the last poll")
	assert.Equal(t, "2", fresh[0].Change.ID)
	assert.Empty(t, fresh[0].Bundle)
}

func TestRecordRefusesAnIDThatEscapesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts")
	err := (&Dir{Path: path}).Record(context.Background(), mergequeue.Verdict{Change: change(".."), Decision: mergequeue.DecisionWait})
	require.ErrorContains(t, err, `change id ".."`)
	assert.DirExists(t, filepath.Dir(path), "the parent survives")
}

func TestPollRefusesAVerdictFiledUnderAnotherID(t *testing.T) {
	path := t.TempDir()
	require.NoError(t, (&Dir{Path: path}).Record(context.Background(), mergequeue.Verdict{Change: change("1"), Decision: mergequeue.DecisionWait}))
	require.NoError(t, os.Rename(filepath.Join(path, "1"), filepath.Join(path, "2")))
	_, _, err := (&Dir{Path: path}).Poll(context.Background())
	require.ErrorContains(t, err, `holds the verdict on "1"`)
}

func planOf(t *testing.T, ids ...string) mergequeue.Plan {
	t.Helper()
	p, err := mergequeue.ReadPlan(bytes.NewReader(planBytes(t, ids...)))
	require.NoError(t, err)
	return p
}

func TestWritePlanIsIdempotentAndRefusesADifferentPlan(t *testing.T) {
	d := &Dir{Path: filepath.Join(t.TempDir(), "verdicts")}
	require.NoError(t, d.WritePlan(planOf(t, "1", "2")))
	require.NoError(t, d.WritePlan(planOf(t, "1", "2")), "every --only job of one run writes the same plan")
	require.ErrorContains(t, d.WritePlan(planOf(t, "3")), "holds a different plan")

	got, ok, err := d.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, planOf(t, "1", "2"), got)
	entries, err := os.ReadDir(d.Path)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file left behind")
}

func TestPlanWithoutFollowReadsOnce(t *testing.T) {
	_, ok, err := (&Dir{Path: t.TempDir()}).Plan(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPlanWhileFollowingWaitsForThePlanOrTheDoneMarker(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	plan := planOf(t, "1")
	written := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		written <- (&Dir{Path: path}).WritePlan(plan)
	}()
	got, ok, err := (&Dir{Path: path, Follow: true, Interval: time.Millisecond}).Plan(ctx)
	require.NoError(t, err)
	require.NoError(t, <-written)
	require.True(t, ok)
	assert.Equal(t, plan, got)

	empty := t.TempDir()
	require.NoError(t, (&Dir{Path: empty}).MarkDone())
	_, ok, err = (&Dir{Path: empty, Follow: true, Interval: time.Millisecond}).Plan(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "complete without a plan")
}
