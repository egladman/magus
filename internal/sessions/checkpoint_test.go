package sessions

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hostCheckpoint() Checkpoint {
	return Checkpoint{
		Host:        "codex",
		HostSession: "01a07dab-240a-75b2-b9fe-816562939726",
		Transcript:  "/Users/e/.codex/sessions/2026/09/07/rollout.jsonl",
		Workspace:   "/repo",
		Tree: types.VCSCheckpoint{
			Revision: "bcac8934e1a2b3c4d5e6f708192a3b4c5d6e7f80",
			Branch:   "cache-dynamic-needs",
			Dirty:    true,
			VCS:      "git",
		},
		Note: "stopped mid-implementation",
	}
}

// A person types a note and nothing else. Everything below has to work from that.
func personCheckpoint(workspace, branch, note string) Checkpoint {
	return Checkpoint{
		Workspace: workspace,
		Tree:      types.VCSCheckpoint{Revision: "62660968c1d2e3f4", Branch: branch, VCS: "git"},
		Note:      note,
	}
}

func recorded(t *testing.T, dir string) []Checkpoint {
	t.Helper()
	fold, err := ReadAll(dir)
	require.NoError(t, err)
	out := make([]Checkpoint, 0, len(fold.Records))
	for _, rec := range LatestCheckpoints(fold) {
		assert.False(t, rec.At.IsZero(), "every checkpoint carries the envelope's timestamp")
		out = append(out, rec.Checkpoint)
	}
	return out
}

func TestRecordCheckpointRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := hostCheckpoint()

	stored, ok, err := RecordCheckpoint(dir, want, SessionStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, want, stored)
	assert.Equal(t, []Checkpoint{want}, recorded(t, dir))
}

// The case that makes this a developer's tool rather than an agent's: two branches
// parked by a person, with no host session anywhere, are two lines of work.
func TestLatestCheckpointsSeparateAPersonsBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	monday := personCheckpoint("/repo", "cache-dynamic-needs", "waiting on the review")
	tuesday := personCheckpoint("/repo", "release-index", "half a manifest written")

	_, ok, err := RecordCheckpoint(dir, monday, SessionStart{})
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = RecordCheckpoint(dir, tuesday, SessionStart{})
	require.NoError(t, err)
	require.True(t, ok, "a second branch is not a refile of the first")

	assert.ElementsMatch(t, []Checkpoint{monday, tuesday}, recorded(t, dir))
}

// The store is repo-wide across clones and worktrees, so a branch name alone is not a
// line of work: two checkouts parked on main are two.
func TestLatestCheckpointsSeparateWorktreesOnOneBranch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	here := personCheckpoint("/repo", "main", "mid-refactor")
	there := personCheckpoint("/repo-review", "main", "reviewing a pull request")

	_, _, err := RecordCheckpoint(dir, here, SessionStart{})
	require.NoError(t, err)
	_, ok, err := RecordCheckpoint(dir, there, SessionStart{})
	require.NoError(t, err)
	require.True(t, ok)

	assert.ElementsMatch(t, []Checkpoint{here, there}, recorded(t, dir))
}

// A host hook fires every turn. Turns that moved nothing must not each cost a record.
func TestRecordCheckpointSkipsAnUnchangedRefile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := hostCheckpoint()

	_, ok, err := RecordCheckpoint(dir, c, SessionStart{})
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = RecordCheckpoint(dir, c, SessionStart{})
	require.NoError(t, err)
	assert.False(t, ok, "nothing about the work moved")
	assert.Len(t, recorded(t, dir), 1)
}

func TestRecordCheckpointSupersedesRatherThanRewrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := hostCheckpoint()
	second := first
	second.Tree.Revision = "cc76e76ef1e2d3c4b5a6978869574635241302ff"
	second.Note = "test design skill committed"

	_, _, err := RecordCheckpoint(dir, first, SessionStart{})
	require.NoError(t, err)
	_, ok, err := RecordCheckpoint(dir, second, SessionStart{})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []Checkpoint{second}, recorded(t, dir), "one line of work, at its newest")

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	kept := 0
	for _, rec := range fold.Records {
		if rec.Kind == KindCheckpoint {
			kept++
		}
	}
	assert.Equal(t, 2, kept, "the superseded checkpoint is still on disk; the store is append-only")
}

// Every checkpoint on one line of work shares a session file, or a hook firing each turn
// buries the run history it sits above.
func TestRecordCheckpointAppendsToOneSessionFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := hostCheckpoint()

	for i, rev := range []string{"aaaa111122223333", "bbbb444455556666", "cccc777788889999"} {
		c.Tree.Revision = rev
		_, ok, err := RecordCheckpoint(dir, c, SessionStart{})
		require.NoError(t, err)
		require.True(t, ok, "revision %d moved", i)
	}

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, fold.Sessions, "three checkpoints, one session")
	assert.Len(t, Summarize(fold), 1)
}

func TestLatestCheckpointsReportOneThreadPerHostSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	codex := hostCheckpoint()
	claude := Checkpoint{Host: "claude", HostSession: "54023016-5d8b", Workspace: "/repo", Tree: types.VCSCheckpoint{Revision: "62660968c", Branch: "main"}}

	_, _, err := RecordCheckpoint(dir, codex, SessionStart{})
	require.NoError(t, err)
	_, _, err = RecordCheckpoint(dir, claude, SessionStart{})
	require.NoError(t, err)

	assert.ElementsMatch(t, []Checkpoint{codex, claude}, recorded(t, dir))
}

// The store is grow-only and every reader folds all of it, so one pasted transcript
// cannot become a cost every later read pays. The caller is told what was stored, not
// what it asked for.
func TestRecordCheckpointClampsANoteAndReportsTheStoredOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := personCheckpoint("/repo", "main", strings.Repeat("e", MaxMessageBytes+512))

	stored, _, err := RecordCheckpoint(dir, c, SessionStart{})
	require.NoError(t, err)

	want := strings.Repeat("e", MaxMessageBytes) + messageTruncated
	assert.Equal(t, want, stored.Note)
	assert.Equal(t, []Checkpoint{stored}, recorded(t, dir))
}

func TestLatestCheckpointsIsEmptyWithoutAny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{Command: "run build"})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	assert.Empty(t, recorded(t, dir))
}

// A detached HEAD has no branch to key on; two of them in one workspace are one line
// of work rather than an unbounded stream of them.
func TestCheckpointKeyFallsBackToTheWorkspace(t *testing.T) {
	t.Parallel()
	detached := Checkpoint{Workspace: "/repo", Tree: types.VCSCheckpoint{Revision: "deadbeef"}}
	other := Checkpoint{Workspace: "/other", Tree: types.VCSCheckpoint{Revision: "deadbeef"}}

	assert.Equal(t, checkpointKey(detached), checkpointKey(Checkpoint{Workspace: "/repo", Tree: types.VCSCheckpoint{Revision: "cafe"}}))
	assert.NotEqual(t, checkpointKey(detached), checkpointKey(other))
}
