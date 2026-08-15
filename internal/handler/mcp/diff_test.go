package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const agentPatch = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,2 @@
-old
+new
@@ -9,1 +9,2 @@
 keep
+added
`

// fakeDiffSrc stands in for the workspace: it serves one patch and one annotated changeset.
type fakeDiffSrc struct {
	patch string
	calls int
}

func (f *fakeDiffSrc) WorkingDiff(context.Context, []string) (string, error) { return f.patch, nil }

func (f *fakeDiffSrc) Diff(_ context.Context, paths []string) (types.Diff, error) {
	f.calls++
	files := make([]types.DiffFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, types.DiffFile{Path: p, Role: types.DiffRoleSource})
	}
	return types.Diff{Base: "working", Files: files}, nil
}

func newDiffTool(t *testing.T, src *fakeDiffSrc) *diffTool {
	t.Helper()
	store := diff.NewStore(t.TempDir())
	// The human's act: a console fetch is what creates the session an agent may join.
	store.Attach("/w", "working", types.Diff{Base: "working"}, diff.PatchDigest(src.patch))
	return &diffTool{sessions: store, root: "/w", src: src}
}

func invoke(t *testing.T, tool *diffTool, params map[string]any) (spells.InvokeResponse, error) {
	t.Helper()
	return tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
}

// The gap that made every other capability unusable: comment and suggest take a 0-based hunk
// index that op=state never showed, so the coordinate had to be guessed.
func TestStateCarriesThePatchAndItsHunks(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	resp, err := invoke(t, tool, map[string]any{"op": "state"})
	require.NoError(t, err)

	st, ok := resp.Data.(diffState)
	require.True(t, ok, "op=state returns the session plus the change it describes")
	assert.Equal(t, agentPatch, st.Patch)
	require.Len(t, st.Hunks, 1)
	assert.Equal(t, "a.go", st.Hunks[0].Path)
	require.Len(t, st.Hunks[0].Hunks, 2, "both hunks are addressable")
	// The digests are the ones Viewed is keyed by, which is what makes "skip what they have
	// already seen" executable rather than merely promised.
	assert.NotEmpty(t, st.Hunks[0].Hunks[0].Digest)
	assert.NotEqual(t, st.Hunks[0].Hunks[0].Digest, st.Hunks[0].Hunks[1].Digest)
}

// An agent cannot see the tree, so a session frozen at whatever a browser last attached is
// served to exactly the party least able to notice.
func TestStateRecomputesWhenTheTreeHasMoved(t *testing.T) {
	src := &fakeDiffSrc{patch: agentPatch}
	tool := newDiffTool(t, src)

	// Same tree: replayed, not recomputed.
	resp, err := invoke(t, tool, map[string]any{"op": "state"})
	require.NoError(t, err)
	assert.False(t, resp.Data.(diffState).Recomputed)
	assert.Zero(t, src.calls)

	// The tree moves underneath the held session.
	src.patch = "diff --git a/b.go b/b.go\n@@ -1 +1 @@\n-x\n+y\n"
	resp, err = invoke(t, tool, map[string]any{"op": "state"})
	require.NoError(t, err)

	st := resp.Data.(diffState)
	assert.True(t, st.Recomputed, "the changeset is recomputed rather than replayed")
	assert.Equal(t, 1, src.calls)
	require.Len(t, st.Diff.Files, 1)
	assert.Equal(t, "b.go", st.Diff.Files[0].Path, "the session now describes the current tree")
}

func TestCommentRefusesACoordinateTheChangesetDoesNotHave(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	// A file with no changes at all - the exact mistake a stale session invited.
	_, err := invoke(t, tool, map[string]any{
		"op": "comment", "path": "not-in-the-change.go", "body": "looks wrong",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no changes in this diff")

	// An out-of-range hunk on a file that IS in the change.
	_, err = invoke(t, tool, map[string]any{
		"op": "comment", "path": "a.go", "hunk": float64(7), "body": "looks wrong",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has 2 hunk(s)")

	// Valid coordinates are accepted, including the file-level anchor.
	_, err = invoke(t, tool, map[string]any{
		"op": "comment", "path": "a.go", "hunk": float64(1), "body": "fine",
	})
	require.NoError(t, err)
	_, err = invoke(t, tool, map[string]any{"op": "comment", "path": "a.go", "body": "file-level"})
	require.NoError(t, err)
}

func TestSuggestValidatesTheSameCoordinate(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	_, err := invoke(t, tool, map[string]any{
		"op": "suggest", "path": "a.go", "hunk": float64(9), "reason": "worth a look",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

// The identity boundary is the claim that survived a hostile persona test; it must keep doing
// so now that comments carry an agent label.
func TestAgentNameIsRecordedButCannotClaimToBeTheHuman(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	resp, err := invoke(t, tool, map[string]any{
		"op": "comment", "path": "a.go", "body": "hi",
		"agent_name": "Eli Gladman (human)",
	})
	require.NoError(t, err)

	sess := resp.Data.(*types.DiffSession)
	require.Len(t, sess.Comments, 1)
	assert.Equal(t, types.DiffAuthorAgent, sess.Comments[0].Author, "author is stamped from the transport")
	assert.Equal(t, "Eli Gladman (human)", sess.Comments[0].AgentName, "the label is kept, as attribution only")
}
