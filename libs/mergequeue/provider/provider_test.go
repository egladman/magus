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
    return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash", "{io["base"]}"], "required_approvals": 2, "queue_label": "queue: ",
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
        "unqueued": [{"id": "9", "head": "` + headF + `", "repo": "acme/acme", "mark": "queued"}],
    };
}

export fun approval_at(io: {str: any}) > any {
    return {"approved": io["id"] == "7", "head": io["commit"], "reason": "{io["title"]}",
        "base": "main", "method": io["method"], "approved_commit": "` + headD + `",
        "queued": true, "shared_with": ["40"]};
}

export fun list_green(io: {str: any}) > any {
    if (io["context"] != "merge-queue") {
        return {"changes": [<any>]};
    }
    return {"changes": [{"id": "{io["base"]}", "repo": "{io["remote_url"]}", "head": "` + headD + `"}]};
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
    return {"merged": io["message"] == "* body" and (pinned or through.len() == 0), "by_provider": through.len() == 0, "reason": "head moved"};
}

export fun kick_back(io: {str: any}) > bool {
    final paths = io["paths"] ?? [<str>];
    final repro = serialize\Boxed.init(io["reproduce"]);
    return io["report"] == "report" and io["code"] == "KICK_CONFLICT" and "{paths}" == "{["a.go", "b.go"]}" and io["candidate_commit"] == "c"
        and io["source"] == "o/r/runs/7" and repro.q("gate").stringValue() == "make test" and repro.q("regenerate").stringValue() == ""
        and io["flag"] == "";
}

export fun mark(io: {str: any}) > bool {
    return io["id"] == "7" and io["repo"] == "acme/acme" and io["mark"] != "rejected";
}

export fun flag(io: {str: any}) > bool {
    return io["id"] == "7" and io["repo"] == "acme/acme" and io["flag"] == "changes_generator" and io["on"] == true;
}

export fun list_artifacts(io: {str: any}) > any {
    final run = {"repo": "o/r", "head_repo": "o/r", "head_branch": "main", "event": "push", "branch_event": true, "definition": "q.yaml"};
    if (io["source"] == "no run") {
        return {"complete": true, "artifacts": [<any>]};
    }
    return {"run": run, "complete": io["source"] == "done", "headers": {"Authorization": "Bearer tok"},
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
		Methods: []types.MergeMethod{types.MethodSquash, types.MethodMerge}, RequiredApprovals: 2, QueueLabel: "queue: ",
		Committer: magustypes.Person{Name: "bot", Email: "bot@example.com"}}, got)
}

// setupScript answers describe with a setup built from what it was asked.
const setupScript = `
export fun describe(io: {str: any}) > any {
    return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash"], "required_approvals": 0, "setup": {
        "status_context": io["status_context"],
        "credential": {"id": "812", "name": io["app"]},
        "required_checks": [{"context": "merge-queue", "integration": "15368"}, {"context": "ci gate", "events": ["{io["setup_steps"]}"]}],
        "settings": [{"name": "allow_auto_merge", "value": "false", "want": "true"}],
        "app": {"slug": io["app"], "id": "812", "client_id": "Iv1", "install_url": "https://github.com/apps/q/installations/new",
            "environment": "magus-queue", "variable": "V", "secret": "S"},
        "steps": [{"title": "Install it", "url": "https://github.com/apps/q/installations/new"}, {"title": "Store it", "command": "gh secret set S"}],
    }};
}
export fun list_changes(io: {str: any}) > any { return {}; }
export fun approval_at(io: {str: any}) > any { return {}; }
export fun list_green(io: {str: any}) > any { return {}; }
export fun post_status(io: {str: any}) > bool { return true; }
export fun retarget(io: {str: any}) > bool { return true; }
export fun merge_change(io: {str: any}) > any { return {}; }
export fun kick_back(io: {str: any}) > bool { return true; }
export fun mark(io: {str: any}) > bool { return true; }
export fun flag(io: {str: any}) > bool { return true; }
`

func TestDescribePassesTheSetupQueryAndDecodesTheSetup(t *testing.T) {
	got, err := open(t, setupScript).Describe(context.Background(), types.ListQuery{Base: "main", StatusContext: "gate", App: "q", SetupSteps: true})
	require.NoError(t, err)
	assert.Equal(t, &types.Setup{
		StatusContext: "gate",
		Credential:    types.Integration{ID: "812", Name: "q"},
		RequiredChecks: []types.RequiredCheck{
			{Context: "merge-queue", Integration: "15368"},
			{Context: "ci gate", Events: []string{"true"}},
		},
		Settings: []types.Setting{{Name: "allow_auto_merge", Value: "false", Want: "true"}},
		App: &types.App{Slug: "q", ID: "812", ClientID: "Iv1", InstallURL: "https://github.com/apps/q/installations/new",
			Environment: "magus-queue", Variable: "V", Secret: "S"},
		Steps: []types.SetupStep{
			{Title: "Install it", URL: "https://github.com/apps/q/installations/new"},
			{Title: "Store it", Command: "gh secret set S"},
		},
	}, got.Setup)
}

