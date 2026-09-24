package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
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

func TestTheGitHubProviderOpensByNameAndListsArtifacts(t *testing.T) {
	p, err := Open(context.Background(), "github")
	require.NoError(t, err)
	assert.True(t, p.ListsArtifacts())
	require.NoError(t, p.Close())
}

var (
	headA = strings.Repeat("a", 40)
	headD = strings.Repeat("d", 40)
	headE = strings.Repeat("e", 40)
	headF = strings.Repeat("f", 40)
)

// script is a provider whose ops echo what they were handed, so a test reads the
// bridge's encoding from the answers.
var script = `
import "std";
import "serialize";

export fun describe(io: {str: any}) > any {
    return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash", "{io["base"]}"], "queue_label": "queue: ",
        "committer": {"name": "bot", "email": "bot@example.com"}};
}

export fun list_changes(io: {str: any}) > any {
    return {
        "changes": [{
            "id": "7", "repo": "acme/acme", "head": "` + headA + `", "ref": "refs/pull/7/head",
            "branch": "feat", "base": "{io["base"]}", "title": "{io["remote_url"]}", "fork": true,
            "method": "squash", "parent": "6",
        }],
        "merged": [{"id": "6", "head": "` + headD + `", "commit": "` + headE + `", "method": "squash"}],
        "unqueued": [{"id": "9", "head": "` + headF + `"}],
    };
}

export fun approval_at(io: {str: any}) > any {
    return {"approved": io["id"] == "7", "head": io["commit"], "reason": "{io["title"]}",
        "base": "main", "method": io["method"], "approved_commit": "` + headD + `",
        "queued": true, "shared_with": ["40"]};
}

export fun post_status(io: {str: any}) > bool {
    return io["context"] == "merge-queue" and io["state"] == "success";
}

export fun retarget(io: {str: any}) > bool {
    return io["base"] == "main";
}

export fun merge_change(io: {str: any}) > any {
    final through = serialize\Boxed.init(io["through"]).listValue();
    final pinned = through.len() == 1 and through[0].q("id").stringValue() == "5" and through[0].q("commit").stringValue() == "` + headE + `";
    return {"merged": io["message"] == "* body" and pinned, "reason": "head moved"};
}

export fun kick_back(io: {str: any}) > bool {
    final paths = io["paths"] ?? [<str>];
    return io["report"] == "report" and io["code"] == "KICK_CONFLICT" and "{paths}" == "{["a.go", "b.go"]}" and io["candidate_commit"] == "c";
}

export fun list_artifacts(io: {str: any}) > any {
    return {"complete": io["source"] == "done", "headers": {"Authorization": "Bearer tok"},
        "artifacts": [{"name": "mergequeue-plan", "url": "https://example.invalid/1.zip"}]};
}
`

