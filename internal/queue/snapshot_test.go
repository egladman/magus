package queue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

func snapshotOf(base string, at int64) Snapshot {
	return Snapshot{
		Fetched: magustypes.InflightFetch{Provider: "github", Host: "github.com", Base: base, At: at, ElapsedMS: 3},
		Changes: types.Changes{Schema: types.SchemaChanges, Base: base, Changes: []types.Change{
			{ID: "7", Head: head("7"), Base: base, Branch: "feat", Author: "ann", Method: types.MethodSquash},
		}},
	}
}

func TestSnapshotRoundTripsPerBaseAndTheNewestWins(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	_, ok, err := NewestSnapshot(root)
	require.NoError(t, err)
	assert.False(t, ok, "nothing was ever fetched")

	require.NoError(t, WriteSnapshot(root, snapshotOf("main", 200)))
	require.NoError(t, WriteSnapshot(root, snapshotOf("release/1.x", 100)))

	got, ok, err := ReadSnapshot(root, "release/1.x")
	require.NoError(t, err)
	require.True(t, ok, "a base with a separator keys a file of its own")
	want := snapshotOf("release/1.x", 100)
	want.Schema = SchemaSnapshot
	assert.Equal(t, want, got)

	newest, ok, err := NewestSnapshot(root)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "main", newest.Fetched.Base)
}

func TestSnapshotRefusesAnotherSchema(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	dir, err := snapshotDir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, snapshotName("main")), []byte(`{"schema":"mergequeue.snapshot/v2"}`), 0o644))

	_, _, err = NewestSnapshot(root)
	require.ErrorContains(t, err, `document is "mergequeue.snapshot/v2", want "mergequeue.snapshot/v1"`)
}

// A plan lands on its base's snapshot and nowhere else: without the changes there is
// nothing for it to place.
func TestSnapshotRecordPlan(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	plan := types.Plan{Base: "main", BaseCommit: head("base"), Depth: 1}

	require.NoError(t, RecordPlan(root, plan))
	_, ok, err := ReadSnapshot(root, "main")
	require.NoError(t, err)
	assert.False(t, ok, "no snapshot was made for the plan")

	require.NoError(t, WriteSnapshot(root, snapshotOf("main", 200)))
	require.NoError(t, RecordPlan(root, plan))
	got, _, err := ReadSnapshot(root, "main")
	require.NoError(t, err)
	assert.Equal(t, &plan, got.Plan)
	assert.Len(t, got.Changes.Changes, 1, "the changes stay")
}

func TestSnapshotJoinInflight(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	list := magustypes.JobList{Jobs: []magustypes.Job{{ID: "a", State: magustypes.StateRunning}}}

	got, err := JoinInflight(t.Context(), root, list, job.Identity{Login: "ann"})
	require.NoError(t, err)
	assert.Equal(t, list, got, "never fetched joins nothing")

	require.NoError(t, WriteSnapshot(root, snapshotOf("main", 200)))
	got, err = JoinInflight(t.Context(), root, list, job.Identity{Login: "ann"})
	require.NoError(t, err)
	assert.Equal(t, &magustypes.InflightFetch{Provider: "github", Host: "github.com", Base: "main", At: 200, ElapsedMS: 3}, got.Fetched)
	assert.Equal(t, []magustypes.InflightChange{{
		ID: "7", Head: head("7"), Base: "main", Branch: "feat", Author: "ann", Intent: "squash", Mine: true, Attention: magustypes.AttentionQueue,
	}}, got.Changes)
}
