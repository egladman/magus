package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/project"
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
	// branch is the review's branch. Empty, no provider is consulted and the tool reports no
	// threads, which is the ordinary state of most workspaces and what most tests exercise around.
	branch string
}

func (f *fakeDiffSrc) WorkingDiff(context.Context, []string) (string, error) { return f.patch, nil }

func (f *fakeDiffSrc) ReviewOrigin(context.Context) types.ReviewOrigin {
	return types.ReviewOrigin{Branch: f.branch}
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
	store := changeset.NewStore(t.TempDir())
	// The human's act: a console fetch is what creates the session an agent may join.
	store.Attach("/w", "working", types.Diff{Base: "working"}, changeset.PatchDigest(src.patch))
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

	// A file with no changes at all, the exact mistake a stale session invited.
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

	sess := resp.Data.(*types.DiffReview)
	require.Len(t, sess.Comments, 1)
	// The id, the stamped origin, the hunk and the anchor are computed, so they are copied across.
	c := sess.Comments[0]
	assert.Equal(t, types.DiffComment{
		ID:        c.ID,
		Path:      "a.go",
		Hunk:      c.Hunk,
		Author:    types.DiffAuthorAgent, // stamped from the transport
		Origin:    c.Origin,
		AgentName: "Eli Gladman (human)", // the label is kept, as attribution only
		Body:      "hi",
		Anchor:    c.Anchor,
		Rung:      c.Rung,
	}, c)
}

// The projection parameter is additive: a caller that never sends it, sends it empty, or
// sends "full" must see byte-identical output to what op=state has always returned: the
// serialized session, patch, and hunks, with nothing narrowed.
func TestProjectionFullMatchesTheOriginalStateResponse(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	// Built the same way op=state has always built its answer, independent of
	// projectDiffState, the reference every case below is pinned against.
	sess := tool.sessions.Get(tool.root)
	want, err := json.Marshal(diffState{DiffReview: sess, Patch: agentPatch, Hunks: changeset.ParseHunks(agentPatch)})
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
// (since a projection struct simply has no field for what it does not) absence of
// everything else, checked on the serialized wire rather than trusted from the Go type alone.
func TestProjectionsIncludeAndExcludeFields(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	// Force one recompute so the annotated changeset actually carries a file: the fixture's
	// initial Attach carries an empty Diff, and Counts.Files would be a misleading 0 otherwise.
	tool.sessions.Attach(tool.root, "working", types.Diff{Base: "working"}, "stale")
	_, err := invoke(t, tool, map[string]any{"op": "state"})
	require.NoError(t, err)

	hunks := changeset.ParseHunks(agentPatch)
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

// The summary projection keeps Recomputed (the one bit that says an answer was freshly
// computed rather than replayed) even though it drops everything else state adds.
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

// projectDiffState only shapes op=state by design: a writing op keeps returning the full
// session no matter what this parameter is set to.
func TestProjectionIsIgnoredByWritingOps(t *testing.T) {
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch})

	resp, err := invoke(t, tool, map[string]any{
		"op": "comment", "path": "a.go", "body": "hi", "projection": "summary",
	})
	require.NoError(t, err)
	_, ok := resp.Data.(*types.DiffReview)
	assert.True(t, ok, "comment always returns the full session regardless of projection")
}

// An agent's words must never become a REPLY to a person.
//
// This is the sharpest form of "agents draft, humans send": a remark on a hunk is addressed to
// whoever reads the review, but a reply is addressed to the colleague who asked, by name, and
// receiving a wall of generated text where you asked a question is how the human half of a review
// dies. The protection is structural rather than advisory (there is simply no agent-reachable op
// that produces one), and this pins it, because the failure mode of a missing test here is a
// future op named "reply" that nobody notices has crossed the line.
//
// publish is refused for the same reason it is refused everywhere else: an agent cannot make
// anything leave the machine.
//
// The same holds for the person's own acts. Reading is a claim only the reader can make, so no op
// marks a hunk read or moves the cursor, and the one thing an agent may leave about a
// conversation, an outline, is bounded to a few short topics and refused past that: a paragraph
// there would be a reply wearing another name.
func TestNoAgentReachableOpSpeaksToAPerson(t *testing.T) {
	withReviewThreads(t, []any{
		map[string]any{"id": "t1", "path": "a.go", "line": float64(1), "author": "priya", "body": "why?"},
	})
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch, branch: "feat/x"})

	for _, op := range []string{"reply", "publish", "approve", "viewed", "read", "mark", "ack", "seen", "cursor", "reading"} {
		_, err := invoke(t, tool, map[string]any{"op": op, "id": "t1", "thread": "t1", "body": "on it"})
		require.Error(t, err, "op %q must not be reachable from the MCP tools", op)
	}

	sess := tool.sessions.Get(tool.root)
	assert.Empty(t, sess.Viewed, "no op marks a hunk read")
	assert.Equal(t, types.DiffCursor{Hunk: -1}, sess.Cursor, "no op moves the cursor")

	long := strings.Repeat("a reply is what a person writes, ", 4)
	_, err := invoke(t, tool, map[string]any{"op": "outline", "thread": "t1", "topics": []any{long}})
	require.Error(t, err, "an over-long outline is a reply, and is refused")
	assert.Contains(t, err.Error(), "the person types the reply")
	assert.Empty(t, tool.sessions.Get(tool.root).Outlines, "and nothing of it is held")
}

