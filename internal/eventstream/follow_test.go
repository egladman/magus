package eventstream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeLog appends raw JSONL to one invocation's run log, standing in for a
// concurrently running magus process.
func writeLog(t *testing.T, dir, inv, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f, err := os.OpenFile(filepath.Join(dir, inv+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// collect drains events into a slice via the emit callback the Follower takes.
func collect(got *[]types.StreamEvent) func(types.StreamEvent) error {
	return func(e types.StreamEvent) error {
		*got = append(*got, e)
		return nil
	}
}

func TestFollowerReplaysRetainedRuns(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "inv1", `{"ts":1,"inv":"inv1","kind":"started","command":{"arguments":["run","build"],"trigger":"run"}}
{"ts":2,"inv":"inv1","kind":"result","project":"api","target":"build","status":"pass","ref":"out_1"}
{"ts":3,"inv":"inv1","kind":"finished","status":"pass"}
`)

	var got []types.StreamEvent
	f := NewFollower(dir, "/repo")
	require.NoError(t, f.Replay(0, collect(&got)))

	require.Len(t, got, 3)
	assert.Equal(t, types.StreamRunStarted, got[0].Body.StreamType())
	assert.Equal(t, types.StreamTarget{Project: "api", Target: "build", Status: "ok", Ref: "out_1"}, got[1].Body)
	assert.Equal(t, types.StreamRunFinished, got[2].Body.StreamType())
	assert.Equal(t, "/repo", got[0].Workspace)
}

// TestFollowerReplayDoesNotRepeatOnFollow: Replay leaves the offset at end of
// file, so attaching a follow after it must deliver only what is new.
func TestFollowerReplayDoesNotRepeatOnFollow(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n")

	var replayed []types.StreamEvent
	f := NewFollower(dir, "/repo")
	require.NoError(t, f.Replay(0, collect(&replayed)))
	require.Len(t, replayed, 1)

	var fresh []types.StreamEvent
	require.NoError(t, f.drain("inv1.jsonl", collect(&fresh)))
	assert.Empty(t, fresh)
}

// TestFollowerSkipsPartialLines is the property that makes reading a log while
// magus writes it safe: an output-triggered page flush can split a line, and
// half a JSON object must never reach a subscriber - nor be skipped once the
// rest of it lands.
func TestFollowerSkipsPartialLines(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n"+`{"ts":2,"inv":"inv1","kind":"res`)

	var got []types.StreamEvent
	f := NewFollower(dir, "/repo")
	require.NoError(t, f.Replay(0, collect(&got)))
	require.Len(t, got, 1, "the torn line must not be emitted")

	writeLog(t, dir, "inv1", `ult","project":"api","target":"build","status":"pass"}`+"\n")
	require.NoError(t, f.drain("inv1.jsonl", collect(&got)))
	require.Len(t, got, 2, "the completed line must be picked up, not lost")
	assert.Equal(t, types.StreamTargetResult, got[1].Body.StreamType())
}

// TestFollowerReplayLimitKeepsNewest: attaching to a workspace with a long
// history must not deliver all of it.
func TestFollowerReplayLimitKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for _, inv := range []string{"inv1", "inv2", "inv3"} {
		writeLog(t, dir, inv, `{"ts":1,"inv":"`+inv+`","kind":"finished","status":"pass"}`+"\n")
		// Distinct mtimes: ordering is by modification time, and a same-tick write
		// would make the assertion depend on filesystem timestamp resolution.
		time.Sleep(10 * time.Millisecond)
	}

	var got []types.StreamEvent
	require.NoError(t, NewFollower(dir, "/repo").Replay(2, collect(&got)))
	require.Len(t, got, 2)
	assert.Equal(t, "inv2", got[0].Inv)
	assert.Equal(t, "inv3", got[1].Inv)
}

// TestFollowerReplayLimitSurvivesTheFirstFollowTick is the regression the limit
// test above cannot catch on its own: Replay honoured the window, and then Follow
// re-listed the whole directory and drained every log the window had excluded from
// offset zero. Attaching to a workspace with months of history replayed all of it
// one tick later, which is the failure --limit exists to prevent.
func TestFollowerReplayLimitSurvivesTheFirstFollowTick(t *testing.T) {
	dir := t.TempDir()
	for _, inv := range []string{"inv1", "inv2", "inv3"} {
		writeLog(t, dir, inv, `{"ts":1,"inv":"`+inv+`","kind":"finished","status":"pass"}`+"\n")
		time.Sleep(10 * time.Millisecond)
	}

	f := NewFollower(dir, "/repo")
	var replayed []types.StreamEvent
	require.NoError(t, f.Replay(1, collect(&replayed)))
	require.Len(t, replayed, 1)

	var followed []types.StreamEvent
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	require.NoError(t, f.Follow(ctx, 10*time.Millisecond, collect(&followed)))
	assert.Empty(t, followed, "the excluded logs must stay excluded once Follow starts")
}

// TestFollowerSkipDeliversOnlyWhatHappensNext covers plain `--follow`: history
// is positioned past, not replayed.
func TestFollowerSkipDeliversOnlyWhatHappensNext(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "inv1", `{"ts":1,"inv":"inv1","kind":"finished","status":"pass"}`+"\n")

	f := NewFollower(dir, "/repo")
	require.NoError(t, f.Skip())

	var got []types.StreamEvent
	require.NoError(t, f.drain("inv1.jsonl", collect(&got)))
	assert.Empty(t, got)

	writeLog(t, dir, "inv1", `{"ts":2,"inv":"inv1","kind":"result","project":"api","target":"test","status":"fail"}`+"\n")
	require.NoError(t, f.drain("inv1.jsonl", collect(&got)))
	require.Len(t, got, 1)
	assert.Equal(t, types.StreamTarget{Project: "api", Target: "test", Status: "failed"}, got[0].Body)
}

// TestFollowerMissingDirectoryIsNotAnError: a workspace that has never run
// anything is a normal state for an editor attaching at startup.
func TestFollowerMissingDirectoryIsNotAnError(t *testing.T) {
	f := NewFollower(filepath.Join(t.TempDir(), "absent"), "/repo")
	var got []types.StreamEvent
	require.NoError(t, f.Replay(0, collect(&got)))
	assert.Empty(t, got)
}

// TestFollowPicksUpANewInvocation is the end-to-end claim the design rests on:
// a run started by ANOTHER process reaches a follower that was already attached.
func TestFollowPicksUpANewInvocation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o755))

	f := NewFollower(dir, "/repo")
	require.NoError(t, f.Skip())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	seen := make(chan types.StreamEvent, 4)
	done := make(chan error, 1)
	go func() {
		done <- f.Follow(ctx, 10*time.Millisecond, func(e types.StreamEvent) error {
			seen <- e
			return nil
		})
	}()

	writeLog(t, dir, "inv9", `{"ts":1,"inv":"inv9","kind":"result","project":"api","target":"build","status":"fail"}`+"\n")

	select {
	case got := <-seen:
		assert.Equal(t, types.StreamTarget{Project: "api", Target: "build", Status: "failed"}, got.Body)
		assert.Equal(t, "inv9", got.Inv)
	case <-time.After(3 * time.Second):
		t.Fatal("a run started elsewhere never reached the follower")
	}

	cancel()
	require.NoError(t, <-done, "cancellation is not a failure")
}