// A setup a person could not follow is the provider's error, named where it broke.
func TestDescribeRefusesASetupItCannotUse(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{`"credential": {"id": "812", "name": io["app"]},`, ``, `setup: field "credential" is missing`},
		{`{"id": "812", "name": io["app"]}`, `{"id": "", "name": io["app"]}`, `without the integration its credential posts as`},
		{`"command": "gh secret set S"`, `"command": "gh secret set S", "url": "https://x"`, `exactly one of a command or a URL`},
		{`"status_context": io["status_context"],`, `"status_context": "",`, `setup for no status context`},
	} {
		_, err := open(t, strings.Replace(setupScript, tc.from, tc.to, 1)).Describe(context.Background(), types.ListQuery{Base: "main", StatusContext: "gate"})
		require.ErrorContains(t, err, tc.want, tc.to)
	}
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
		Unqueued: []types.UnqueuedChange{{ID: "9", Repo: "acme/acme", Head: headF, Mark: types.MarkQueued}},
	}, got)
	_, err = open(t, strings.Replace(script, `"mark": "queued"`, `"mark": "merged"`, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `unqueued[0]: #9: mark "merged", want queued, rejected or none`)
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
		{`"unqueued": [{"id": "9", "head": "` + headF + `", "repo": "acme/acme", "mark": "queued"}],`, `list_changes: field "unqueued" is missing`},
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
	// Read as zero, a missing count would say the base requires no review.
	_, err = open(t, strings.Replace(script, `"required_approvals": 2, `, "", 1)).Describe(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `field "required_approvals" is missing`)
	_, err = open(t, strings.Replace(script, `"required_approvals": 2, `, `"required_approvals": "2", `, 1)).Describe(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `field "required_approvals" is string, want int`)
}

func TestListGreenDecodesTheChangesCarryingTheStatus(t *testing.T) {
	got, err := open(t, script).ListGreen(context.Background(), types.ListQuery{Base: "12", RemoteURL: "acme/acme"}, "merge-queue")
	require.NoError(t, err)
	assert.Equal(t, []types.GreenChange{{ID: "12", Repo: "acme/acme", Head: headD}}, got)
	got, err = open(t, script).ListGreen(context.Background(), types.ListQuery{Base: "12"}, "other")
	require.NoError(t, err)
	assert.Empty(t, got)
	_, err = open(t, script).ListGreen(context.Background(), types.ListQuery{Base: ".."}, "merge-queue")
	require.ErrorContains(t, err, `list_green: changes[0]: change id ".."`)
	_, err = open(t, strings.Replace(script, `"head": "`+headD+`"}]}`, `"head": "d"}]}`, 1)).ListGreen(context.Background(), types.ListQuery{Base: "12"}, "merge-queue")
	require.ErrorContains(t, err, `head "d" is not a full commit id`)
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
	merged, err := p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* body",
		Through: []types.PinnedChange{{ID: "5", Commit: headE}}})
	require.NoError(t, err)
	assert.Equal(t, types.MergeResult{}, merged, "the queue's own call merged the stack")
	merged, err = p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* body"})
	require.NoError(t, err)
	assert.Equal(t, types.MergeResult{ByProvider: true}, merged)
	_, err = p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* other"})
	require.ErrorContains(t, err, "not merged: head moved")
	kick := types.Kick{Code: types.CodeKickConflict, Report: "report", Paths: []string{"a.go", "b.go"}, CandidateCommit: "c",
		Source: "o/r/runs/7", Reproduce: &types.Reproduction{Gate: "make test"}}
	require.NoError(t, p.KickBack(ctx, change, headA, kick))
	noRepro := kick
	noRepro.Reproduce = nil
	require.ErrorContains(t, p.KickBack(ctx, change, headA, noRepro), "provider refused", "no reproduce record reaches the script as null")
	flagged := kick
	flagged.Flag = types.FlagChangesGenerator
	require.ErrorContains(t, p.KickBack(ctx, change, headA, flagged), "provider refused", "the flag reaches the script")
	kick.Paths = nil
	require.ErrorContains(t, p.KickBack(ctx, change, headA, kick), "provider refused")
	require.NoError(t, p.Mark(ctx, change, types.MarkQueued))
	require.NoError(t, p.Mark(ctx, change, types.MarkNone))
	require.ErrorContains(t, p.Mark(ctx, change, types.MarkRejected), "provider refused")
	require.EqualError(t, p.Mark(ctx, change, "merged"), `provider "echo": mark: mark "merged", want queued, rejected or none`)
	require.NoError(t, p.Flag(ctx, change, types.FlagChangesGenerator, true))
	require.ErrorContains(t, p.Flag(ctx, change, types.FlagChangesGenerator, false), "provider refused")
	require.EqualError(t, p.Flag(ctx, change, "urgent", true), `provider "echo": flag: flag "urgent", want changes_generator`)
}

func TestListArtifactsDecodesTheListing(t *testing.T) {
	got, err := open(t, script).ListArtifacts(context.Background(), "done")
	require.NoError(t, err)
	run := types.RunOrigin{Repo: "o/r", HeadRepo: "o/r", HeadBranch: "main", Event: "push", BranchEvent: true, Definition: "q.yaml"}
	assert.Equal(t, types.ArtifactListing{Run: run, Complete: true, Headers: map[string]string{"Authorization": "Bearer tok"},
		Artifacts: []types.Artifact{{Name: "mergequeue-plan", URL: "https://example.invalid/1.zip"}}}, got)
}

func TestAListingThatDoesNotSayWhatRanIsRefused(t *testing.T) {
	_, err := open(t, script).ListArtifacts(context.Background(), "no run")
	require.EqualError(t, err, `provider "echo": list_artifacts: field "run" is missing`)
}

func TestAScriptMissingAnOpIsRefusedAndListArtifactsIsOptional(t *testing.T) {
	_, err := newScript(context.Background(), "half", `export fun list_changes(io: {str: any}) > any { return {}; }`)
	require.EqualError(t, err, `provider "half" does not export describe, approval_at, list_green, post_status, retarget, merge_change, kick_back, mark, flag`)

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
