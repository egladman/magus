package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

// The GitHub provider's own `test` blocks, run on the same VM modules the queue gives it.
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
var script = fmt.Sprintf(`
import "std";
import "mergequeue/types";

export fun describe(req: QueueDescribeRequest) > any {
    return {"capabilities": {"stackMerge": "atomic", "linearStacks": true, "methods": ["squash", req.base], "requiredApprovals": 2, "queueLabel": "queue: ",
        "committer": {"name": "bot", "email": "bot@example.com"}}};
}

export fun list_changes(req: QueueListChangesRequest) > any {
    return {
        "changes": [{
            "id": "7", "repo": "acme/acme", "head": "%s", "ref": "refs/pull/7/head",
            "branch": "feat", "base": req.base, "title": req.remoteURL, "author": "priya", "fork": true,
            "method": "squash", "parent": "6",
        }],
        "merged": [{"id": "6", "head": "%s", "commit": "%s", "method": "squash"}],
        "unqueued": [{"id": "9", "head": "%s", "repo": "acme/acme", "title": "wip", "author": "ann", "branch": "wip", "mark": "queued", "base": "main"}],
        "closed": [{"id": "12", "repo": "acme/acme"}],
    };
}

export fun approval_at(req: QueueApprovalRequest) > any {
    return {"approved": req.id == "7", "head": req.commit, "reason": req.title,
        "base": "main", "method": req.method, "approvedCommit": "%s",
        "queued": true, "branchSharedWith": ["40"]};
}

export fun list_green(req: QueueListGreenRequest) > any {
    if (req.context != "merge-queue") {
        return [<any>];
    }
    return [{"id": req.base, "repo": req.remoteURL, "head": "%s"}];
}

export fun post_status(req: QueueStatusRequest) > Ack {
    return Ack{ ok = req.context == "merge-queue" and req.state == "success" };
}

export fun retarget(req: QueueRetargetRequest) > Ack {
    return Ack{ ok = req.to == "main" };
}

export fun merge_change(req: QueueMergeRequest) > any {
    final through = req.through;
    final pinned = through.len() == 1 and through[0].id == "5" and through[0].commit == "%s";
    return {"merged": req.message == "* body" and req.app == "q" and (pinned or through.len() == 0), "byProvider": through.len() == 0, "reason": "head moved"};
}

export fun kick_back(req: QueueKickBackRequest) > Ack {
    var gate = "";
    var regenerate = "-";
    if (req.reproduce -> repro) {
        gate = repro.gate;
        regenerate = repro.regenerate;
    }
    return Ack{ ok = req.report == "report" and req.claim == "exit 1" and req.code == "KICK_CONFLICT" and "{req.paths}" == "{["a.go", "b.go"]}" and req.candidateCommit == "c"
        and req.source == "o/r/runs/7" and gate == "make test" and regenerate == "" };
}

export fun mark(req: QueueMarkRequest) > Ack {
    return Ack{ ok = req.id == "7" and req.repo == "acme/acme" and req.mark != "kicked_back" };
}

export fun required_checks(req: QueueRequiredChecksRequest) > any {
    return [{"name": "ci gate", "state": "failure"}, {"name": req.commit, "state": req.title}];
}

export fun reviews(req: QueueReviewsRequest) > any {
    if (req.title == "bad commit") {
        return {"head": "%s", "approving": [{"id": "12", "reviewer": "ann", "commit": "d"}]};
    }
    return {"head": "%s", "approving": [{"id": "12", "reviewer": req.title, "commit": "%s"}]};
}

export fun dismiss_review(req: QueueDismissReviewRequest) > Ack {
    return Ack{ ok = req.id == "7" and req.review.id == "12" and req.review.reviewer == "ann" and req.review.commit == "%s" and req.message == "a.go changed" };
}

export fun list_artifacts(req: QueueListArtifactsRequest) > any {
    final run = {"repo": "o/r", "headRepo": "o/r", "headBranch": "main", "event": "push", "branchEvent": true, "definition": "q.yaml"};
    if (req.source == "no run") {
        return {"complete": true, "artifacts": [<any>]};
    }
    return {"run": run, "complete": req.source == "done", "headers": {"Authorization": "Bearer tok"},
        "artifacts": [{"name": "mergequeue-plan", "url": "https://example.invalid/1.zip"}]};
}
`, headA, headD, headE, headF, headD, headD, headE, headA, headA, headD, headD)

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
import "mergequeue/types";
export fun describe(req: QueueDescribeRequest) > any {
    return {"capabilities": {"stackMerge": "atomic", "linearStacks": true, "methods": ["squash"], "requiredApprovals": 0, "setup": {
        "statusContext": req.statusContext,
        "credential": {"id": "812", "name": req.app},
        "requiredChecks": [{"context": "merge-queue", "integration": "15368"}, {"context": "ci gate", "events": ["{req.setupSteps}"]}],
        "settings": [{"name": "allow_auto_merge", "value": "false", "want": "true"}],
        "app": {"slug": req.app, "id": "812", "clientID": "Iv1", "installURL": "https://github.com/apps/q/installations/new",
            "environment": "magus-queue", "variable": "V", "secret": "S"},
        "steps": [{"title": "Install it", "url": "https://github.com/apps/q/installations/new"}, {"title": "Store it", "command": "gh secret set S"}],
    }}};
}
` + otherOps

// otherOps are the required ops a describe-only script still exports, never called.
const otherOps = `
export fun list_changes(req: QueueListChangesRequest) > any { return {}; }
export fun approval_at(req: QueueApprovalRequest) > any { return {}; }
export fun list_green(req: QueueListGreenRequest) > any { return {}; }
export fun post_status(req: QueueStatusRequest) > Ack { return Ack{ ok = true }; }
export fun retarget(req: QueueRetargetRequest) > Ack { return Ack{ ok = true }; }
export fun merge_change(req: QueueMergeRequest) > any { return {}; }
export fun kick_back(req: QueueKickBackRequest) > Ack { return Ack{ ok = true }; }
export fun mark(req: QueueMarkRequest) > Ack { return Ack{ ok = true }; }
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
		{`"credential": {"id": "812", "name": req.app},`, ``, `setup: field "credential" is missing`},
		{`{"id": "812", "name": req.app}`, `{"id": "", "name": req.app}`, `without the integration its credential posts as`},
		{`"command": "gh secret set S"`, `"command": "gh secret set S", "url": "https://x"`, `exactly one of a command or a URL`},
		{`"statusContext": req.statusContext,`, `"statusContext": "",`, `setup for no status context`},
	} {
		_, err := open(t, strings.Replace(setupScript, tc.from, tc.to, 1)).Describe(context.Background(), types.ListQuery{Base: "main", StatusContext: "gate"})
		require.ErrorContains(t, err, tc.want, tc.to)
	}
}

