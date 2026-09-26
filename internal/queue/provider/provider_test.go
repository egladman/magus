package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
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
        "closed": [{"id": "12", "repo": "acme/acme"}],
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
    return {"merged": io["message"] == "* body" and io["app"] == "q" and (pinned or through.len() == 0), "by_provider": through.len() == 0, "reason": "head moved"};
}

export fun kick_back(io: {str: any}) > bool {
    final paths = io["paths"] ?? [<str>];
    final repro = serialize\Boxed.init(io["reproduce"]);
    return io["report"] == "report" and io["claim"] == "exit 1" and io["code"] == "KICK_CONFLICT" and "{paths}" == "{["a.go", "b.go"]}" and io["candidate_commit"] == "c"
        and io["source"] == "o/r/runs/7" and repro.q("gate").stringValue() == "make test" and repro.q("regenerate").stringValue() == "";
}

export fun mark(io: {str: any}) > bool {
    return io["id"] == "7" and io["repo"] == "acme/acme" and io["mark"] != "kicked_back";
}

export fun required_checks(io: {str: any}) > any {
    return {"checks": [{"name": "ci gate", "state": "failure"}, {"name": "{io["commit"]}", "state": "{io["title"]}"}]};
}

export fun list_artifacts(io: {str: any}) > any {
    final run ={"repo": "o/r", "head_repo": "o/r", "head_branch": "main", "event": "push", "branch_event": true, "definition": "q.yaml"};
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

// refusedScript declines to describe a setup for the app "q", asking for "q:<id>", and
// describes one for any other app, which it echoes as the credential's name. Its base
// picks a refusal that leaves something out.
const refusedScript = `
export fun describe(io: {str: any}) > any {
    if (io["base"] == "no reason") {
        return {"refused": {"url": "https://example.invalid/apps/q", "app": "q:<id>"}};
    }
    if (io["base"] == "no url") {
        return {"refused": {"reason": "no id for q", "url": "", "app": "q:<id>"}};
    }
    if (io["base"] == "no app") {
        return {"refused": {"reason": "no id for q", "url": "https://example.invalid/apps/q", "app": ""}};
    }
    if (io["app"] == "q") {
        return {"refused": {"reason": "no id for q", "url": "https://example.invalid/apps/q", "app": "q:<id>"}};
    }
    return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash"], "required_approvals": 0, "setup": {
        "status_context": io["status_context"], "credential": {"id": "812", "name": io["app"]}, "steps": [<any>],
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
`

// --app reaches the provider as the person wrote it, and a provider that declines to
// describe a setup for it says so as a *types.SetupRefusedError naming the app to ask
// again with.
func TestDescribePassesTheAppAsGivenAndDecodesARefusal(t *testing.T) {
	p := open(t, refusedScript)
	_, err := p.Describe(context.Background(), types.ListQuery{Base: "main", StatusContext: "gate", App: "q"})
	var refused *types.SetupRefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, &types.SetupRefusedError{Reason: "no id for q", URL: "https://example.invalid/apps/q", App: "q:<id>"}, refused)

	got, err := p.Describe(context.Background(), types.ListQuery{Base: "main", StatusContext: "gate", App: "q:2034567"})
	require.NoError(t, err)
	assert.Equal(t, &types.Setup{StatusContext: "gate", Credential: types.Integration{ID: "812", Name: "q:2034567"}}, got.Setup)
}

