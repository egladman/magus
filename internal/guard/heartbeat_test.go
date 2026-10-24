package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stampLines(t *testing.T, root string) int {
	t.Helper()
	store, err := heartbeatStore(root)
	require.NoError(t, err)
	read, err := file.ReadLines(store.Path)
	require.NoError(t, err)
	return len(read.Lines)
}

func TestStampHeartbeatThrottle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	ctx := context.Background()
	at := time.Unix(1_800_000_000, 0)
	stamp := func(session, lease string, after time.Duration) {
		stampHeartbeat(ctx, root, heartbeat{Session: session, Lease: lease, At: at.Add(after)})
	}

	stamp("s1", "job-a", 0)
	assert.Equal(t, 1, stampLines(t, root), "the first call stamps")
	stamp("s1", "job-a", 59*time.Second)
	assert.Equal(t, 1, stampLines(t, root), "a call inside the minute adds nothing")
	stamp("s1", "job-a", time.Minute)
	assert.Equal(t, 2, stampLines(t, root), "a call a minute on stamps again")
	stamp("s2", "job-a", 5*time.Second)
	stamp("s1", "job-b", 5*time.Second)
	assert.Equal(t, 4, stampLines(t, root), "another session or lease has its own throttle")

	store, err := heartbeatStore(root)
	require.NoError(t, err)
	got, err := store.Load()
	require.NoError(t, err)
	require.Len(t, got, 3, "the last line per pair is the entry")
	assert.Equal(t, at.Add(time.Minute).Unix(), got[0].At.Unix())
}

func TestStampHeartbeatSkipsWhatItCannotKey(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	stampHeartbeat(context.Background(), root, heartbeat{Session: "", Lease: "job-a", At: time.Now()})
	stampHeartbeat(context.Background(), root, heartbeat{Session: "s1", Lease: "", At: time.Now()})
	stampHeartbeat(context.Background(), "", heartbeat{Session: "s1", Lease: "job-a", At: time.Now()})
	assert.Zero(t, stampLines(t, root))
}

func TestStampHeartbeatStoreErrorDoesNotPanic(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	t.Setenv("XDG_STATE_HOME", blocker)
	assert.NotPanics(t, func() {
		stampHeartbeat(context.Background(), t.TempDir(), heartbeat{Session: "s1", Lease: "job-a", At: time.Now()})
	})
}

