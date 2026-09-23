package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
)

// The GitHub provider's own `test` blocks, run on the same VM surface the queue gives it.
func TestGitHubProviderBuzzTests(t *testing.T) {
	ctx := context.Background()
	sess, err := newSession(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, sess.Exec(ctx, githubSource))
	tests := sess.Tests()
	require.NotEmpty(t, tests)
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := sess.CallValue(ctx, tc.Fn, nil)
			require.NoError(t, err)
		})
	}
}

func TestTheGitHubProviderOpensByName(t *testing.T) {
	p, err := Open(context.Background(), "github")
	require.NoError(t, err)
	require.NoError(t, p.Close())
}

var (
	headA = strings.Repeat("a", 40)
	headD = strings.Repeat("d", 40)
)

// script is a provider whose ops echo what they were handed, so a test reads the
// bridge's encoding from the answers.
var script = `
import "std";

export fun list_changes(io: {str: any}) > [any] {
    return [{
        "id": "7", "repo": "acme/acme", "head": "` + headA + `", "ref": "refs/pull/7/head",
        "branch": "feat", "base": "{io["base"]}", "title": "{io["remote"]}", "author": "priya", "fork": true,
    }];
}

export fun approval_at(io: {str: any}) > any {
    return {"approved": io["id"] == "7", "head": io["commit"], "required": 1, "reason": "{io["title"]}"};
}

export fun post_status(io: {str: any}) > bool {
    return io["context"] == "merge-queue" and io["state"] == "success";
}

export fun merge_change(io: {str: any}) > any {
    return {"merged": io["message"] == "* body", "reason": "head moved"};
}

export fun kick_back(io: {str: any}) > bool {
    return io["report"] == "report";
}
`

func open(t *testing.T, src string) *Script {
	t.Helper()
	p, err := New(context.Background(), "echo", src)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

var change = mergequeue.Change{ID: "7", Repo: "acme/acme", Head: headA, Base: "main", Title: "add x"}

func TestListChangesDecodesEveryField(t *testing.T) {
	got, err := open(t, script).ListChanges(context.Background(), mergequeue.ListQuery{Base: "main", Remote: "git@github.com:acme/acme.git"})
	require.NoError(t, err)
	assert.Equal(t, []mergequeue.Change{{
		ID: "7", Repo: "acme/acme", Head: headA, Ref: "refs/pull/7/head", Branch: "feat",
		Base: "main", Title: "git@github.com:acme/acme.git", Author: "priya", Fork: true,
	}}, got)
}

func TestListChangesRefusesWhatGitCouldReadAsAnOption(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{`"head": "` + headA + `", `, "", `head "" is not a full commit id`},
		{`"id": "7"`, `"id": ".."`, `change id ".."`},
		{`"branch": "feat"`, `"branch": "--upload-pack=x"`, `starts with '-'`},
		{`"ref": "refs/pull/7/head"`, `"ref": "refs/pull/7/head:refs/heads/main"`, `holds ':'`},
	} {
		_, err := open(t, strings.Replace(script, tc.from, tc.to, 1)).ListChanges(context.Background(), mergequeue.ListQuery{Base: "main"})
		require.ErrorContains(t, err, tc.want, tc.to)
	}
}

func TestApprovalDecodesAtTheCommitAsked(t *testing.T) {
	got, err := open(t, script).ApprovalAt(context.Background(), change, headD)
	require.NoError(t, err)
	assert.Equal(t, mergequeue.Approval{Approved: true, Head: headD, Reason: "add x"}, got)
}

func TestApprovalWithoutAHeadIsAnError(t *testing.T) {
	p := open(t, strings.Replace(script, `"head": io["commit"], `, "", 1))
	_, err := p.ApprovalAt(context.Background(), change, headD)
	require.ErrorContains(t, err, `approval_at: head: #7 (add x): head "" is not a full commit id`)
}

func TestApprovalOfTheWrongTypeNamesTheField(t *testing.T) {
	p := open(t, strings.Replace(script, `"approved": io["id"] == "7"`, `"approved": "yes"`, 1))
	_, err := p.ApprovalAt(context.Background(), change, headA)
	require.ErrorContains(t, err, `field "approved" is string, want bool`)
}

func TestWritesCarryTheirParametersAndARefusalIsAnError(t *testing.T) {
	p, ctx := open(t, script), context.Background()
	require.NoError(t, p.PostStatus(ctx, change, headA, mergequeue.CommitStatus{Context: "merge-queue", State: mergequeue.StateSuccess}))
	require.ErrorContains(t, p.PostStatus(ctx, change, headA, mergequeue.CommitStatus{Context: "other", State: mergequeue.StateSuccess}), "the host refused")
	require.NoError(t, p.MergeChange(ctx, change, headA, "* body"))
	require.ErrorContains(t, p.MergeChange(ctx, change, headA, "* other"), "not merged: head moved")
	require.NoError(t, p.KickBack(ctx, change, headA, "report"))
	require.ErrorContains(t, p.KickBack(ctx, change, headA, "other"), "the host refused")
}

func TestAScriptMissingAnOpIsRefused(t *testing.T) {
	_, err := New(context.Background(), "half", `export fun list_changes(io: {str: any}) > [any] { return []; }`)
	require.EqualError(t, err, `provider "half" does not export approval_at, post_status, merge_change, kick_back`)
}

func TestAnUnknownProviderNamesBothPlacesItLooked(t *testing.T) {
	_, err := Open(context.Background(), "gitlab")
	require.ErrorContains(t, err, `provider "gitlab": not built in`)
}

func TestTheBridgeRefusesAValueItDoesNotPass(t *testing.T) {
	_, err := toValue(map[string]any{"n": 3})
	require.EqualError(t, err, `field "n" is int, which the bridge does not pass`)
}