// withReviewThreads wires a review provider whose review holds threads, so the tool's forge reads
// have something to answer with.
func withReviewThreads(t *testing.T, threads []any) {
	t.Helper()
	name := "fake-mcp-review-" + t.Name()
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			switch req.Target {
			case spells.FindReviewContract:
				return map[string]any{"id": "482", "repo": "acme/acme"}, nil
			case spells.ReviewThreadsContract:
				return threads, nil
			default:
				return nil, nil
			}
		})))
	prev := bindings.ReviewProvider()
	bindings.SetReviewProvider(name)
	t.Cleanup(func() { bindings.SetReviewProvider(prev) })
}

var briefThreads = []any{
	map[string]any{"id": "t1", "path": "a.go", "line": float64(9), "author": "priya", "body": "why added?"},
	map[string]any{"id": "t2", "root": "t1", "author": "marcus", "body": "for the cache"},
}

// TestProjectionThreadReturnsTheBriefAndNoSession. The brief is the whole conversation and the
// hunk it sits in, and it is not the session: none of the session's bodies come with it.
func TestProjectionThreadReturnsTheBriefAndNoSession(t *testing.T) {
	withReviewThreads(t, briefThreads)
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch, branch: "feat/x"})

	resp, err := invoke(t, tool, map[string]any{"op": "state", "projection": "thread", "thread": "t2"})
	require.NoError(t, err)

	reply, ok := resp.Data.(review.ThreadBriefReply)
	require.True(t, ok, "projection=thread returns {id, brief}, got %T", resp.Data)
	assert.Equal(t, "t1", reply.ID, "a reply's id answers with its conversation's root")
	assert.Contains(t, reply.Brief, "- priya:\n  > why added?\n- marcus:\n  > for the cache")
	assert.Contains(t, reply.Brief, "- where: a.go:9")
	assert.Contains(t, reply.Brief, "+added")
	assert.Contains(t, reply.Brief, "I will type the reply")
	assert.Contains(t, reply.Brief, "note anchors: this server has no notes store wired")
}

func TestProjectionThreadRefusesWhatNamesNoConversation(t *testing.T) {
	withReviewThreads(t, briefThreads)
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch, branch: "feat/x"})

	_, err := invoke(t, tool, map[string]any{"op": "state", "projection": "thread"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs thread")

	_, err = invoke(t, tool, map[string]any{"op": "state", "projection": "thread", "thread": "t404"})
	require.ErrorIs(t, err, review.ErrNoConversation)
	assert.Contains(t, err.Error(), "op=state's threads")
}

// TestOutlineIsHeldForThePersonKeyedByTheRoot. An outline sent against a reply lands on the
// conversation, a second one replaces the first, and the session shows it to the person.
func TestOutlineIsHeldForThePersonKeyedByTheRoot(t *testing.T) {
	withReviewThreads(t, briefThreads)
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch, branch: "feat/x"})

	_, err := invoke(t, tool, map[string]any{
		"op": "outline", "thread": "t2", "topics": []any{"is the cache write racy?", "who calls Put"},
		"agent_name": "scout",
	})
	require.NoError(t, err)
	resp, err := invoke(t, tool, map[string]any{
		"op": "outline", "thread": "t1", "topics": []any{"replaces the first"},
	})
	require.NoError(t, err)

	sess := resp.Data.(*types.DiffReview)
	assert.Equal(t, []types.DiffOutline{{Thread: "t1", Topics: []string{"replaces the first"}}}, sess.Outlines)
	assert.Equal(t, sess.Outlines, tool.sessions.Get(tool.root).Outlines)
}

func TestOutlineRefusesWhatIsNotAPointer(t *testing.T) {
	withReviewThreads(t, briefThreads)
	tool := newDiffTool(t, &fakeDiffSrc{patch: agentPatch, branch: "feat/x"})

	cases := map[string]map[string]any{
		"no thread":    {"op": "outline", "topics": []any{"a"}},
		"no topics":    {"op": "outline", "thread": "t1"},
		"six topics":   {"op": "outline", "thread": "t1", "topics": []any{"a", "b", "c", "d", "e", "f"}},
		"a newline":    {"op": "outline", "thread": "t1", "topics": []any{"one\ntwo"}},
		"not strings":  {"op": "outline", "thread": "t1", "topics": []any{"a", float64(2)}},
		"not a list":   {"op": "outline", "thread": "t1", "topics": "a"},
		"unknown id":   {"op": "outline", "thread": "t404", "topics": []any{"a"}},
		"too long one": {"op": "outline", "thread": "t1", "topics": []any{strings.Repeat("x", types.DiffOutlineTopicRunes+1)}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := invoke(t, tool, params)
			require.Error(t, err)
		})
	}
	assert.Empty(t, tool.sessions.Get(tool.root).Outlines)
}