func TestHeartbeatJSONRoundTrip(t *testing.T) {
	in := heartbeat{Session: "s1", Lease: "job-a", Transcript: "/t.jsonl", At: time.Unix(1_800_000_000, 0)}
	raw, err := in.MarshalJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{"session":"s1","lease":"job-a","transcript":"/t.jsonl","at":1800000000}`, string(raw))
	var out heartbeat
	require.NoError(t, out.UnmarshalJSON(raw))
	assert.True(t, in.At.Equal(out.At))
	out.At = in.At
	assert.Equal(t, in, out)
}

func TestSilentLeases(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-45 * time.Minute)
	recent := now.Add(-5 * time.Minute)

	live := writeTranscript(t, bgSearchCall, bgSearchResult)
	finished := writeTranscript(t, bgSearchCall, bgSearchResult, bgSearchDone)

	for _, h := range []heartbeat{
		{Session: "s-silent", Lease: "silent", At: old},
		{Session: "s-live", Lease: "background", Transcript: live, At: old},
		{Session: "s-done", Lease: "finished-background", Transcript: finished, At: old},
		{Session: "s-recent", Lease: "recent", At: recent},
		{Session: "s-gone", Lease: "exited", At: old},
		{Session: "s-old", Lease: "silent", At: old.Add(-time.Hour)},
		{Session: "s-server", Lease: "server", At: old},
	} {
		stampHeartbeat(ctx, root, h)
	}
	rows := []types.Job{
		{ID: "silent", State: types.StateRunning},
		{ID: "background", State: types.StateRunning},
		{ID: "finished-background", State: types.StateDeclared},
		{ID: "recent", State: types.StateRunning},
		{ID: "exited", State: types.StateNoReturn},
		{ID: "unstamped", State: types.StateRunning},
		{ID: "server", State: types.StateRunning, Holder: types.HolderServer},
	}

	got, err := SilentLeases(root, t.TempDir(), rows, now, SilentLeaseAfter)
	require.NoError(t, err)
	byLease := map[string]SilentLease{}
	for _, s := range got {
		byLease[s.Lease] = s
	}
	assert.Len(t, byLease, 2)
	require.Contains(t, byLease, "silent")
	silent := byLease["silent"]
	assert.Contains(t, silent.BackgroundNote, "no transcript", "an unread background state is said, not assumed")
	assert.True(t, old.Equal(silent.LastSeen), "the newest stamp is the one seen")
	silent.BackgroundNote, silent.LastSeen = "", time.Time{}
	assert.Equal(t, SilentLease{Lease: "silent", Session: "s-silent", SilentSeconds: 45 * 60}, silent, "the newest stamp names the session")
	assert.Contains(t, byLease, "finished-background", "a transcript whose background command ended counts it dead")
	assert.NotContains(t, byLease, "background", "a running background command counts as alive")
	assert.NotContains(t, byLease, "recent")
	assert.NotContains(t, byLease, "exited", "only a row still being edited is leased")
	assert.NotContains(t, byLease, "unstamped", "a row never stamped is unobserved, not silent")
	assert.NotContains(t, byLease, "server", "a server-held row has no session to be silent")
}

// A holder is dead only on evidence: silent past the bound with a transcript that shows no
// background command running. An unread transcript, a running command, a recent call or a
// row nobody took leaves it alive, since its path goes to someone else.
func TestDeadHolderNeedsSilenceAndNoBackgroundCommand(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	checkout := t.TempDir()
	at := location{workspace: t.TempDir(), cacheDir: t.TempDir()}
	store := job.NewStore(at.jobs())
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-45 * time.Minute)

	finished := writeTranscript(t, bgSearchCall, bgSearchResult, bgSearchDone)
	running := writeTranscript(t, bgSearchCall, bgSearchResult)
	for _, h := range []heartbeat{
		{Session: "s-dead", Lease: "dead", Transcript: finished, At: old},
		{Session: "s-bg", Lease: "background", Transcript: running, At: old},
		{Session: "s-blind", Lease: "blind", At: old},
		{Session: "s-recent", Lease: "recent", Transcript: finished, At: now.Add(-time.Minute)},
		{Session: "s-untaken", Lease: "untaken", Transcript: finished, At: old},
	} {
		stampHeartbeat(ctx, checkout, h)
	}
	row := func(id string, registered int64) types.Job {
		return types.Job{ID: id, State: types.StateRunning, Registered: registered, CheckoutRoot: checkout}
	}

	got, ok := deadHolder(store, at, row("dead", 1), now)
	require.True(t, ok)
	assert.Equal(t, job.DeadHolder{Row: row("dead", 1), Since: old.Unix(),
		Why: "its session s-dead made no judged call for 45m0s, past 30m0s, and runs no background command"}, got)

	for _, id := range []string{"background", "blind", "recent", "unstamped"} {
		_, ok := deadHolder(store, at, row(id, 1), now)
		assert.False(t, ok, id)
	}
	_, ok = deadHolder(store, at, row("untaken", 0), now)
	assert.False(t, ok, "a row nobody took has no holder to die")
}

// Past heartbeatMaxEntries pairs, a stamp rewrites the store by rename to the newest pairs,
// one line each, dropping any older than heartbeatRetain.
func TestStampHeartbeatCompactsPastItsBound(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	store, err := heartbeatStore(root)
	require.NoError(t, err)

	lines := make([][]byte, 0, heartbeatMaxEntries+1)
	stale, err := heartbeat{Session: "s", Lease: "stale", At: now.Add(-heartbeatRetain - time.Hour)}.MarshalJSON()
	require.NoError(t, err)
	lines = append(lines, stale)
	for i := range heartbeatMaxEntries {
		line, err := heartbeat{Session: "s", Lease: fmt.Sprintf("job-%d", i), At: now.Add(time.Duration(i-heartbeatMaxEntries) * time.Second)}.MarshalJSON()
		require.NoError(t, err)
		lines = append(lines, line)
	}
	require.NoError(t, file.RewriteLines(store.Path, lines, store.Perm))

	stampHeartbeat(ctx, root, heartbeat{Session: "s", Lease: "newest", At: now})
	assert.Equal(t, heartbeatMaxEntries, stampLines(t, root), "the store holds its bound, one line per pair")
	got, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, "job-1", got[0].Lease, "the stale stamp and the oldest pair went")
	assert.Equal(t, "newest", got[len(got)-1].Lease)
}
