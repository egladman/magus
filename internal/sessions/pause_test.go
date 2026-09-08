package sessions

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hostPause() Pause {
	return Pause{
		Host:       "codex",
		Session:    "01a07dab-240a-75b2-b9fe-816562939726",
		Transcript: "/Users/e/.codex/sessions/2026/09/07/rollout.jsonl",
		Workspace:  "/repo",
		At: types.VCSCheckpoint{
			Revision: "bcac8934e1a2b3c4d5e6f708192a3b4c5d6e7f80",
			Branch:   "cache-dynamic-needs",
			Dirty:    true,
			VCS:      "git",
		},
		Note: "stopped mid-implementation",
	}
}

// A person types a note and nothing else. Everything below has to work from that.
func personPause(branch, note string) Pause {
	return Pause{
		Workspace: "/repo",
		At:        types.VCSCheckpoint{Revision: "62660968c1d2e3f4", Branch: branch, VCS: "git"},
		Note:      note,
	}
}

func TestRecordPauseRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := hostPause()

	recorded, err := RecordPause(dir, want, SessionStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, recorded)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, []Pause{want}, Pauses(fold))
}

// The case that makes this a developer's tool rather than an agent's: two branches
// parked by a person, with no host session anywhere, are two lines of work.
func TestPausesSeparateAPersonsBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	monday := personPause("cache-dynamic-needs", "waiting on the review")
	tuesday := personPause("release-index", "half a manifest written")

	recorded, err := RecordPause(dir, monday, SessionStart{})
	require.NoError(t, err)
	require.True(t, recorded)
	recorded, err = RecordPause(dir, tuesday, SessionStart{})
	require.NoError(t, err)
	require.True(t, recorded, "a second branch is not a refile of the first")

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Pause{monday, tuesday}, Pauses(fold))
}

// A host hook fires every turn. Turns that moved nothing must not each cost a record.
func TestRecordPauseSkipsAnUnchangedRefile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := hostPause()

	recorded, err := RecordPause(dir, p, SessionStart{})
	require.NoError(t, err)
	require.True(t, recorded)

	recorded, err = RecordPause(dir, p, SessionStart{})
	require.NoError(t, err)
	assert.False(t, recorded, "nothing about the work moved")

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Len(t, Pauses(fold), 1)
}

func TestRecordPauseSupersedesRatherThanRewrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := hostPause()
	second := first
	second.At.Revision = "cc76e76ef1e2d3c4b5a6978869574635241302ff"
	second.Note = "test design skill committed"

	_, err := RecordPause(dir, first, SessionStart{})
	require.NoError(t, err)
	recorded, err := RecordPause(dir, second, SessionStart{})
	require.NoError(t, err)
	assert.True(t, recorded)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, []Pause{second}, Pauses(fold), "one line of work, at its newest")

	kept := 0
	for _, rec := range fold.Records {
		if rec.Kind == KindSessionPause {
			kept++
		}
	}
	assert.Equal(t, 2, kept, "the superseded pause is still on disk; the store is append-only")
}

// Every pause on one line of work shares a session file, or a hook firing each turn
// buries the run history it sits above.
func TestRecordPauseAppendsToOneSessionFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := hostPause()

	for i, rev := range []string{"aaaa111122223333", "bbbb444455556666", "cccc777788889999"} {
		p.At.Revision = rev
		recorded, err := RecordPause(dir, p, SessionStart{})
		require.NoError(t, err)
		require.True(t, recorded, "revision %d moved", i)
	}

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, fold.Sessions, "three pauses, one session")
	assert.Len(t, Summarize(fold), 1)
}

func TestPausesReportOneThreadPerHostSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	codex := hostPause()
	claude := Pause{Host: "claude", Session: "54023016-5d8b", Workspace: "/repo", At: types.VCSCheckpoint{Revision: "62660968c", Branch: "main"}}

	_, err := RecordPause(dir, codex, SessionStart{})
	require.NoError(t, err)
	_, err = RecordPause(dir, claude, SessionStart{})
	require.NoError(t, err)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Pause{codex, claude}, Pauses(fold))
}

// The store is grow-only and every reader folds all of it, so one pasted transcript
// cannot be allowed to become a cost every later read pays.
func TestRecordPauseClampsANote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := personPause("main", strings.Repeat("e", MaxMessageBytes+512))

	_, err := RecordPause(dir, p, SessionStart{})
	require.NoError(t, err)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	got := Pauses(fold)
	require.Len(t, got, 1)
	assert.Equal(t, strings.Repeat("e", MaxMessageBytes)+messageTruncated, got[0].Note)
}

func TestPausesIsEmptyWithoutAny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{Command: "run build"})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Empty(t, Pauses(fold))
}

// A detached HEAD has no branch to key on; two of them in one workspace are one line
// of work rather than an unbounded stream of them.
func TestPauseKeyFallsBackToTheWorkspace(t *testing.T) {
	t.Parallel()
	detached := Pause{Workspace: "/repo", At: types.VCSCheckpoint{Revision: "deadbeef"}}
	other := Pause{Workspace: "/other", At: types.VCSCheckpoint{Revision: "deadbeef"}}

	assert.Equal(t, pauseKey(detached), pauseKey(Pause{Workspace: "/repo", At: types.VCSCheckpoint{Revision: "cafe"}}))
	assert.NotEqual(t, pauseKey(detached), pauseKey(other))
}