// refusedScript declines to describe a setup for the app "q", asking for "q:<id>", and
// describes one for any other app, which it echoes as the credential's name. Its base
// picks a refusal that leaves something out.
const refusedScript = `
import "mergequeue/types";
export fun describe(req: QueueDescribeRequest) > any {
    if (req.base == "no reason") {
        return {"refused": {"url": "https://example.invalid/apps/q", "app": "q:<id>"}};
    }
    if (req.base == "no url") {
        return {"refused": {"reason": "no id for q", "url": "", "app": "q:<id>"}};
    }
    if (req.base == "no app") {
        return {"refused": {"reason": "no id for q", "url": "https://example.invalid/apps/q", "app": ""}};
    }
    if (req.app == "q") {
        return {"refused": {"reason": "no id for q", "url": "https://example.invalid/apps/q", "app": "q:<id>"}};
    }
    return {"capabilities": {"stackMerge": "atomic", "linearStacks": true, "methods": ["squash"], "requiredApprovals": 0, "setup": {
        "statusContext": req.statusContext, "credential": {"id": "812", "name": req.app}, "steps": [<any>],
    }}};
}
` + otherOps

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
			Base: "main", Title: "git@github.com:acme/acme.git", Author: "priya", Fork: true,
			Method: types.MethodSquash, Parent: "6",
		}},
		Merged: []types.MergedChange{{ID: "6", Head: headD, Commit: headE, Method: types.MethodSquash}},
		Unqueued: []types.UnqueuedChange{{
			ID: "9", Repo: "acme/acme", Head: headF, Title: "wip", Author: "ann", Branch: "wip", Base: "main", Mark: types.MarkQueued,
		}},
		Closed: []types.ClosedChange{{ID: "12", Repo: "acme/acme"}},
	}, got)
	_, err = open(t, strings.Replace(script, `"closed": [{"id": "12"`, `"closed": [{"id": "../12"`, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, "closed[0]:")
	_, err = open(t, strings.Replace(script, `"mark": "queued"`, `"mark": "merged"`, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `unqueued[0]: #9: mark "merged", want queued, kicked_back, needs_regeneration or none`)
}

func TestListChangesRefusesWhatGitCouldReadAsAnOption(t *testing.T) {
	for _, tc := range []struct{ from, to, want string }{
		{fmt.Sprintf("\"head\": \"%s\", ", headA), `"head": "", `, `head "" is not a full commit id`},
		{`"id": "7"`, `"id": ".."`, `change id ".."`},
		{`"branch": "feat"`, `"branch": "--upload-pack=x"`, `starts with '-'`},
		{`"ref": "refs/pull/7/head"`, `"ref": "refs/pull/7/head:refs/heads/main"`, `holds ':'`},
		{`"method": "squash", "parent"`, `"method": "", "parent"`, `merge method ""`},
		{fmt.Sprintf("\"commit\": \"%s\"", headE), `"commit": "e"`, `must be full commit ids`},
	} {
		_, err := open(t, strings.Replace(script, tc.from, tc.to, 1)).ListChanges(context.Background(), types.ListQuery{Base: "main"})
		require.ErrorContains(t, err, tc.want, tc.to)
	}
}

// A field a record leaves out is an error, never its zero value. Before, a change
// record without "fork" read as a change from this repository.
func TestAMissingRequiredFieldIsAnError(t *testing.T) {
	for _, tc := range []struct{ from, want string }{
		{`, "fork": true`, `list_changes.changes[0]: field "fork" is missing`},
		{`"repo": "acme/acme", `, `list_changes.changes[0]: field "repo" is missing`},
		{`"base": req.base, `, `list_changes.changes[0]: field "base" is missing`},
		{fmt.Sprintf("\"unqueued\": [{\"id\": \"9\", \"head\": \"%s\", \"repo\": \"acme/acme\", \"title\": \"wip\", \"author\": \"ann\", \"branch\": \"wip\", \"mark\": \"queued\", \"base\": \"main\"}],", headF), `list_changes: field "unqueued" is missing`},
		{`, "method": "squash"}]`, `list_changes.merged[0]: field "method" is missing`},
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
		{`"branchSharedWith": ["40"]`, `field "branchSharedWith" is missing`},
		{`"base": "main", `, `field "base" is missing`},
	} {
		_, err := open(t, strings.Replace(script, tc.from, "", 1)).ApprovalAt(context.Background(), change, headD)
		require.ErrorContains(t, err, tc.want, tc.from)
	}
	_, err := open(t, strings.Replace(script, `"linearStacks": true, `, "", 1)).Describe(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `field "linearStacks" is missing`)
	// Read as zero, a missing count would say the base requires no review.
	_, err = open(t, strings.Replace(script, `"requiredApprovals": 2, `, "", 1)).Describe(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `field "requiredApprovals" is missing`)
	_, err = open(t, strings.Replace(script, `"requiredApprovals": 2, `, `"requiredApprovals": "2", `, 1)).Describe(context.Background(), types.ListQuery{Base: "main"})
	require.ErrorContains(t, err, `describe.capabilities.requiredApprovals: want int`)
}

func TestListGreenDecodesTheChangesCarryingTheStatus(t *testing.T) {
	got, err := open(t, script).ListGreen(context.Background(), types.ListQuery{Base: "12", RemoteURL: "acme/acme"}, "merge-queue")
	require.NoError(t, err)
	assert.Equal(t, []types.GreenChange{{ID: "12", Repo: "acme/acme", Head: headD}}, got)
	got, err = open(t, script).ListGreen(context.Background(), types.ListQuery{Base: "12"}, "other")
	require.NoError(t, err)
	assert.Empty(t, got)
	_, err = open(t, script).ListGreen(context.Background(), types.ListQuery{Base: ".."}, "merge-queue")
	require.ErrorContains(t, err, `list_green[0]: change id ".."`)
	_, err = open(t, strings.Replace(script, fmt.Sprintf("\"head\": \"%s\"}];", headD), `"head": "d"}];`, 1)).ListGreen(context.Background(), types.ListQuery{Base: "12"}, "merge-queue")
	require.ErrorContains(t, err, `head "d" is not a full commit id`)
}

func TestApprovalDecodesAtTheCommitAsked(t *testing.T) {
	got, err := open(t, script).ApprovalAt(context.Background(), change, headD)
	require.NoError(t, err)
	assert.Equal(t, types.Approval{Approved: true, Head: headD, Reason: "add x", Base: "main",
		Method: types.MethodSquash, ApprovedCommit: headD, Queued: true, BranchSharedWith: []string{"40"}}, got)
}

func TestApprovalWithoutAHeadIsAnError(t *testing.T) {
	p := open(t, strings.Replace(script, `"head": req.commit, `, `"head": "", `, 1))
	_, err := p.ApprovalAt(context.Background(), change, headD)
	require.ErrorContains(t, err, `approval_at: head: #7 (add x): head "" is not a full commit id`)
}

func TestApprovalOfTheWrongTypeNamesTheField(t *testing.T) {
	p := open(t, strings.Replace(script, `"approved": req.id == "7"`, `"approved": "yes"`, 1))
	_, err := p.ApprovalAt(context.Background(), change, headA)
	require.ErrorContains(t, err, `approval_at.approved: want bool`)
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
	require.EqualError(t, err, `provider "echo": list_artifacts: field "run" is missing, and a QueueArtifactListing requires it`)
}

func TestAScriptMissingAnOpIsRefusedAndListArtifactsIsOptional(t *testing.T) {
	_, err := newScript(context.Background(), "half", `import "mergequeue/types";
export fun list_changes(req: QueueListChangesRequest) > any { return {}; }`)
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
	require.EqualError(t, err, fmt.Sprintf("provider \"echo\": required_checks: check \"%s\" has state \"red\", want pending, success or failure", headD))

	none, err := open(t, strings.Split(script, "export fun required_checks")[0]).RequiredChecks(context.Background(), c, headD)
	require.NoError(t, err)
	assert.Nil(t, none, "a provider that cannot read them reports none")
}

func TestReviewsDecodesTheApprovalsAndDismissReviewCarriesTheReview(t *testing.T) {
	p, ctx := open(t, script), context.Background()
	c := change
	c.Title = "ann"
	got, err := p.Reviews(ctx, c)
	require.NoError(t, err)
	assert.Equal(t, types.Reviews{Head: headA, Approving: []types.Review{{ID: "12", Reviewer: "ann", Commit: headD}}}, got)

	require.NoError(t, p.DismissReview(ctx, c, got.Approving[0], "a.go changed"))
	require.ErrorContains(t, p.DismissReview(ctx, c, got.Approving[0], "other"), "provider refused")

	c.Title = "bad commit"
	_, err = p.Reviews(ctx, c)
	require.ErrorContains(t, err, `review 12: commit "d" is not a full commit id`)

	none := open(t, fmt.Sprintf("%sexport fun list_artifacts%s", strings.Split(script, "export fun reviews")[0], strings.Split(script, "export fun list_artifacts")[1]))
	_, err = none.Reviews(ctx, c)
	require.EqualError(t, err, `provider "echo" does not export reviews`, "reviews is optional")
}

func TestAnUnknownProviderNamesBothPlacesItLooked(t *testing.T) {
	_, err := Open(context.Background(), "gitlab")
	require.ErrorContains(t, err, `provider "gitlab": not built in`)
}

// cannedGitHub answers the GitHub provider's reads from answers, keyed by method, path and
// query, and fails the test on any read it has no answer for. It returns the remote URL
// of acme/widgets on it.
func cannedGitHub(t *testing.T, answers map[string]answer) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
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
	return fmt.Sprintf("https://%s/acme/widgets", strings.TrimPrefix(srv.URL, "http://"))
}

