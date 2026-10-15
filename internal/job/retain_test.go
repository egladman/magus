package job

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const retainNow = int64(2_000_000_000)

func aged(id string, state types.JobState, days int64) types.Job {
	return types.Job{ID: id, State: state, Updated: retainNow - days*24*3600}
}

func ids(rows []types.Job) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestRetireDropsWhatIsOverAndOld(t *testing.T) {
	t.Parallel()

	queued := aged("queued", types.StateDeclared, 9)
	queued.DependsOn = []string{"dependency"}
	child := aged("child", types.StateRunning, 0)
	child.Parent = "parent"
	parent := aged("parent", types.StatePass, 9)
	parent.Parent = "grandparent"
	server := aged("server", types.StatePass, 9)
	server.Holder = types.HolderServer
	rows := []types.Job{
		aged("passed", types.StatePass, 8),
		aged("failed", types.StateFail, 8),
		aged("ended", types.StateNoReturn, 8),
		aged("uncollected", types.StateExited, 8),
		aged("recent", types.StateNoReturn, 6),
		aged("idle", types.StateRunning, 30),
		aged("dependency", types.StatePass, 9),
		queued,
		aged("grandparent", types.StateNoReturn, 20),
		parent,
		child,
		server,
	}

	kept, dropped := retire(rows, retainNow, 7*24*time.Hour)

	assert.Equal(t, []string{"recent", "idle", "dependency", "queued", "grandparent", "parent", "child", "server"}, ids(kept),
		"a live row stays however old, and keeps every row its tree and queue name; server rows are the server's")
	assert.Equal(t, 4, dropped)

	kept, dropped = retire(rows, retainNow, 0)
	assert.Equal(t, ids(rows), ids(kept), "a zero window keeps every row")
	assert.Zero(t, dropped)
}

// A write runs the pass at most once per retainEvery, and the pass also removes the
// archives and lost temp files the window has outlived.
func TestAWriteRetainsOncePerInterval(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	now := time.Unix(retainNow, 0)
	s.clock = func() time.Time { return now }
	path, err := s.Path()
	require.NoError(t, err)
	dir := filepath.Dir(path)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	plant := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("{}"), 0o644))
		require.NoError(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))
		return p
	}
	week := config.Defaults().Jobs.Retain
	oldArchive := plant("jobs-20260101T000000Z.jsonl", week+time.Hour)
	newArchive := plant("jobs-20260201T000000Z.jsonl", time.Hour)
	lostTemp := plant(".jobs.jsonl.tmp.123", 2*time.Hour)
	other := plant("landing-probes.jsonl", week+time.Hour)

	write := func(rows ...types.Job) {
		t.Helper()
		require.NoError(t, s.update(t.Context(), func(f *jobsFile) error {
			f.Jobs = append(f.Jobs, rows...)
			return nil
		}))
	}
	write(aged("old", types.StatePass, 8), aged("live", types.StateRunning, 8))
	rows, err := s.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"old", "live"}, ids(rows.Jobs), "the first write stamps the clock and keeps every row")
	assert.Equal(t, retainNow, rows.Retained)
	assert.FileExists(t, oldArchive)

	now = now.Add(retainEvery - time.Second)
	write(aged("old-again", types.StatePass, 8))
	rows, err = s.read()
	require.NoError(t, err)
	assert.True(t, slices.Contains(ids(rows.Jobs), "old"), "no pass inside the interval")

	now = now.Add(time.Second)
	write()
	rows, err = s.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"live"}, ids(rows.Jobs), "the first write past the interval runs the pass")
	assert.Equal(t, now.Unix(), rows.Retained)
	assert.NoFileExists(t, oldArchive)
	assert.NoFileExists(t, lostTemp)
	assert.FileExists(t, newArchive, "an archive younger than the window stays")
	assert.FileExists(t, other, "only archives and temp files are the pass's to remove")
}

// The pass reads jobs.retain from the workspace's magus.yaml, where 0 keeps every row, and
// a value that does not read fails the write rather than retire rows by a window nobody
// chose.
func TestARetentionPassReadsJobsRetain(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		yaml    string
		want    []string
		wantErr string
	}{
		"absent keeps a week":  {want: []string{"two-days", "live"}},
		"a day":                {yaml: "jobs:\n  retain: 24h\n", want: []string{"live"}},
		"zero keeps every row": {yaml: "jobs:\n  retain: 0s\n", want: []string{"ten-days", "two-days", "live"}},
		"negative":             {yaml: "jobs:\n  retain: -1h\n", wantErr: "retain"},
		"not a duration":       {yaml: "jobs:\n  retain: forever\n", wantErr: "retain"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte(tc.yaml), 0o644))
			}
			s := tmpStore(t, root)
			s.clock = func() time.Time { return time.Unix(retainNow, 0) }
			rows := []types.Job{aged("ten-days", types.StatePass, 10), aged("two-days", types.StatePass, 2), aged("live", types.StateRunning, 10)}
			err := s.update(t.Context(), func(f *jobsFile) error {
				f.Jobs, f.Retained = rows, retainNow-int64(retainEvery/time.Second)
				return nil
			})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			got, err := s.read()
			require.NoError(t, err)
			assert.Equal(t, tc.want, ids(got.Jobs))
		})
	}
}

// A delete archives the row it drops, not the whole store: archiving every row on every
// delete is how the store's directory grew to fifteen copies of it.
func TestDeleteArchivesOnlyTheDroppedRow(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	seed(t, s, lease("keep"))
	seed(t, s, lease("drop"))
	_, err := s.Delete(t.Context(), "drop", false)
	require.NoError(t, err)

	path, err := s.Path()
	require.NoError(t, err)
	archives, err := filepath.Glob(filepath.Join(filepath.Dir(path), "jobs-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	archived := NewStore(Location{StateBase: t.TempDir(), Root: t.TempDir()})
	archived.path = archives[0]
	f, err := archived.read()
	require.NoError(t, err)
	assert.Equal(t, []string{"drop"}, ids(f.Jobs))
}