// A refusal without a reason, a URL or an app cannot tell a person what to run next.
func TestDescribeRefusesARefusalThatSaysNothing(t *testing.T) {
	p := open(t, refusedScript)
	for base, want := range map[string]string{
		"no reason": `field "reason" is missing`,
		"no url":    `field "refused" needs a reason, a URL and an app`,
		"no app":    `field "refused" needs a reason, a URL and an app`,
	} {
		_, err := p.Describe(context.Background(), types.ListQuery{Base: base, App: "q"})
		require.ErrorContains(t, err, want, base)
		assert.NotErrorAs(t, err, new(*types.SetupRefusedError), base)
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
		Closed:   []types.ClosedChange{{ID: "12", Repo: "acme/acme"}},
	}, got)
	_, err = open(t, strings.Replace(script, `"closed": [{"id": "12"`, `"closed": [{"id": "../12"`, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, "closed[0]:")
	_, err = open(t, strings.Replace(script, `"mark": "queued"`, `"mark": "merged"`, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `unqueued[0]: #9: mark "merged", want queued, kicked_back, needs_regeneration or none`)
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
	merged, err := p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* body", App: "q",
		Through: []types.PinnedChange{{ID: "5", Commit: headE}}})
	require.NoError(t, err)
	assert.Equal(t, types.MergeResult{}, merged, "the queue's own call merged the stack")
	merged, err = p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* body", App: "q"})
	require.NoError(t, err)
	assert.Equal(t, types.MergeResult{ByProvider: true}, merged)
	_, err = p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* other", App: "q"})
	require.ErrorContains(t, err, "not merged: head moved")
	_, err = p.MergeChange(ctx, change, types.MergeOptions{Commit: headA, Message: "* body"})
	require.ErrorContains(t, err, "not merged", "the app reaches the script")
	kick := types.Kick{Code: types.CodeKickConflict, Report: "report", Claim: "exit 1", Paths: []string{"a.go", "b.go"}, CandidateCommit: "c",
		Source: "o/r/runs/7", Reproduce: &types.Reproduction{Gate: "make test"}}
	require.NoError(t, p.KickBack(ctx, change, headA, kick))
	noRepro := kick
	noRepro.Reproduce = nil
	require.ErrorContains(t, p.KickBack(ctx, change, headA, noRepro), "provider refused", "no reproduce record reaches the script as null")
	regeneration := kick
	regeneration.Code = types.CodeKickRegeneration
	require.ErrorContains(t, p.KickBack(ctx, change, headA, regeneration), "provider refused", "the code reaches the script")
	kick.Paths = nil
	require.ErrorContains(t, p.KickBack(ctx, change, headA, kick), "provider refused")
	require.NoError(t, p.Mark(ctx, change, types.MarkQueued))
	require.NoError(t, p.Mark(ctx, change, types.MarkNone))
	require.NoError(t, p.Mark(ctx, change, types.MarkNeedsRegeneration))
	require.ErrorContains(t, p.Mark(ctx, change, types.MarkKickedBack), "provider refused")
	require.EqualError(t, p.Mark(ctx, change, "rejected"), `provider "echo": mark: mark "rejected", want queued, kicked_back, needs_regeneration or none`)
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
	require.EqualError(t, err, `provider "half" does not export describe, approval_at, list_green, post_status, retarget, merge_change, kick_back, mark`)

	noRuns := open(t, strings.Split(script, "export fun list_artifacts")[0])
	assert.False(t, noRuns.ListsArtifacts())
	_, err = noRuns.ListArtifacts(context.Background(), "done")
	require.EqualError(t, err, `provider "echo" does not export list_artifacts`)
}

func TestRequiredChecksDecodesEachStateAndIsOptional(t *testing.T) {
	c := types.Change{ID: "7", Repo: "acme/acme", Head: headA, Base: "main", Method: types.MethodSquash, Title: "pending"}
	got, err := open(t, script).RequiredChecks(context.Background(), c, headD)
	require.NoError(t, err)
	assert.Equal(t, []types.Check{{Name: "ci gate", State: types.StateFailure}, {Name: headD, State: types.StatePending}}, got)

	c.Title = "red"
	_, err = open(t, script).RequiredChecks(context.Background(), c, headD)
	require.EqualError(t, err, `provider "echo": required_checks: check "`+headD+`" has state "red", want pending, success or failure`)

	none, err := open(t, strings.Split(script, "export fun required_checks")[0]).RequiredChecks(context.Background(), c, headD)
	require.NoError(t, err)
	assert.Nil(t, none, "a provider that cannot read them reports none")
}