// cannedWeb is the web host the GitHub provider derives from cannedGitHub's API root.
func cannedWeb() string {
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
		"GET /repos/acme/widgets": {200, fmt.Sprintf(`{"allow_auto_merge": true, "allow_squash_merge": true, "allow_merge_commit": false, "allow_rebase_merge": false,
			"owner": {"type": "%s"}}`, ownerType)},
		"GET /repos/acme/widgets/rules/branches/main": {200, `[]`},
		"GET /repos/acme/widgets/branches/main":       {200, `{"protection": {}}`},
		"GET /users/q[bot]":                           {200, `{"login": "q[bot]", "id": 333550495}`},
		"GET /apps/q":                                 notFound,
	}
}

func githubDescribe(t *testing.T, answers map[string]answer, app string, steps bool) (types.Capabilities, error) {
	t.Helper()
	remote := cannedGitHub(t, answers)
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
			assert.Equal(t, tc.want(cannedWeb()), refused)
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
	web := cannedWeb()
	assert.Equal(t, &types.SetupRefusedError{
		Reason: "github: no GitHub App is named q, since GitHub has no user q[bot]; an app's slug ends the address of its settings page, and the apps are listed",
		URL:    web + "/settings/apps", App: "<slug>",
	}, refused)

	_, err = githubDescribe(t, repoAnswers("User"), "", true)
	require.ErrorAs(t, err, &refused)
	web = cannedWeb()
	assert.Equal(t, &types.SetupRefusedError{
		Reason: "github: the merge queue writes only as its own GitHub App, and none is named; register one",
		URL:    fmt.Sprintf("%s/settings/apps/new?name=acme-magus-queue&url=%s&public=false&webhook_active=false&contents=write&pull_requests=write&statuses=write&actions=write&workflows=write", web, strings.ToLower(url.QueryEscape(web+"/acme/widgets"))),
		App:    "<slug>",
	}, refused)
}

