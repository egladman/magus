package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCorpse plants the file a killed magus would have left: a real pid file, with a pid
// that cannot be running. The test cannot kill itself and keep asserting, so the corpse is
// synthesized rather than produced.
func writeCorpse(t *testing.T, dir, host string, pid int, project, target string, started time.Time) {
	t.Helper()
	b, err := json.Marshal([]inflightTarget{{
		Project: project, Target: target, Pid: pid, Host: host, Started: started,
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, inflightPrefix+"424242.json"), b, 0o644))
}

// TestInflightReportsAKilledRun is the whole point: a run that is SIGKILLed cannot say
// what it was doing, so the next run says it.
func TestInflightReportsAKilledRun(t *testing.T) {
	dir := t.TempDir()
	host, _ := os.Hostname()
	started := time.Now().Add(-9 * time.Minute)
	writeCorpse(t, dir, host, 2147483645, "docs", "generate", started)

	dead := newInflight(dir).takeAbandoned()
	require.Len(t, dead, 1)
	assert.Equal(t, "docs", dead[0].Project)

	msg := abandonedMessage(dead, time.Now())
	assert.Contains(t, msg, "docs generate")
	assert.Contains(t, msg, "9m0s in", "how long it had been running is the diagnosis")
	assert.Contains(t, msg, "OOM killer")

	// Reported once: a second run must not keep blaming a death already announced.
	assert.Empty(t, newInflight(dir).takeAbandoned())
}

// A live peer's file must survive another run's post-mortem sweep. This is what the
// shared-file design got wrong: reporting one corpse deleted everyone's records.
func TestInflightLeavesALivePeerAlone(t *testing.T) {
	dir := t.TempDir()
	host, _ := os.Hostname()
	writeCorpse(t, dir, host, 2147483645, "docs", "generate", time.Now())

	peer := newInflight(dir)
	defer peer.start("console", "build")()

	dead := newInflight(dir).takeAbandoned()
	require.Len(t, dead, 1, "only the corpse")
	assert.Equal(t, "docs", dead[0].Project)
	assert.FileExists(t, peer.path, "the live peer's record must survive the sweep")
}

// A pid from another machine is meaningless: CI restores a cache dir onto a fresh runner
// where that number belongs to something unrelated, or to nothing.
func TestInflightIgnoresAnotherHostsPid(t *testing.T) {
	dir := t.TempDir()
	writeCorpse(t, dir, "some-other-runner", os.Getpid(), "docs", "generate", time.Now())

	dead := newInflight(dir).takeAbandoned()
	require.Len(t, dead, 1, "our live pid on another host is still a corpse here")
	assert.Equal(t, "docs", dead[0].Project)
}

func TestInflightCleanRunReportsNothing(t *testing.T) {
	dir := t.TempDir()
	f := newInflight(dir)
	f.start("a", "build")()
	assert.NoFileExists(t, f.path, "a finished run leaves no corpse")
	assert.Empty(t, newInflight(dir).takeAbandoned())
	assert.Empty(t, abandonedMessage(nil, time.Now()))
}

// Every target edge runs inside an errgroup goroutine, so the file must stay parseable
// under concurrent starts and finishes. Run with -race.
func TestInflightConcurrentEdges(t *testing.T) {
	dir := t.TempDir()
	f := newInflight(dir)

	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := f.start("p", string(rune('a'+i%26)))
			done()
		}()
	}
	wg.Wait()

	assert.Empty(t, f.Running(), "every edge closed")
	if b, err := os.ReadFile(f.path); err == nil {
		var got []inflightTarget
		require.NoError(t, json.Unmarshal(b, &got), "the file must never be left torn")
	}
	// No stray temp files: a failed write must clean up after itself.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "temp files must not accumulate")
	}
}

// A Ctrl+C handler cannot reach a Cache, so InFlight must see every tracker in the
// process, and a cleared record must leave it. Other tests may run targets beside this
// one, so it asserts on its own names only.
func TestInflightForInterruptSpansEveryTracker(t *testing.T) {
	a, b := newInflight(t.TempDir()), newInflight(t.TempDir())
	doneA := a.start("interrupt-a", "build")
	doneB := b.start("interrupt-b", "test")

	names := func() []string {
		var out []string
		for _, r := range InFlight() {
			if strings.HasPrefix(r.Project, "interrupt-") {
				out = append(out, r.Project+" "+r.Target)
			}
		}
		return out
	}
	assert.Equal(t, []string{"interrupt-a build", "interrupt-b test"}, names())
	for _, r := range InFlight() {
		assert.False(t, r.Started.IsZero(), "the start time is what the warning ages")
	}

	doneA()
	assert.Equal(t, []string{"interrupt-b test"}, names(), "clearing one leaves the other")
	doneB()
	assert.Empty(t, names())
}

// A cache with nowhere to write must not make every call site nil-check.
func TestInflightNilIsUsable(t *testing.T) {
	var f *inflight
	assert.NotPanics(t, func() { f.start("a", "b")() })
	assert.Empty(t, f.takeAbandoned())
}

// One project and target can run twice at once (another charm set, other args). Each
// run clears its own record, so the first to finish does not erase the second.
func TestInflightSameTargetTwiceKeepsBothRecords(t *testing.T) {
	i := newInflight(t.TempDir())
	doneA := i.start("docs", "generate")
	doneB := i.start("docs", "generate")
	doneA()
	require.Len(t, i.Running(), 1, "finishing one run erased the other's record")
	doneB()
	assert.Empty(t, i.Running())
}

// A killed run's inflight temp file, under its current name or an older binary's, and a
// remote-tier staging directory are collected once stale; a fresh one may belong to a
// live writer and stays.
func TestInflightCollectsStaleLitter(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * staleAfter)
	tmp := filepath.Join(dir, "."+inflightPrefix+"1.json.tmp.123")
	olderTmp := filepath.Join(dir, inflightPrefix+"3.tmp")
	for _, p := range []string{tmp, olderTmp} {
		require.NoError(t, os.WriteFile(p, nil, 0o644))
	}
	staging := filepath.Join(dir, stagingPrefix+"old")
	require.NoError(t, os.MkdirAll(filepath.Join(staging, "cas"), 0o755))
	for _, p := range []string{tmp, olderTmp, staging} {
		require.NoError(t, os.Chtimes(p, old, old))
	}
	fresh := []string{"." + inflightPrefix + "2.json.tmp.456", inflightPrefix + "4.tmp"}
	for _, name := range fresh {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o644))
	}

	newInflight(dir).takeAbandoned()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, fresh, names)
}

// The record names projects and targets a run is building, so it stays private to its
// owner on a shared machine.
func TestInflightRecordIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	i := newInflight(t.TempDir())
	defer i.start("docs", "generate")()

	fi, err := os.Stat(i.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}