func TestAnUnknownProviderNamesBothPlacesItLooked(t *testing.T) {
	_, err := Open(context.Background(), "gitlab")
	require.ErrorContains(t, err, `provider "gitlab": not built in`)
}

func TestTheBridgeRefusesAValueItDoesNotPass(t *testing.T) {
	_, err := toValue(map[string]any{"n": 3})
	require.EqualError(t, err, `field "n" is int, which the bridge does not pass`)
}

// fakeGitHub answers the GitHub provider's reads from answers, keyed by method, path and
// query, and fails the test on any read it has no answer for. It returns the remote URL
// of acme/widgets on it.
func fakeGitHub(t *testing.T, answers map[string]answer) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		a, ok := answers[key]
		if !ok {
			t.Errorf("unexpected read %s", key)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "t")
	t.Setenv("MERGEQUEUE_TOKEN", "")
	return "https://" + strings.TrimPrefix(srv.URL, "http://") + "/acme/widgets"
}

// fakeWeb is the web host the GitHub provider derives from fakeGitHub's API root.
func fakeWeb() string {
	return "https://" + strings.TrimPrefix(os.Getenv("GITHUB_API_URL"), "http://")
}

type answer struct {
	status int
	body   string
}

var notFound = answer{http.StatusNotFound, `{"message": "Not Found"}`}

// repoAnswers are the reads every describe with a status context makes of acme/widgets,
// owned by a user or an organization, whose main branch nothing protects.
func repoAnswers(ownerType string) map[string]answer {
	return map[string]answer{
		"GET /repos/acme/widgets": {200, `{"allow_auto_merge": true, "allow_squash_merge": true, "allow_merge_commit": false, "allow_rebase_merge": false,
			"owner": {"type": "` + ownerType + `"}}`},
		"GET /repos/acme/widgets/rules/branches/main": {200, `[]`},
		"GET /repos/acme/widgets/branches/main":       {200, `{"protection": {}}`},
		"GET /users/q[bot]":                           {200, `{"login": "q[bot]", "id": 333550495}`},
		"GET /apps/q":                                 notFound,
	}
}

func githubDescribe(t *testing.T, answers map[string]answer, app string, steps bool) (types.Capabilities, error) {
	t.Helper()
	remote := fakeGitHub(t, answers)
	p, err := Open(context.Background(), "github")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p.Describe(context.Background(), types.ListQuery{Base: "main", RemoteURL: remote, StatusContext: "merge-queue", App: app, SetupSteps: steps})
}

// A private app on an organization's repository is hidden from GET /apps/<slug>, and an
// owner's token finds its App ID among the organization's installations: nobody types it.
func TestDescribeGitHubReadsAPrivateAppsIDFromTheOrganizationsInstallations(t *testing.T) {
	answers := repoAnswers("Organization")
	answers["GET /orgs/acme/installations?per_page=100&page=1"] = answer{200, `{"total_count": 2, "installations": [
		{"app_id": 15368, "app_slug": "github-actions"}, {"app_id": 2034567, "app_slug": "q"}]}`}
	got, err := githubDescribe(t, answers, "q", false)
	require.NoError(t, err)
	assert.Equal(t, types.Capabilities{
		StackMerge: types.StackMergeAtomic, LinearStacks: true, Methods: []types.MergeMethod{types.MethodSquash}, QueueLabel: "merge-queue: ",
		Committer: magustypes.Person{Name: "q[bot]", Email: "333550495+q[bot]@users.noreply.github.com"},
		Setup: &types.Setup{
			StatusContext: "merge-queue",
			Credential:    types.Integration{ID: "2034567"},
			Settings:      []types.Setting{{Name: "allow_auto_merge", Value: "true", Want: "true"}},
		},
	}, got)
}

