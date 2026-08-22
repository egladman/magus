package sessionjournal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const retention = 30 * 24 * time.Hour

// msAgo is a record timestamp age old, in the unix milliseconds a Record carries.
func msAgo(age time.Duration) int64 { return time.Now().Add(-age).UnixMilli() }

// writeAged lays down a session file and backdates its modification time to match.
// Both halves matter and they are not the same knob: rotation DECIDES on the newest
// record, and only reads a file at all once its modification time says it might be
// old enough to be worth reading.
func writeAged(t *testing.T, dir, session string, age time.Duration, records []Record) {
	t.Helper()
	writeRaw(t, dir, session, records)
	when := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filepath.Join(dir, session+fileExt), when, when))
}

// storedSessions names the journals still on disk, sorted.
func storedSessions(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := []string{}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), fileExt); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func mustRead(t *testing.T, dir string) Fold {
	t.Helper()
	fold, err := Read(dir)
	require.NoError(t, err)
	return fold
}

func TestRotatePrunesASessionWhoseNewestFactAgedOut(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "stale", 40*24*time.Hour, []Record{
		attRecord(t, "stale", 1, msAgo(40*24*time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})
	writeAged(t, dir, "fresh", time.Hour, []Record{
		attRecord(t, "fresh", 1, msAgo(time.Hour), KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}),
	})

	Rotate(dir, retention)

	assert.Equal(t, []string{"fresh"}, storedSessions(t, dir))
	assert.Len(t, mustRead(t, dir).Records, 1)
}

// A session that has run for months keeps its whole history, back to its first fact.
// Retention is a question about the file, and the answer is delete or leave alone:
// dropping the old records out of a live journal would leave a file whose beginning
// is missing, which nothing that reads one is written to expect.
func TestRotateKeepsEveryFactInAFileItSpares(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "long-lived", 90*24*time.Hour, []Record{
		attRecord(t, "long-lived", 1, msAgo(90*24*time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
		attRecord(t, "long-lived", 2, msAgo(80*24*time.Hour), KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}),
		attRecord(t, "long-lived", 3, msAgo(time.Hour), KindTargetResult, TargetResult{Target: "lint", Outcome: OutcomePass}),
	})

	Rotate(dir, retention)

	assert.Equal(t, []string{"long-lived"}, storedSessions(t, dir),
		"the newest record is inside the window, so the file is not old")
	assert.Len(t, mustRead(t, dir).Records, 3, "a spared file is spared whole")
}

// The modification time is a pre-filter, never the verdict. A file the filesystem
// calls ancient but whose records are recent survives, because a copy, a restore or a
// touch can move a modification time without adding or removing a single fact.
func TestRotateDecidesOnRecordsNotOnTheFilesystemClock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "misdated", 200*24*time.Hour, []Record{
		attRecord(t, "misdated", 1, msAgo(time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})

	Rotate(dir, retention)

	assert.Equal(t, []string{"misdated"}, storedSessions(t, dir))
}

// Rule 1: a request nobody has answered pins every file naming it, however old. The
// queue belongs to a person, and a retention window that emptied it would be an
// expiry - the one thing this package refuses to have.
func TestRotateExemptsAFileHoldingAStillOpenRequest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	age := 200 * 24 * time.Hour

	writeAged(t, dir, "blocked", age, []Record{
		attRecord(t, "blocked", 1, msAgo(age), KindAttentionOpen, openPayload("att-1", "needs a human")),
	})
	writeAged(t, dir, "quiet", age, []Record{
		attRecord(t, "quiet", 1, msAgo(age), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})

	Rotate(dir, retention)

	assert.Equal(t, []string{"blocked"}, storedSessions(t, dir),
		"the exemption covers the blocked session only; its neighbor ages out normally")

	open := OpenAttention(mustRead(t, dir))
	require.Len(t, open, 1)
	assert.Equal(t, "att-1", open[0].ID)
}

// Rule 2, the resurrection half. The agent that raised the block is still running, so
// its file is young; the person who disposed of it has not run magus since, so theirs
// is old. Pruning the dispose alone would put an answered request back in the queue.
func TestRotateWillNotResurrectARequestByPruningItsDispose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "agent", time.Hour, []Record{
		attRecord(t, "agent", 1, msAgo(200*24*time.Hour), KindAttentionOpen, openPayload("att-1", "needs a human")),
		attRecord(t, "agent", 2, msAgo(time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})
	writeAged(t, dir, "human", 199*24*time.Hour, []Record{
		attRecord(t, "human", 1, msAgo(199*24*time.Hour), KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "handled"}),
	})

	require.Empty(t, OpenAttention(mustRead(t, dir)), "the request is answered before rotation runs")

	Rotate(dir, retention)

	assert.Equal(t, []string{"agent", "human"}, storedSessions(t, dir))
	assert.Empty(t, OpenAttention(mustRead(t, dir)),
		"an answered request must not come back because its answer was garbage-collected")
}

