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

func TestTheGitHubProviderOpensByNameAndReadsRuns(t *testing.T) {
	p, err := Open(context.Background(), "github")
	require.NoError(t, err)
	assert.True(t, p.ReadsRuns())
	require.NoError(t, p.Close())
}

var (
	headA = strings.Repeat("a", 40)
	headD = strings.Repeat("d", 40)
	headE = strings.Repeat("e", 40)
)

// script is a provider whose ops echo what they were handed, so a test reads the
// bridge's encoding from the answers.
var script = `
import "std";

export fun describe(io: {str: any}) > any {
    return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash", "{io["base"]}"]};
}

export fun list_changes(io: {str: any}) > any {
    return {
        "changes": [{
            "id": "7", "repo": "acme/acme", "head": "` + headA + `", "ref": "refs/pull/7/head",
            "branch": "feat", "base": "{io["base"]}", "title": "{io["remote"]}", "author": "priya", "fork": true,
            "method": "squash", "parent": "6",
        }],
        "landed": [{"id": "6", "head": "` + headD + `", "commit": "` + headE + `", "method": "squash"}],
    };
}

export fun approval_at(io: {str: any}) > any {
    return {"approved": io["id"] == "7", "head": io["commit"], "required": 1, "reason": "{io["title"]}",
        "base": "main", "method": io["method"], "approved_at": "` + headD + `"};
}

export fun post_status(io: {str: any}) > bool {
    return io["context"] == "merge-queue" and io["state"] == "success";
}

export fun retarget(io: {str: any}) > bool {
    return io["base"] == "main";
}

export fun merge_change(io: {str: any}) > any {
    return {"merged": io["message"] == "* body" and io["through"] == "5", "reason": "head moved"};
}

export fun kick_back(io: {str: any}) > bool {
    final paths = io["paths"] ?? [<str>];
    return io["report"] == "report" and io["code"] == "KICK_CONFLICT" and "{paths}" == "{["a.go", "b.go"]}";
}

export fun run_artifacts(io: {str: any}) > any {
    return {"completed": io["run"] == "done", "headers": {"Authorization": "Bearer tok"},
        "artifacts": [{"name": "magus-queue-plan", "url": "https://example.invalid/1.zip"}]};
}
`

func open(t *testing.T, src string) *Script {
	t.Helper()
	p, err := New(context.Background(), "echo", src)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

var change = mergequeue.Change{ID: "7", Repo: "acme/acme", Head: headA, Base: "main", Title: "add x", Method: mergequeue.MethodSquash}

func TestDescribeDecodesWhatTheProviderSupports(t *testing.T) {
	got, err := open(t, script).Describe(context.Background(), mergequeue.ListQuery{Base: "merge"})
	require.NoError(t, err)
	assert.Equal(t, mergequeue.Capabilities{StackMerge: mergequeue.StackMergeAtomic, LinearStacks: true,
		Methods: []mergequeue.MergeMethod{mergequeue.MethodSquash, mergequeue.MethodMerge}}, got)
}

func TestListChangesDecodesEveryFieldAndTheLandedChanges(t *testing.T) {
	got, err := open(t, script).ListChanges(context.Background(), mergequeue.ListQuery{Base: "main", Remote: "git@github.com:acme/acme.git"})
	require.NoError(t, err)
	assert.Equal(t, mergequeue.Changes{
		Schema: mergequeue.SchemaChanges, Base: "main", Remote: "git@github.com:acme/acme.git",
		Changes: []mergequeue.Change{{
			ID: "7", Repo: "acme/acme", Head: headA, Ref: "refs/pull/7/head", Branch: "feat",
			Base: "main", Title: "git@github.com:acme/acme.git", Author: "priya", Fork: true,
			Method: mergequeue.MethodSquash, Parent: "6",
		}},
		Landed: []mergequeue.Landed{{ID: "6", Head: headD, Commit: headE, Method: mergequeue.MethodSquash}},
	}, got)
}

func TestListChangesRefusesWhatGitCouldReadAsAnOption(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{`"head": "` + headA + `", `, "", `head "" is not a full commit id`},
		{`"id": "7"`, `"id": ".."`, `change id ".."`},
		{`"branch": "feat"`, `"branch": "--upload-pack=x"`, `starts with '-'`},
		{`"ref": "refs/pull/7/head"`, `"ref": "refs/pull/7/head:refs/heads/main"`, `holds ':'`},
		{`"method": "squash", "parent"`, `"method": "", "parent"`, `merge method ""`},
	} {
		_, err := open(t, strings.Replace(script, tc.from, tc.to, 1)).ListChanges(context.Background(), mergequeue.ListQuery{Base: "main"})
		require.ErrorContains(t, err, tc.want, tc.to)
	}
}

func TestApprovalDecodesAtTheCommitAsked(t *testing.T) {
	got, err := open(t, script).ApprovalAt(context.Background(), change, headD)
	require.NoError(t, err)
	assert.Equal(t, mergequeue.Approval{Approved: true, Head: headD, Reason: "add x", Base: "main",
		Method: mergequeue.MethodSquash, ApprovedAt: headD}, got)
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
	require.NoError(t, p.Retarget(ctx, change, "main"))
	require.ErrorContains(t, p.Retarget(ctx, change, "dev"), "the host refused")
	require.NoError(t, p.MergeChange(ctx, change, mergequeue.MergeRequest{Commit: headA, Message: "* body", Through: "5"}))
	require.ErrorContains(t, p.MergeChange(ctx, change, mergequeue.MergeRequest{Commit: headA, Message: "* other"}), "not merged: head moved")
	kick := mergequeue.Kick{Code: mergequeue.CodeConflict, Report: "report", Paths: []string{"a.go", "b.go"}}
	require.NoError(t, p.KickBack(ctx, change, headA, kick))
	kick.Paths = nil
	require.ErrorContains(t, p.KickBack(ctx, change, headA, kick), "the host refused")
}

func TestRunArtifactsDecodesTheListing(t *testing.T) {
	got, err := open(t, script).RunArtifacts(context.Background(), "done")
	require.NoError(t, err)
	assert.Equal(t, mergequeue.RunArtifacts{Completed: true, Headers: map[string]string{"Authorization": "Bearer tok"},
		Artifacts: []mergequeue.Artifact{{Name: "magus-queue-plan", URL: "https://example.invalid/1.zip"}}}, got)
}

func TestAScriptMissingAnOpIsRefusedAndRunArtifactsIsOptional(t *testing.T) {
	_, err := New(context.Background(), "half", `export fun list_changes(io: {str: any}) > any { return {}; }`)
	require.EqualError(t, err, `provider "half" does not export describe, approval_at, post_status, retarget, merge_change, kick_back`)

	noRuns := open(t, strings.Split(script, "export fun run_artifacts")[0])
	assert.False(t, noRuns.ReadsRuns())
	_, err = noRuns.RunArtifacts(context.Background(), "done")
	require.EqualError(t, err, `provider "echo" does not export run_artifacts`)
}

func TestAnUnknownProviderNamesBothPlacesItLooked(t *testing.T) {
	_, err := Open(context.Background(), "gitlab")
	require.ErrorContains(t, err, `provider "gitlab": not built in`)
}

func TestTheBridgeRefusesAValueItDoesNotPass(t *testing.T) {
	_, err := toValue(map[string]any{"n": 3})
	require.EqualError(t, err, `field "n" is int, which the bridge does not pass`)
}