// Where GitHub shows the App ID to no token describe holds, describe names where it is
// and which app, and describes nothing.
func TestDescribeGitHubRefusesAPrivateAppWhoseIDItCannotRead(t *testing.T) {
	user := repoAnswers("User")
	org := repoAnswers("Organization")
	org["GET /orgs/acme/installations?per_page=100&page=1"] = notFound
	for name, tc := range map[string]struct {
		answers map[string]answer
		want    func(web string) *types.SetupRefusedError
	}{
		"a user's repository": {user, func(web string) *types.SetupRefusedError {
			return &types.SetupRefusedError{
				Reason: "github: GitHub shows the App ID of the private app q to no token but its own installation's; it is under About on the app's settings page",
				URL:    web + "/settings/apps/q", App: "q:<id>",
			}
		}},
		"an organization's, to a token that is not an owner's": {org, func(web string) *types.SetupRefusedError {
			return &types.SetupRefusedError{
				Reason: "github: GitHub shows the App ID of the private app q only to its own installation's token and, once it is installed on acme, to an owner of acme; it is under About on the app's settings page",
				URL:    web + "/organizations/acme/settings/apps/q", App: "q:<id>",
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := githubDescribe(t, tc.answers, "q", true)
			var refused *types.SetupRefusedError
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, tc.want(fakeWeb()), refused)
		})
	}
}

// A slug GitHub has no bot user for names no app at all, so describe asks for the slug
// again rather than for an id; with no app named it links the registration.
func TestDescribeGitHubRefusesAnAppItCannotFindOrNone(t *testing.T) {
	answers := repoAnswers("User")
	answers["GET /users/q[bot]"] = notFound
	_, err := githubDescribe(t, answers, "q", true)
	var refused *types.SetupRefusedError
	require.ErrorAs(t, err, &refused)
	web := fakeWeb()
	assert.Equal(t, &types.SetupRefusedError{
		Reason: "github: no GitHub App is named q, since GitHub has no user q[bot]; an app's slug ends the address of its settings page, and the apps are listed",
		URL:    web + "/settings/apps", App: "<slug>",
	}, refused)

	_, err = githubDescribe(t, repoAnswers("User"), "", true)
	require.ErrorAs(t, err, &refused)
	web = fakeWeb()
	assert.Equal(t, &types.SetupRefusedError{
		Reason: "github: the merge queue writes only as its own GitHub App, and none is named; register one",
		URL: web + "/settings/apps/new?name=acme-magus-queue&url=" + strings.ToLower(url.QueryEscape(web+"/acme/widgets")) +
			"&public=false&webhook_active=false&contents=write&pull_requests=write&statuses=write&actions=write&workflows=write",
		App: "<slug>",
	}, refused)
}

