package sessions

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codexHandoff() Handoff {
	return Handoff{
		Host:       "codex",
		Session:    "01a07dab-240a-75b2-b9fe-816562939726",
		Event:      "Stop",
		Transcript: "/Users/e/.codex/sessions/2026/09/07/rollout.jsonl",
		Workspace:  "/repo",
		At: types.VCSCheckpoint{
			Revision: "bcac8934e1a2b3c4d5e6f708192a3b4c5d6e7f80",
			Branch:   "handoff",
			Dirty:    true,
			VCS:      "git",
		},
		Note: "usage limit hit mid-implementation",
	}
}

func TestRecordHandoffRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := codexHandoff()

	recorded, err := RecordHandoff(dir, want, SessionStart{Workspace: "/repo"})
	require.NoError(t, err)
	assert.True(t, recorded)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, []Handoff{want}, Handoffs(fold))
}

// A host hook fires every turn. Turns that moved nothing must not each cost a record.
func TestRecordHandoffSkipsAnUnchangedRefile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := codexHandoff()

	recorded, err := RecordHandoff(dir, h, SessionStart{})
	require.NoError(t, err)
	require.True(t, recorded)

	recorded, err = RecordHandoff(dir, h, SessionStart{})
	require.NoError(t, err)
	assert.False(t, recorded, "nothing about the session moved")

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Len(t, Handoffs(fold), 1)
}

func TestRecordHandoffSupersedesRatherThanRewrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := codexHandoff()
	second := first
	second.At.Revision = "cc76e76ef1e2d3c4b5a6978869574635241302ff"
	second.Note = "test design skill committed"

	_, err := RecordHandoff(dir, first, SessionStart{})
	require.NoError(t, err)
	recorded, err := RecordHandoff(dir, second, SessionStart{})
	require.NoError(t, err)
	assert.True(t, recorded)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, []Handoff{second}, Handoffs(fold), "one open thread per host session, at its newest")

	kinds := 0
	for _, rec := range fold.Records {
		if rec.Kind == KindHandoff {
			kinds++
		}
	}
	assert.Equal(t, 2, kinds, "the superseded handoff is still on disk; the store is append-only")
}

// The read side is what an arriving session consults, and two hosts working one
// repository are the case it exists for.
func TestHandoffsReportOneThreadPerHostSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	codex := codexHandoff()
	claude := Handoff{Host: "claude", Session: "54023016-5d8b", Workspace: "/repo", At: types.VCSCheckpoint{Revision: "62660968c", Branch: "main"}}

	_, err := RecordHandoff(dir, codex, SessionStart{})
	require.NoError(t, err)
	_, err = RecordHandoff(dir, claude, SessionStart{})
	require.NoError(t, err)

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []Handoff{codex, claude}, Handoffs(fold))
}

func TestHandoffsIsEmptyWithoutAny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := Open(dir, "sess1", SessionStart{Command: "run build"})
	require.NoError(t, err)
	require.NoError(t, w.Append(KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Empty(t, Handoffs(fold))
}