func open(t *testing.T, src string) *Script {
	t.Helper()
	p, err := newScript(context.Background(), "echo", src)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

var change = types.Change{ID: "7", Repo: "acme/acme", Head: headA, Base: "main", Title: "add x", Method: types.MethodSquash}

func TestDescribeDecodesWhatTheProviderSupports(t *testing.T) {
	got, err := open(t, script).Describe(context.Background(), types.ListQuery{Base: "merge"})
	require.NoError(t, err)
	assert.Equal(t, types.Capabilities{StackMerge: types.StackMergeAtomic, LinearStacks: true,
		Methods: []types.MergeMethod{types.MethodSquash, types.MethodMerge}, QueueLabel: "queue: ",
		Committer: magustypes.Person{Name: "bot", Email: "bot@example.com"}}, got)
}

func TestListChangesDecodesEveryFieldAndTheMergedAndUnqueuedChanges(t *testing.T) {
	got, err := open(t, script).ListChanges(context.Background(), types.ListQuery{Base: "main", RemoteURL: "git@github.com:acme/acme.git"})
	require.NoError(t, err)
	assert.Equal(t, types.Changes{
		Schema: types.SchemaChanges, Base: "main", RemoteURL: "git@github.com:acme/acme.git",
		Changes: []types.Change{{
			ID: "7", Repo: "acme/acme", Head: headA, Ref: "refs/pull/7/head", Branch: "feat",
			Base: "main", Title: "git@github.com:acme/acme.git", Fork: true,
			Method: types.MethodSquash, Parent: "6",
		}},
		Merged:   []types.MergedChange{{ID: "6", Head: headD, Commit: headE, Method: types.MethodSquash}},
		Unqueued: []types.UnqueuedChange{{ID: "9", Head: headF}},
	}, got)
}

func TestListChangesRefusesWhatGitCouldReadAsAnOption(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{`"head": "` + headA + `", `, `"head": "", `, `head "" is not a full commit id`},
		{`"id": "7"`, `"id": ".."`, `change id ".."`},
		{`"branch": "feat"`, `"branch": "--upload-pack=x"`, `starts with '-'`},
		{`"ref": "refs/pull/7/head"`, `"ref": "refs/pull/7/head:refs/heads/main"`, `holds ':'`},
		{`"method": "squash", "parent"`, `"method": "", "parent"`, `merge method ""`},
		{`"commit": "` + headE + `"`, `"commit": "e"`, `must be full commit ids`},
	} {
		_, err := open(t, strings.Replace(script, tc.from, tc.to, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
		require.ErrorContains(t, err, tc.want, tc.to)
	}
}

// A field a record leaves out is an error, never its zero value. Before, a change
// record without "fork" read as a change from this repository.
func TestAMissingRequiredFieldIsAnError(t *testing.T) {
	for _, tc := range []struct{ from, want string }{
		{`, "fork": true`, `list_changes: changes[0]: field "fork" is missing`},
		{`"repo": "acme/acme", `, `list_changes: changes[0]: field "repo" is missing`},
		{`"base": "{io["base"]}", `, `list_changes: changes[0]: field "base" is missing`},
		{`"unqueued": [{"id": "9", "head": "` + headF + `"}],`, `list_changes: field "unqueued" is missing`},
		{`, "method": "squash"}]`, `list_changes: merged[0]: field "method" is missing`},
	} {
		to := ""
		if strings.HasSuffix(tc.from, "}]") {
			to = "}]"
		}
		_, err := open(t, strings.Replace(script, tc.from, to, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
		require.ErrorContains(t, err, tc.want, tc.from)
	}
	for _, tc := range []struct{ from, want string }{
		{`"queued": true, `, `field "queued" is missing`},
		{`"shared_with": ["40"]`, `field "shared_with" is missing`},
		{`"base": "main", `, `field "base" is missing`},
	} {
		_, err := open(t, strings.Replace(script, tc.from, "", 1)).ApprovalAt(context.Background(), change, headD)
		require.ErrorContains(t, err, tc.want, tc.from)
	}
	_, err := open(t, strings.Replace(script, `"linear_stacks": true, `, "", 1)).Describe(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `field "linear_stacks" is missing`)
}

func TestApprovalDecodesAtTheCommitAsked(t *testing.T) {
	got, err := open(t, script).ApprovalAt(context.Background(), change, headD)
	require.NoError(t, err)
	assert.Equal(t, types.Approval{Approved: true, Head: headD, Reason: "add x", Base: "main",
		Method: types.MethodSquash, ApprovedCommit: headD, Queued: true, BranchSharedWith: []string{"40"}}, got)
}

func TestApprovalWithoutAHeadIsAnError(t *testing.T) {
	p := open(t, strings.Replace(script, `"head": io["commit"], `, `"head": "", `, 1))
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
	require.NoError(t, p.PostStatus(ctx, change, headA, types.CommitStatus{Context: "merge-queue", State: types.StateSuccess}))
	require.ErrorContains(t, p.PostStatus(ctx, change, headA, types.CommitStatus{Context: "other", State: types.StateSuccess}), "provider refused")
	require.NoError(t, p.Retarget(ctx, change, "main"))
	require.ErrorContains(t, p.Retarget(ctx, change, "dev"), "provider refused")
	require.NoError(t, p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* body",
		Through: []types.PinnedChange{{ID: "5", Commit: headE}}}))
	require.ErrorContains(t, p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* other"}), "not merged: head moved")
	kick := types.Kick{Code: types.CodeKickConflict, Report: "report", Paths: []string{"a.go", "b.go"}, CandidateCommit: "c"}
	require.NoError(t, p.KickBack(ctx, change, headA, kick))
	kick.Paths = nil
	require.ErrorContains(t, p.KickBack(ctx, change, headA, kick), "provider refused")
}

func TestListArtifactsDecodesTheListing(t *testing.T) {
	got, err := open(t, script).ListArtifacts(context.Background(), "done")
	require.NoError(t, err)
	assert.Equal(t, types.ArtifactListing{Complete: true, Headers: map[string]string{"Authorization": "Bearer tok"},
		Artifacts: []types.Artifact{{Name: "mergequeue-plan", URL: "https://example.invalid/1.zip"}}}, got)
}

func TestAScriptMissingAnOpIsRefusedAndListArtifactsIsOptional(t *testing.T) {
	_, err := newScript(context.Background(), "half", `export fun list_changes(io: {str: any}) > any { return {}; }`)
	require.EqualError(t, err, `provider "half" does not export describe, approval_at, post_status, retarget, merge_change, kick_back`)

	noRuns := open(t, strings.Split(script, "export fun list_artifacts")[0])
	assert.False(t, noRuns.ListsArtifacts())
	_, err = noRuns.ListArtifacts(context.Background(), "done")
	require.EqualError(t, err, `provider "echo" does not export list_artifacts`)
}

func TestAnUnknownProviderNamesBothPlacesItLooked(t *testing.T) {
	_, err := Open(context.Background(), "gitlab")
	require.ErrorContains(t, err, `provider "gitlab": not built in`)
}

func TestTheBridgeRefusesAValueItDoesNotPass(t *testing.T) {
	_, err := toValue(map[string]any{"n": 3})
	require.EqualError(t, err, `field "n" is int, which the bridge does not pass`)
}