// The id a person gives is the pin's integration when GitHub hides the app, and GitHub's
// own answer when it does not; one that is not an App ID, or disagrees, is an error.
func TestDescribeGitHubPinsAGivenIDAndChecksIt(t *testing.T) {
	answers := repoAnswers("User")
	answers["GET /repos/acme/widgets/pulls?state=all&base=main&sort=updated&direction=desc&per_page=20"] = answer{200, `[]`}
	got, err := githubDescribe(t, answers, "q:2034567", true)
	require.NoError(t, err)
	web := fakeWeb()
	assert.Equal(t, &types.Setup{
		StatusContext: "merge-queue",
		Credential:    types.Integration{ID: "2034567"},
		Settings:      []types.Setting{{Name: "allow_auto_merge", Value: "true", Want: "true"}},
		App: &types.App{Slug: "q", ID: "2034567",
			RegistrationURL: web + "/settings/apps/new?name=acme-magus-queue&url=" + strings.ToLower(url.QueryEscape(web+"/acme/widgets")) +
				"&public=false&webhook_active=false&contents=write&pull_requests=write&statuses=write&actions=write&workflows=write",
			InstallURL:  web + "/apps/q/installations/new",
			Environment: "magus-queue", Variable: "MAGUS_QUEUE_APP_CLIENT_ID", Secret: "MAGUS_QUEUE_APP_PRIVATE_KEY"},
		Steps: []types.SetupStep{
			{Title: "Install q on acme/widgets only", URL: web + "/apps/q/installations/new"},
			{Title: "Create the magus-queue environment, whose secrets only main can read, so pull request runs never see the key",
				Command: "gh api -X PUT repos/acme/widgets/environments/magus-queue -F 'deployment_branch_policy[protected_branches]=false' -F 'deployment_branch_policy[custom_branch_policies]=true'\n" +
					"gh api -X POST repos/acme/widgets/environments/magus-queue/deployment-branch-policies -f name=main -f type=branch"},
			{Title: "Store the app's client id, under About on " + web + "/settings/apps/q; gh asks for it",
				Command: "gh variable set MAGUS_QUEUE_APP_CLIENT_ID --repo acme/widgets"},
			{Title: "Generate a private key on " + web + "/settings/apps/q (a .pem downloads), store it in the environment alone, and delete the download",
				Command: "(\n" +
					"  pem=$(find ~/Downloads -maxdepth 1 -name 'q.*.private-key.pem' -exec ls -t {} + | head -n 1)\n" +
					"  if [ -n \"$pem\" ]; then\n" +
					"    gh secret set MAGUS_QUEUE_APP_PRIVATE_KEY --repo acme/widgets --env magus-queue < \"$pem\"\n" +
					"    find ~/Downloads -maxdepth 1 -name 'q.*.private-key.pem' -delete\n" +
					"  else\n" +
					"    echo 'no q key in ~/Downloads: generate one on " + web + "/settings/apps/q unless MAGUS_QUEUE_APP_PRIVATE_KEY is listed below' >&2\n" +
					"  fi\n" +
					"  gh secret ls --repo acme/widgets --env magus-queue\n" +
					")"},
			{Title: "Require merge-queue from 2034567 on main, in a ruleset of its own (your other rulesets stay as they are)",
				Command: "gh api -X POST repos/acme/widgets/rulesets --input - <<'EOF'\n" +
					`{"name":"magus merge queue","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},` +
					`"rules":[{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,` +
					`"required_status_checks":[{"context":"merge-queue","integration_id":2034567}]}}]}` + "\nEOF"},
		},
	}, got.Setup)

	readable := repoAnswers("User")
	readable["GET /apps/q"] = answer{200, `{"slug": "q", "id": 812, "client_id": "Iv23li", "name": "Q queue"}`}
	for app, want := range map[string]string{
		"q:0812": "github: the app 'q:0812' is not <slug> or <slug>:<App ID>, an App ID being a positive integer with no leading zero",
		"q:Iv23": "github: the app 'q:Iv23' is not <slug> or <slug>:<App ID>, an App ID being a positive integer with no leading zero",
		"q:":     "github: the app 'q:' is not <slug> or <slug>:<App ID>, an App ID being a positive integer with no leading zero",
		":812":   "github: the app ':812' is not <slug> or <slug>:<App ID>, an App ID being a positive integer with no leading zero",
		"q:813":  "github: describe: the app q is App ID 812, and the id given is 813",
	} {
		_, err := githubDescribe(t, readable, app, false)
		require.ErrorContains(t, err, want, app)
		assert.NotErrorAs(t, err, new(*types.SetupRefusedError), app)
	}
}

// Only 404 means GitHub hides the app; a 403 is the token's, and says so.
func TestDescribeGitHubReportsAForbiddenAppRead(t *testing.T) {
	answers := repoAnswers("User")
	answers["GET /apps/q"] = answer{http.StatusForbidden, `{"message": "Forbidden"}`}
	_, err := githubDescribe(t, answers, "q:2034567", false)
	require.ErrorContains(t, err, `github: read the app q: HTTP 403: either it does not exist or the token lacks access to it: {"message": "Forbidden"}`)
	assert.NotErrorAs(t, err, new(*types.SetupRefusedError))
}
