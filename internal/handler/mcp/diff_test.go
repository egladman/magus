package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/json"
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

// No branch and no remote, so no provider is consulted and the tool reports no threads. That
// is the ordinary state of most workspaces and is what these tests exercise around.
func (f *fakeDiffSrc) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{}
}

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

// What makes every other capability usable: comment and suggest take a 0-based hunk index,
// and an op=state that did not show one would leave the coordinate to be guessed.
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

// The projection parameter is additive: a caller that never sends it, sends it empty, or
// sends "full" must see byte-identical output to what op=state has always returned - the
// serialized session, patch, and hunks, with nothing narrowed.
func TestProjectionFullMatchesTheOriginalStateResponse(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	// Built the same way op=state has always built its answer, independent of
	// projectDiffState - the reference every case below is pinned against.
	sess := tool.sessions.Get(tool.root)
	want, err := json.Marshal(diffState{DiffSession: sess, Patch: agentPatch, Hunks: diff.ParseHunks(agentPatch)})
	require.NoError(t, err)

	cases := []struct {
		name   string
		params map[string]any
	}{
		{"projection absent", map[string]any{"op": "state"}},
		{"projection empty", map[string]any{"op": "state", "projection": ""}},
		{"projection full", map[string]any{"op": "state", "projection": "full"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := invoke(t, tool, tc.params)
			require.NoError(t, err)
			got, err := json.Marshal(resp.Data)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got))
		})
	}
}

// Each projection keeps only the fields it promises: presence of what it claims to carry, and
// - since a projection struct simply has no field for what it does not - absence of
// everything else, checked on the serialized wire rather than trusted from the Go type alone.
func TestProjectionsIncludeAndExcludeFields(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	// Force one recompute so the annotated changeset actually carries a file: the fixture's
	// initial Attach carries an empty Diff, and Counts.Files would be a misleading 0 otherwise.
	tool.sessions.Attach(tool.root, "working", types.Diff{Base: "working"}, "stale")
	_, err := invoke(t, tool, map[string]any{"op": "state"})
	require.NoError(t, err)

	hunks := diff.ParseHunks(agentPatch)
	tool.sessions.MarkViewed(tool.root, hunks[0].Hunks[0].Digest, true)
	_, err = invoke(t, tool, map[string]any{"op": "comment", "path": "a.go", "body": "note"})
	require.NoError(t, err)
	_, err = invoke(t, tool, map[string]any{"op": "suggest", "path": "a.go", "reason": "look here"})
	require.NoError(t, err)

	cases := []struct {
		projection string
		present    []string
		absent     []string
	}{
		{
			projection: "summary",
			present:    []string{`"id":`, `"base":`, `"cursor":`, `"counts":`, `"files":1`, `"hunks":2`, `"comments":1`, `"suggestions":1`, `"viewed":1`},
			absent:     []string{`"diff":`, `"patch":`, `"hunks":[`, `"comments":[`, `"suggestions":[`, `"viewed":[`},
		},
		{
			projection: "conversation",
			present:    []string{`"cursor":`, `"viewed":`, `"comments":`, `"suggestions":`},
			absent:     []string{`"diff":`, `"patch":`, `"hunks":`, `"counts":`},
		},
		{
			projection: "patch",
			present:    []string{`"id":`, `"base":`, `"patch":`, `"hunks":`},
			absent:     []string{`"diff":`, `"comments":`, `"suggestions":`, `"viewed":`, `"counts":`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.projection, func(t *testing.T) {
			resp, err := invoke(t, tool, map[string]any{"op": "state", "projection": tc.projection})
			require.NoError(t, err)
			got, err := json.Marshal(resp.Data)
			require.NoError(t, err)
			for _, want := range tc.present {
				assert.Contains(t, string(got), want)
			}
			for _, notWant := range tc.absent {
				assert.NotContains(t, string(got), notWant)
			}
		})
	}
}

// The summary projection keeps Recomputed - the one bit that says an answer was freshly
// computed rather than replayed - even though it drops everything else state adds.
func TestProjectionSummaryCarriesRecomputedThrough(t *testing.T) {
	src := &fakeDiffSrc{patch: agentPatch}
	tool := newDiffTool(t, src)

	src.patch = "diff --git a/b.go b/b.go\n@@ -1 +1 @@\n-x\n+y\n"
	resp, err := invoke(t, tool, map[string]any{"op": "state", "projection": "summary"})
	require.NoError(t, err)

	sum, ok := resp.Data.(diffSummary)
	require.True(t, ok)
	assert.True(t, sum.Recomputed, "projection carries Recomputed through")
	assert.Equal(t, 1, sum.Counts.Files, "the recomputed changeset, not the stale one, is counted")
}

func TestProjectionRejectsAnUnknownValue(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	_, err := invoke(t, tool, map[string]any{"op": "state", "projection": "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown projection")
	assert.Contains(t, err.Error(), `"bogus"`)
	assert.Contains(t, err.Error(), "summary")
}

// projectDiffState only shapes op=state by design - a writing op keeps returning the full
// session no matter what this parameter is set to.
func TestProjectionIsIgnoredByWritingOps(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	resp, err := invoke(t, tool, map[string]any{
		"op": "comment", "path": "a.go", "body": "hi", "projection": "summary",
	})
	require.NoError(t, err)
	_, ok := resp.Data.(*types.DiffSession)
	assert.True(t, ok, "comment always returns the full session regardless of projection")
}

// An agent's words must never become a REPLY to a person.
//
// This is the sharpest form of "agents draft, humans send": a remark on a hunk is addressed to
// whoever reads the review, but a reply is addressed to the colleague who asked, by name, and
// receiving a wall of generated text where you asked a question is how the human half of a review
// dies. The protection is structural rather than advisory - there is simply no agent-reachable op
// that produces one - and this pins it, because the failure mode of a missing test here is a
// future op named "reply" that nobody notices has crossed the line.
//
// publish is refused for the same reason it is refused everywhere else: an agent cannot make
// anything leave the machine.
func TestNoAgentReachableOpSpeaksToAPerson(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})
	for _, op := range []string{"reply", "publish", "approve"} {
		_, err := invoke(t, tool, map[string]any{"op": op, "id": "t1", "body": "on it"})
		require.Error(t, err, "op %q must not be reachable from the agent surface", op)
	}
}
