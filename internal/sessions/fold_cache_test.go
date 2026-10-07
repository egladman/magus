package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentRecord(invocation string, seq uint64, path string) Record {
	return Record{
		V: SchemaVersion, Invocation: invocation, Seq: seq, Kind: KindAgentEvent, Ts: int64(seq),
		Payload: []byte(fmt.Sprintf(`{"host":"h","event":"file.read","ref":"%d","at":%d,"text":%q}`, seq, seq, path)),
	}
}

// agentEventsOf is the oracle: ReadAll, narrowed the way ReadAgentEvents narrows.
func agentEventsOf(t *testing.T, dir string) Fold {
	t.Helper()
	fold := mustRead(t, dir)
	var records []Record
	for _, rec := range fold.Records {
		if rec.Kind == KindAgentEvent {
			records = append(records, rec)
		}
	}
	fold.Records = records
	return fold
}

func TestReadAgentEventsMatchesReadAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []Record{
		{V: SchemaVersion, Invocation: "a", Seq: 1, Kind: KindInvocationStart, Ts: 1},
		agentRecord("a", 2, "x.go"),
	})
	writeRaw(t, dir, "b", []Record{agentRecord("b", 3, "y.go")})
	writeLegacy(t, dir, "legacy", time.Now())

	for range 2 { // the miss that writes the cache, then the hit that reads it
		got, err := ReadAgentEvents(dir)
		require.NoError(t, err)
		assert.Equal(t, agentEventsOf(t, dir), got)
	}
}

// The point of the cache: an unchanged file is not read again. Rewriting a file's bytes
// while restoring its size and modification time is invisible to the key, so a read that
// still returns the old records proves the file was not opened.
func TestReadAgentEventsReadsOnlyFilesThatChanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []Record{agentRecord("a", 1, "x.go")})
	writeRaw(t, dir, "b", []Record{agentRecord("b", 2, "y.go")})
	_, err := ReadAgentEvents(dir)
	require.NoError(t, err)

	pathA := filepath.Join(dir, "a"+fileExt)
	fi, err := os.Stat(pathA)
	require.NoError(t, err)
	before, err := os.ReadFile(pathA)
	require.NoError(t, err)
	f, err := os.OpenFile(pathA, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte(string(before[:len(before)-2])+"X\n"), 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, os.Chtimes(pathA, fi.ModTime(), fi.ModTime()))

	w, err := Open(dir, "b", InvocationStart{})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindAgentEvent, AgentEvent{Host: "h", Kind: EventFileWrite, Ref: "w", Text: "z.go"}))

	got, err := ReadAgentEvents(dir)
	require.NoError(t, err)
	require.Len(t, got.Records, 3, "a's cached record, b's original, and b's append")
	assert.Equal(t, "a", got.Records[0].Invocation, "a was served from the cache, not reread")
	assert.Zero(t, got.Skipped, "the damaged line in a was never read")
}

func TestReadAgentEventsRefoldsFromATornCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []Record{agentRecord("a", 1, "x.go")})
	_, err := ReadAgentEvents(dir)
	require.NoError(t, err)

	cache := foldCachePath(dir, KindAgentEvent)
	whole, err := os.ReadFile(cache)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cache, whole[:len(whole)/2], 0o644))

	got, err := ReadAgentEvents(dir)
	require.NoError(t, err)
	assert.Equal(t, agentEventsOf(t, dir), got)

	repaired, err := os.ReadFile(cache)
	require.NoError(t, err)
	assert.Equal(t, whole, repaired, "the refold writes the cache back whole")
}

func TestReadAgentEventsForgetsAPrunedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []Record{agentRecord("a", 1, "x.go")})
	writeRaw(t, dir, "b", []Record{agentRecord("b", 2, "y.go")})
	_, err := ReadAgentEvents(dir)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, "a"+fileExt)))

	got, err := ReadAgentEvents(dir)
	require.NoError(t, err)
	assert.Equal(t, agentEventsOf(t, dir), got)
	assert.NotContains(t, readFoldCache(foldCachePath(dir, KindAgentEvent), KindAgentEvent).Files, "a"+fileExt)
}

// Each kind keeps its own cache, so the push rule's read of gate verdicts never decodes the
// agent events, and a gate result appended later is seen by the next read.
func TestReadGateResultsFoldsOnlyGateVerdicts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRaw(t, dir, "a", []Record{agentRecord("a", 1, "x.go")})
	require.NoError(t, RecordGate(dir, GateResult{Target: "ci", Commit: "abc123", Outcome: OutcomePass}, InvocationStart{}))

	_, err := ReadAgentEvents(dir)
	require.NoError(t, err)
	gates, err := ReadGateResults(dir)
	require.NoError(t, err)
	require.Len(t, gates.Records, 1)
	assert.Equal(t, KindGateResult, gates.Records[0].Kind)
	assert.Empty(t, readFoldCache(foldCachePath(dir, KindGateResult), KindGateResult).Files["a"+fileExt].Records,
		"the gate cache holds no agent event")

	require.NoError(t, RecordGate(dir, GateResult{Target: "ci", Commit: "def456", Outcome: OutcomeFail}, InvocationStart{}))
	gates, err = ReadGateResults(dir)
	require.NoError(t, err)
	rec, ok := GateAt(gates, "def456", "ci")
	require.True(t, ok)
	assert.Equal(t, OutcomeFail, rec.Outcome)
}

// Readers and writers race on one store, as every worktree of a repository does. Each
// read must equal some fold of the files, and once the writers stop, a read must equal
// the final fold exactly: a cache written mid-append cannot have pinned a short one.
func TestReadAgentEventsUnderConcurrentReadersAndWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const writers, appends = 4, 25
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			w, err := Open(dir, fmt.Sprintf("w%d", i), InvocationStart{})
			if !assert.NoError(t, err) { //nolint:testifylint // a wg.Go goroutine, where require's FailNow cannot stop the test
				return
			}
			for j := range appends {
				assert.NoError(t, w.Append(KindAgentEvent, AgentEvent{Host: "h", Kind: EventFileRead, Ref: fmt.Sprint(j), Text: "f.go"}))
			}
		})
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := ReadAgentEvents(dir)
				if assert.NoError(t, err) {
					assert.LessOrEqual(t, len(got.Records), writers*appends)
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	got, err := ReadAgentEvents(dir)
	require.NoError(t, err)
	assert.Len(t, got.Records, writers*appends)
	assert.Equal(t, agentEventsOf(t, dir), got)
}
