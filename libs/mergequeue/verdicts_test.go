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

	"github.com/egladman/magus/libs/mergequeue/types"
)

func TestPollSeesOnlyFinishedVerdictsEachOnceAndTheDoneMarker(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()
	reader := &VerdictDir{Path: path, Follow: true}
	batch, err := reader.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, batch.Verdicts)
	assert.False(t, batch.Done)

	writer := &VerdictDir{Path: path}
	require.NoError(t, writer.Record(green("pr-1")))
	require.NoError(t, os.Mkdir(filepath.Join(path, ".pr-3-123"), 0o755)) // a Record in progress
	batch, err = reader.Poll(ctx)
	require.NoError(t, err)
	assert.False(t, batch.Done)
	assert.Equal(t, []string{"pr-1"}, idsOf(batch.Verdicts))

	require.NoError(t, writer.Record(waiting("2")))
	require.NoError(t, writer.MarkDone())
	batch, err = reader.Poll(ctx)
	require.NoError(t, err)
	assert.True(t, batch.Done)
	assert.Equal(t, []string{"2"}, idsOf(batch.Verdicts), "only what appeared since the last poll")
}

func TestRecordRefusesAnIDThatEscapesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts")
	err := (&VerdictDir{Path: path}).Record(waiting(".."))
	require.ErrorContains(t, err, `change id ".."`)
	assert.DirExists(t, filepath.Dir(path), "the parent survives")
}

// One unreadable entry holds its own change; before, it stopped applying for every
// change in the run.
func TestPollRejectsAnUnreadableVerdictForItsChangeAlone(t *testing.T) {
	path := t.TempDir()
	d := &VerdictDir{Path: path}
	require.NoError(t, d.Record(waiting("1")))
	require.NoError(t, os.Rename(filepath.Join(path, "1"), filepath.Join(path, "2")))
	require.NoError(t, d.Record(waiting("3")))
	require.NoError(t, os.MkdirAll(filepath.Join(path, "4"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "4", VerdictFile), []byte("{"), 0o644))
	batch, err := (&VerdictDir{Path: path}).Poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"3"}, idsOf(batch.Verdicts))
	require.Len(t, batch.Rejected, 2)
	assert.Equal(t, "2", batch.Rejected[0].Change)
	assert.Contains(t, batch.Rejected[0].Reason, `holds the verdict on "1"`)
	assert.Equal(t, "4", batch.Rejected[1].Change)
}

func planWith(t *testing.T, ids ...string) types.Plan {
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