// Rule 2, the other half: once no file naming a request is young enough to spare, the
// whole request goes at once. Nothing is left half-pruned.
func TestRotatePrunesARequestsFilesTogetherOnceBothAgedOut(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	age := 200 * 24 * time.Hour

	writeAged(t, dir, "agent", age, []Record{
		attRecord(t, "agent", 1, msAgo(age), KindAttentionOpen, openPayload("att-1", "needs a human")),
	})
	writeAged(t, dir, "human", age-time.Hour, []Record{
		attRecord(t, "human", 1, msAgo(age-time.Hour), KindAttentionDispose, AttentionDispose{Request: "att-1", Note: "handled"}),
	})

	Rotate(dir, retention)

	assert.Empty(t, storedSessions(t, dir))
	assert.Empty(t, Attention(mustRead(t, dir)))
}

// Exemptions travel: sparing a file for one request can spare a SECOND request that
// shares it, which in turn spares the file holding that request's other half. Here
// att-open pins the agent file, which pins att-done, which pins the human file - and
// nothing but the pinning would stop att-done's open from outliving its dispose.
//
// One pass in an unlucky map order gets this wrong, which is why rotation iterates to
// a fixed point.
func TestRotateExemptionPropagatesThroughASharedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	age := 200 * 24 * time.Hour

	writeAged(t, dir, "agent", age, []Record{
		attRecord(t, "agent", 1, msAgo(age), KindAttentionOpen, openPayload("att-open", "still waiting")),
		attRecord(t, "agent", 2, msAgo(age), KindAttentionOpen, openPayload("att-done", "was waiting")),
	})
	writeAged(t, dir, "human", age, []Record{
		attRecord(t, "human", 1, msAgo(age), KindAttentionDispose, AttentionDispose{Request: "att-done", Note: "handled"}),
	})

	Rotate(dir, retention)

	assert.Equal(t, []string{"agent", "human"}, storedSessions(t, dir))

	open := OpenAttention(mustRead(t, dir))
	require.Len(t, open, 1, "att-done stays answered; only att-open is still a wait")
	assert.Equal(t, "att-open", open[0].ID)
}

// Every producer opens, so opening is where the store pays for its own housekeeping.
// Open writes no record, so a store with nothing left to keep ends up empty.
func TestOpenRotatesTheStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "ancient", 400*24*time.Hour, []Record{
		attRecord(t, "ancient", 1, msAgo(400*24*time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})

	_, err := Open(dir, "current", SessionStart{Workspace: "/repo"})
	require.NoError(t, err)

	assert.Empty(t, storedSessions(t, dir))
}

// A writer must never delete the file it is about to append to, whatever the clock
// says about the journal already sitting under its session id.
func TestOpenNeverPrunesItsOwnJournal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "resumed", 400*24*time.Hour, []Record{
		attRecord(t, "resumed", 1, msAgo(400*24*time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})

	w, err := Open(dir, "resumed", SessionStart{})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}))

	assert.Equal(t, []string{"resumed"}, storedSessions(t, dir))
	assert.Len(t, mustRead(t, dir).Records, 3, "the old fact, the session start, and the new fact")
}

func TestRotateWithANonPositiveWindowKeepsEverything(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeAged(t, dir, "ancient", 400*24*time.Hour, []Record{
		attRecord(t, "ancient", 1, msAgo(400*24*time.Hour), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})

	Rotate(dir, 0)
	Rotate(dir, -time.Hour)

	assert.Equal(t, []string{"ancient"}, storedSessions(t, dir))
}

// Rotation in one process runs against a fold another process is in the middle of
// reading, so a listed file can be gone by the time the reader opens it. That is
// housekeeping finishing first, not corruption, and it must not surface as either an
// error or a damaged-line count.
func TestReadTreatsAFileThatVanishedMidFoldAsAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeRaw(t, dir, "kept", []Record{
		attRecord(t, "kept", 1, 10, KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}),
	})

	// A dangling symlink reproduces the race deterministically: the directory lists
	// the name, and the open that follows fails with ENOENT - exactly what a reader
	// meets when rotation unlinks a file between those two calls.
	if err := os.Symlink(filepath.Join(dir, "already-rotated"), filepath.Join(dir, "gone"+fileExt)); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	fold, err := Read(dir)
	require.NoError(t, err, "a pruned file must not fail the fold")
	assert.Len(t, fold.Records, 1)
	assert.Equal(t, 1, fold.Sessions, "a file that is no longer there is not a session anybody can read")
	assert.Zero(t, fold.Skipped, "rotation is not damage")
}

func TestReadFileReportsAMissingFileAsVanishedNotSkipped(t *testing.T) {
	t.Parallel()

	records, skipped, vanished := readFile(filepath.Join(t.TempDir(), "never-existed"+fileExt))
	assert.Empty(t, records)
	assert.Zero(t, skipped)
	assert.True(t, vanished)
}
