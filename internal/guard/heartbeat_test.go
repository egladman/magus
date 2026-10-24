package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file"
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
		stampHeartbeat(ctx, root, Heartbeat{Session: session, Lease: lease, At: at.Add(after)})
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
	stampHeartbeat(context.Background(), root, Heartbeat{Session: "", Lease: "job-a", At: time.Now()})
	stampHeartbeat(context.Background(), root, Heartbeat{Session: "s1", Lease: "", At: time.Now()})
	stampHeartbeat(context.Background(), "", Heartbeat{Session: "s1", Lease: "job-a", At: time.Now()})
	assert.Zero(t, stampLines(t, root))
}

func TestStampHeartbeatStoreErrorDoesNotPanic(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	t.Setenv("XDG_STATE_HOME", blocker)
	assert.NotPanics(t, func() {
		stampHeartbeat(context.Background(), t.TempDir(), Heartbeat{Session: "s1", Lease: "job-a", At: time.Now()})
	})
}

func TestHeartbeatJSONRoundTrip(t *testing.T) {
	in := Heartbeat{Session: "s1", Lease: "job-a", Transcript: "/t.jsonl", At: time.Unix(1_800_000_000, 0)}
	raw, err := in.MarshalJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{"session":"s1","lease":"job-a","transcript":"/t.jsonl","at":1800000000}`, string(raw))
	var out Heartbeat
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

	for _, h := range []Heartbeat{
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
	assert.Equal(t, "s-silent", byLease["silent"].Session, "the newest stamp names the session")
	assert.Equal(t, int64(45*60), byLease["silent"].SilentSeconds)
	assert.Contains(t, byLease["silent"].BackgroundNote, "no transcript", "an unread background state is said, not assumed")
	assert.Contains(t, byLease, "finished-background", "a transcript whose background command ended counts it dead")
	assert.NotContains(t, byLease, "background", "a running background command counts as alive")
	assert.NotContains(t, byLease, "recent")
	assert.NotContains(t, byLease, "exited", "only a row still being edited is leased")
	assert.NotContains(t, byLease, "unstamped", "a row never stamped is unobserved, not silent")
	assert.NotContains(t, byLease, "server", "a server-held row has no session to be silent")
}
