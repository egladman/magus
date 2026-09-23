package verdicts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
