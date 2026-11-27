package job

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const retainNow = int64(2_000_000_000)

func aged(id string, state job.State, days int64) job.Job {
	return job.Job{ID: id, State: state, Updated: retainNow - days*24*3600}
}

func ids(rows []job.Job) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestRetireDropsWhatIsOverAndOld(t *testing.T) {
	t.Parallel()

	queued := aged("queued", job.StateDeclared, 9)
	queued.DependsOn = []string{"dependency"}
	child := aged("child", job.StateRunning, 0)
	child.Parent = "parent"
	parent := aged("parent", job.StatePass, 9)
	parent.Parent = "grandparent"
	server := aged("server", job.StatePass, 9)
	server.RegisteredBy = job.Origin{EntryPoint: job.EntryPointServer}
	rows := []job.Job{
		aged("passed", job.StatePass, 8),
		aged("failed", job.StateFail, 8),
		aged("ended", job.StateNoReturn, 8),
		aged("uncollected", job.StateExited, 8),
		aged("recent", job.StateNoReturn, 6),
		aged("idle", job.StateRunning, 30),
		aged("dependency", job.StatePass, 9),
		queued,
		aged("grandparent", job.StateNoReturn, 20),
		parent,
		child,
		server,
	}

	kept, dropped := retire(rows, retainNow, 7*24*time.Hour, nil)

	assert.Equal(t, []string{"recent", "idle", "dependency", "queued", "grandparent", "parent", "child", "server"}, ids(kept),
		"a live row stays however old, and keeps every row its tree and queue name; server rows are the server's")
	assert.Equal(t, 4, dropped)

	kept, dropped = retire(rows, retainNow, 0, nil)
	assert.Equal(t, ids(rows), ids(kept), "a zero window keeps every row")
	assert.Zero(t, dropped)
}

// Retention must not forget work: a row whose head no base is known to hold
// stays until it lands or a person sets it aside.
func TestRetireKeepsARowWhoseHeadNoBaseHolds(t *testing.T) {
	t.Parallel()
	wrote := func(id string) job.Job {
		r := aged(id, job.StateNoReturn, 30)
		r.Registered, r.Head = 1, id+"-head"
		return r
	}
	off := wrote("off")
	headless := wrote("headless")
	headless.Head = ""
	landed := wrote("landed")
	landed.Landed = true
	aside := wrote("aside")
	aside.LeftUnmerged = "a person dropped it"
	merged := wrote("merged")
	untaken := aged("untaken", job.StateNoReturn, 30)

	kept, dropped := retire([]job.Job{off, headless, landed, aside, merged, untaken}, retainNow, 7*24*time.Hour,
		map[placedMain]bool{{ID: "merged", Head: "merged-head"}: true})

	assert.Equal(t, []string{"off", "headless"}, ids(kept))
	assert.Equal(t, 4, dropped)
}

// A placement that finds a head on a base records it for the retention pass.
func TestAPlacementOnABaseIsRecordedForRetention(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	require.NoError(t, recordPlacedMain(root, []Placed{
		{Job: "merged", Head: "abc", Place: PlaceMain},
		{Job: "waiting", Head: "def", Place: PlaceLocal},
	}))
	require.NoError(t, recordPlacedMain(root, []Placed{{Job: "merged", Head: "abc", Place: PlaceMain}}))

	assert.Equal(t, map[placedMain]bool{{ID: "merged", Head: "abc"}: true}, readPlacedMain(root))
}

// A write runs the pass at most once per retainEvery, and the pass also removes the
// archives and lost temp files the window has outlived.
func TestAWriteRetainsOncePerInterval(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
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

	write := func(rows ...job.Job) {
		t.Helper()
		require.NoError(t, s.Rewrite(t.Context(), func(f *job.Rows) error {
			f.Jobs = append(f.Jobs, rows...)
			return nil
		}))
	}
	write(aged("old", job.StatePass, 8), aged("live", job.StateRunning, 8))
	rows, err := s.Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"old", "live"}, ids(rows.Jobs), "the first write stamps the clock and keeps every row")
	assert.Equal(t, retainNow, rows.Retained)
	assert.FileExists(t, oldArchive)

	now = now.Add(retainEvery - time.Second)
	write(aged("old-again", job.StatePass, 8))
	rows, err = s.Read(t.Context())
	require.NoError(t, err)
	assert.True(t, slices.Contains(ids(rows.Jobs), "old"), "no pass inside the interval")

	now = now.Add(time.Second)
	write()
	rows, err = s.Read(t.Context())
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
			s := tempStore(t, root)
			s.clock = func() time.Time { return time.Unix(retainNow, 0) }
			rows := []job.Job{aged("ten-days", job.StatePass, 10), aged("two-days", job.StatePass, 2), aged("live", job.StateRunning, 10)}
			err := s.Rewrite(t.Context(), func(f *job.Rows) error {
				f.Jobs, f.Retained = rows, retainNow-int64(retainEvery/time.Second)
				return nil
			})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			got, err := s.Read(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, ids(got.Jobs))
		})
	}
}

// A delete archives the row it drops, not the whole store: archiving every row on every
// delete is how the store's directory grew to fifteen copies of it.
func TestDeleteArchivesOnlyTheDroppedRow(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	seed(t, s, lease("keep"))
	seed(t, s, lease("drop"))
	_, err := s.Delete(t.Context(), "drop", false)
	require.NoError(t, err)

	path, err := s.Path()
	require.NoError(t, err)
	archives, err := filepath.Glob(filepath.Join(filepath.Dir(path), "jobs-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	// The module store reads the jobs.jsonl of the directory it opens, so the archive is
	// read from a copy under that name.
	archive, err := os.ReadFile(archives[0])
	require.NoError(t, err)
	archived := NewStore(Location{StateBase: t.TempDir(), Root: t.TempDir()})
	archived.path = filepath.Join(t.TempDir(), "jobs.jsonl")
	require.NoError(t, os.WriteFile(archived.path, archive, 0o600))
	f, err := archived.open().Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"drop"}, ids(f.Jobs))
}
