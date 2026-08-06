package cache

import (
	"os"
	"path/filepath"
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
		assert.NoError(t, json.Unmarshal(b, &got), "the file must never be left torn")
	}
	// No stray temp files: a failed write must clean up after itself.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "temp files must not accumulate")
	}
}

// A cache with nowhere to write must not make every call site nil-check.
func TestInflightNilIsUsable(t *testing.T) {
	var f *inflight
	assert.NotPanics(t, func() { f.start("a", "b")() })
	assert.Empty(t, f.takeAbandoned())
}