// The id a person gives is the pin's integration when GitHub hides the app, and GitHub's
// own answer when it does not; one that is not an App ID, or disagrees, is an error.
func TestDescribeGitHubPinsAGivenIDAndChecksIt(t *testing.T) {
	answers := repoAnswers("User")
	answers["GET /repos/acme/widgets/pulls?state=all&base=main&sort=updated&direction=desc&per_page=20"] = answer{200, `[]`}
	got, err := githubDescribe(t, answers, "q:2034567", true)
	require.NoError(t, err)
	web := cannedWeb()
	assert.Equal(t, &types.Setup{
		StatusContext: "merge-queue",
		Credential:    types.Integration{ID: "2034567"},
		Settings:      []types.Setting{{Name: "allow_auto_merge", Value: "true", Want: "true"}},
		App: &types.App{Slug: "q", ID: "2034567",
			RegistrationURL: fmt.Sprintf("%s/settings/apps/new?name=acme-magus-queue&url=%s&public=false&webhook_active=false&contents=write&pull_requests=write&statuses=write&actions=write&workflows=write", web, strings.ToLower(url.QueryEscape(web+"/acme/widgets"))),
			InstallURL:      web + "/apps/q/installations/new",
			Environment:     "magus-queue", Variable: "MAGUS_QUEUE_APP_CLIENT_ID", Secret: "MAGUS_QUEUE_APP_PRIVATE_KEY"},
		Steps: []types.SetupStep{
			{Title: "Install q on acme/widgets only", URL: web + "/apps/q/installations/new"},
			{Title: "Create the magus-queue environment, whose secrets only main can read, so pull request runs never see the key",
				Command: "gh api -X PUT repos/acme/widgets/environments/magus-queue -F 'deployment_branch_policy[protected_branches]=false' -F 'deployment_branch_policy[custom_branch_policies]=true'\n" +
					"gh api -X POST repos/acme/widgets/environments/magus-queue/deployment-branch-policies -f name=main -f type=branch"},
			{Title: fmt.Sprintf("Store the app's client id, under About on %s/settings/apps/q; gh asks for it", web),
				Command: "gh variable set MAGUS_QUEUE_APP_CLIENT_ID --repo acme/widgets"},
			{Title: fmt.Sprintf("Generate a private key on %s/settings/apps/q (a .pem downloads), store it in the environment alone, and delete the download", web),
				Command: fmt.Sprintf("(\n  pem=$(find ~/Downloads -maxdepth 1 -name 'q.*.private-key.pem' -exec ls -t {} + | head -n 1)\n  if [ -n \"$pem\" ]; then\n    gh secret set MAGUS_QUEUE_APP_PRIVATE_KEY --repo acme/widgets --env magus-queue < \"$pem\"\n    find ~/Downloads -maxdepth 1 -name 'q.*.private-key.pem' -delete\n  else\n    echo 'no q key in ~/Downloads: generate one on %s/settings/apps/q unless MAGUS_QUEUE_APP_PRIVATE_KEY is listed below' >&2\n  fi\n  gh secret ls --repo acme/widgets --env magus-queue\n)", web)},
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

// countingGitHub serves acme/widgets with three open pull requests, #1 and #2 queued by
// auto-merge and #3 under review with two pages of reviews, and counts every request by
// the read it is. It returns the remote URL and the counts.
func countingGitHub(t *testing.T) (string, map[string]int) {
	t.Helper()
	pr := func(n, sha, merge string) string {
		auto := `null`
		if merge != "" {
			auto = fmt.Sprintf("{\"merge_method\": \"%s\"}", merge)
		}
		return fmt.Sprintf(`{"number": %s, "draft": false, "title": "t", "user": {"login": "u"}, "labels": [],
			"head": {"sha": "%s", "ref": "b%s", "repo": {"full_name": "acme/widgets"}},
			"base": {"ref": "main"}, "auto_merge": %s}`, n, strings.Repeat(sha, 40), n, auto)
	}
	pulls := fmt.Sprintf("[%s,%s,%s]", pr("1", "a", "squash"), pr("2", "b", "squash"), pr("3", "c", ""))
	review := func(id, who, oid string) string {
		return fmt.Sprintf("{\"databaseId\": %s, \"state\": \"APPROVED\", \"authorCanPushToRepository\": true, \"author\": {\"login\": \"%s\"}, \"commit\": {\"oid\": \"%s\"}}", id, who, oid)
	}
	page := func(next, cursor, nodes string) string {
		return fmt.Sprintf(`{"data": {"repository": {"pullRequest": {"headRefOid": "%s", "reviewDecision": "APPROVED",
			"reviews": {"pageInfo": {"hasNextPage": %s, "endCursor": "%s"}, "nodes": [%s]}}}}}`, strings.Repeat("c", 40), next, cursor, nodes)
	}
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		var answer string
		switch {
		case r.URL.Path == "/repos/acme/widgets/pulls":
			answer = pulls
		case r.URL.Path == "/search/issues":
			answer = `{"items": []}`
		case r.URL.Path == "/graphql" && strings.Contains(string(body), "commits(first"):
			key += " merged"
			answer = `{"data": {"repository": {"pullRequest": {"commits": {"pageInfo": {"hasNextPage": false}, "nodes": []}}}}}`
		case r.URL.Path == "/graphql" && strings.Contains(string(body), `"c1"`):
			key += " reviews"
			answer = page("false", "c2", review("13", "cy", strings.Repeat("e", 40)))
		case r.URL.Path == "/graphql":
			key += " reviews"
			answer = page("true", "c1", review("12", "ann", strings.Repeat("d", 40)))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		counts[key]++
		mu.Unlock()
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "t")
	t.Setenv("MERGEQUEUE_TOKEN", "")
	return fmt.Sprintf("https://%s/acme/widgets", strings.TrimPrefix(srv.URL, "http://")), counts
}

// A listing scoped to the change under review reads the merged changes that one carries
// alone, and no closed ones: two requests here, where the queue's full listing takes
// four, one more for every queued pull request.
func TestAListingScopedToOneChangeReadsWhatItsClassificationTakes(t *testing.T) {
	remote, counts := countingGitHub(t)
	p, err := Open(context.Background(), "github")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	full, err := p.ListChanges(context.Background(), types.ListQuery{Base: "main", RemoteURL: remote})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"GET /repos/acme/widgets/pulls": 1, "POST /graphql merged": 2, "GET /search/issues": 1}, counts, "the full listing")
	clear(counts)

	scoped, err := p.ListChanges(context.Background(), types.ListQuery{Base: "main", RemoteURL: remote, Only: "3"})
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"GET /repos/acme/widgets/pulls": 1, "POST /graphql merged": 1}, counts, "scoped to #3")
	assert.Equal(t, full.Changes, scoped.Changes, "the same open changes, queued or not, so stacks read the same")
	assert.Equal(t, full.Unqueued, scoped.Unqueued)
}

// Reviews pages to the end: an approval past the first 100 reviews still counts.
func TestReviewsReadsEveryPage(t *testing.T) {
	_, counts := countingGitHub(t)
	p, err := Open(context.Background(), "github")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	got, err := p.Reviews(context.Background(), types.Change{ID: "3", Repo: "acme/widgets", Head: strings.Repeat("c", 40), Base: "main"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []types.Review{{ID: "12", Reviewer: "ann", Commit: strings.Repeat("d", 40)}, {ID: "13", Reviewer: "cy", Commit: strings.Repeat("e", 40)}}, got.Approving)
	assert.Equal(t, map[string]int{"POST /graphql reviews": 2}, counts)
}

// A script's print reaches the log display as a notice record, not a raw stderr write.
func TestPrintRecordsLogsOneNoticePerPrint(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	n, err := printRecords{ctx: context.Background()}.Write([]byte("checking #12\n"))
	require.NoError(t, err)
	assert.Equal(t, len("checking #12\n"), n)
	assert.Equal(t, "level=INFO msg=\"checking #12\" notice=\"\"\n", buf.String())
}
