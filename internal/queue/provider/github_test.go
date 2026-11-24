package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/queue/types"
)

// githubErrorSuite runs the GitHub provider against canned answers and
// reads each failure as the queue does.
type githubErrorSuite struct {
	suite.Suite
	ctx context.Context
}

func TestGitHubErrors(t *testing.T) { suite.Run(t, new(githubErrorSuite)) }

func (s *githubErrorSuite) SetupTest() { s.ctx = context.Background() }

type canned struct {
	key string
	answer
}

func on(key string, status int, body string) canned {
	return canned{key, answer{status, body}}
}

// remote serves the answers and returns the acme/widgets remote on that
// server, with the write token set.
func (s *githubErrorSuite) remote(served ...canned) string {
	answers := map[string]answer{}
	for _, c := range served {
		answers[c.key] = c.answer
	}
	remote := cannedGitHub(s.T(), answers)
	s.T().Setenv("MERGEQUEUE_TOKEN", "w")
	return remote
}

func (s *githubErrorSuite) open() *Script {
	p, err := Open(s.ctx, "github")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = p.Close() })
	return p
}

var widgets = types.Change{ID: "7", Repo: "acme/widgets", Head: headA, Base: "main", Title: "add x", Method: types.MethodSquash}

func (s *githubErrorSuite) TestDescribeNeedsACredential() {
	s.T().Setenv("GITHUB_TOKEN", "")
	s.T().Setenv("MERGEQUEUE_TOKEN", "")
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: "https://github.com/acme/widgets"})
	s.ErrorContains(err, "github: describe needs GITHUB_TOKEN or MERGEQUEUE_TOKEN, and neither is set")
}

func (s *githubErrorSuite) TestDescribeRefusesARemoteOfAnotherHost() {
	s.remote()
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: "https://elsewhere.example/acme/widgets"})
	s.ErrorContains(err, "github: the remote 'https://elsewhere.example/acme/widgets' is not a ")
}

func (s *githubErrorSuite) TestDescribeNamesTheStatusOfAFailedRepositoryRead() {
	remote := s.remote(on("GET /repos/acme/widgets", http.StatusInternalServerError, "boom"))
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote})
	s.ErrorContains(err, "github: describe acme/widgets: HTTP 500: boom")
}

func (s *githubErrorSuite) TestDescribeNamesThePermissionAForbiddenRulesReadLacks() {
	remote := s.remote(
		on("GET /repos/acme/widgets", 200, `{"allow_squash_merge": true}`),
		on("GET /repos/acme/widgets/rules/branches/main", http.StatusForbidden, "forbidden"),
	)
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote})
	s.ErrorContains(err, "github: read the rules on main: HTTP 403: either it does not exist or the token lacks metadata read on acme/widgets: forbidden")
}

func (s *githubErrorSuite) TestDescribeFailsWhenNoMergeMethodIsAllowed() {
	remote := s.remote(
		on("GET /repos/acme/widgets", 200, `{"allow_merge_commit": true, "allow_squash_merge": false, "allow_rebase_merge": false}`),
		on("GET /repos/acme/widgets/rules/branches/main", 200, `[{"type": "pull_request", "parameters": {"allowed_merge_methods": ["squash"]}}]`),
	)
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote})
	s.ErrorContains(err, "allow no merge method in common")
}

func (s *githubErrorSuite) TestDescribeFailsWhenTheTokenReadsNoMergeSettings() {
	remote := s.remote(
		on("GET /repos/acme/widgets", 200, `{"full_name": "acme/widgets"}`),
		on("GET /repos/acme/widgets/rules/branches/main", 200, `[]`),
	)
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote})
	s.ErrorContains(err, "the token cannot read the repository's merge settings (they need push access)")
}

func (s *githubErrorSuite) TestDescribeRefusesAnAppThatIsNotASlugAndAnID() {
	answers := repositoryAnswers("User")
	served := make([]canned, 0, len(answers))
	for key, a := range answers {
		served = append(served, canned{key, a})
	}
	remote := s.remote(served...)
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote, StatusContext: "merge-queue", App: ":2034567"})
	s.ErrorContains(err, "github: the app ':2034567' is not <slug> or <slug>:<App ID>")
}

func (s *githubErrorSuite) TestDescribeNamesABadAnswerOfTheBotRead() {
	answers := repositoryAnswers("User")
	answers["GET /users/q[bot]"] = answer{http.StatusInternalServerError, "down"}
	served := make([]canned, 0, len(answers))
	for key, a := range answers {
		served = append(served, canned{key, a})
	}
	remote := s.remote(served...)
	_, err := s.open().Describe(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote, StatusContext: "merge-queue", App: "q"})
	s.ErrorContains(err, "github: read the bot of the app q: HTTP 500: down")
}

func (s *githubErrorSuite) TestListChangesNamesTheStatusOfAFailedPullListing() {
	remote := s.remote(on("GET /repos/acme/widgets/pulls?state=open&sort=created&direction=asc&per_page=100&page=1", http.StatusInternalServerError, "down"))
	_, err := s.open().ListChanges(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote})
	s.ErrorContains(err, "github: list pull requests: HTTP 500: down")
}

func (s *githubErrorSuite) TestListChangesNamesAListingThatIsNotJSON() {
	remote := s.remote(on("GET /repos/acme/widgets/pulls?state=open&sort=created&direction=asc&per_page=100&page=1", 200, "not json"))
	_, err := s.open().ListChanges(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote})
	s.ErrorContains(err, "github: pages")
}

func (s *githubErrorSuite) TestListGreenNamesGraphQLsOwnErrors() {
	remote := s.remote(on("POST /graphql", 200, `{"errors": [{"message": "Resource limited"}]}`))
	_, err := s.open().ListGreen(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote}, "merge-queue")
	s.ErrorContains(err, "github: GraphQL: Resource limited")
}

func (s *githubErrorSuite) TestListGreenNamesTheStatusOfAFailedGraphQLCall() {
	remote := s.remote(on("POST /graphql", http.StatusBadGateway, "bad gateway"))
	_, err := s.open().ListGreen(s.ctx, types.ListQuery{Base: "main", RemoteURL: remote}, "merge-queue")
	s.ErrorContains(err, "github: GraphQL: HTTP 502: bad gateway")
}

func (s *githubErrorSuite) TestApprovalAtNamesAPullRequestItCannotRead() {
	s.remote(on("GET /repos/acme/widgets/pulls/7", http.StatusNotFound, `{"message": "Not Found"}`))
	_, err := s.open().ApprovalAt(s.ctx, widgets, headA)
	s.ErrorContains(err, `github: read #7: HTTP 404: {"message": "Not Found"}`)
}

func (s *githubErrorSuite) TestApprovalAtNeedsAnOwnerAndNameRepository() {
	s.remote()
	c := widgets
	c.Repo = "widgets"
	_, err := s.open().ApprovalAt(s.ctx, c, headA)
	s.ErrorContains(err, "github: approval_at needs an owner/name repo, got 'widgets'")
}

func (s *githubErrorSuite) TestApprovalAtReadsTheReviewsOfAQueuedChange() {
	pull := fmt.Sprintf(`{"head": {"sha": "%s", "ref": "feat", "repo": {"full_name": "acme/widgets", "owner": {"login": "acme"}}},
		"base": {"ref": "main"}, "auto_merge": {"merge_method": "squash"}}`, headA)
	reviews := fmt.Sprintf(`{"data": {"repository": {"pullRequest": {"headRefOid": "%s", "reviewDecision": "APPROVED",
		"reviews": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": [
		{"databaseId": 1, "state": "APPROVED", "authorCanPushToRepository": true, "author": {"login": "ann"}, "commit": {"oid": "%s"}}]}}}}}`, headA, headA)
	s.remote(
		on("GET /repos/acme/widgets/pulls/7", 200, pull),
		on("GET /repos/acme/widgets/rules/branches/main", 200, `[{"type": "pull_request", "parameters": {"required_approving_review_count": 1}}]`),
		on("POST /graphql", 200, reviews),
		on("GET /repos/acme/widgets/pulls?state=open&head=acme%3afeat&per_page=100&page=1", 200, `[{"number": 7}]`),
	)
	got, err := s.open().ApprovalAt(s.ctx, widgets, headA)
	s.Require().NoError(err)
	s.True(got.Approved)
	s.True(got.Queued)
	s.Equal("main", got.Base)
	s.Equal(types.MethodSquash, got.Method)
	s.Empty(got.BranchSharedWith)
}

func (s *githubErrorSuite) TestReviewsNeedsAnOwnerAndNameRepository() {
	s.remote()
	c := widgets
	c.Repo = "widgets"
	_, err := s.open().Reviews(s.ctx, c)
	s.ErrorContains(err, "github: reviews needs an owner/name repo, got 'widgets'")
}

// sequenced answers each request to /graphql with the next body.
func (s *githubErrorSuite) sequenced(bodies ...string) {
	next := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path != "/graphql" || next >= len(bodies) {
			s.T().Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body := bodies[next]
		next++
		_, _ = io.WriteString(w, body)
	}))
	s.T().Cleanup(srv.Close)
	s.T().Setenv("GITHUB_API_URL", srv.URL)
	s.T().Setenv("GITHUB_TOKEN", "t")
	s.T().Setenv("MERGEQUEUE_TOKEN", "")
}

func (s *githubErrorSuite) TestReviewsRefusesPagesThatBelongToTwoHeads() {
	page := func(head string, more bool) string {
		return fmt.Sprintf(`{"data": {"repository": {"pullRequest": {"headRefOid": "%s", "reviewDecision": "APPROVED",
			"reviews": {"pageInfo": {"hasNextPage": %t, "endCursor": "c1"}, "nodes": []}}}}}`, head, more)
	}
	s.sequenced(page("aaa", true), page("bbb", false))
	_, err := s.open().Reviews(s.ctx, widgets)
	s.ErrorContains(err, "github: #7 moved from aaa to bbb while its reviews were read")
}

func (s *githubErrorSuite) TestRequiredChecksNamesAFailedRulesRead() {
	s.remote(on("GET /repos/acme/widgets/rules/branches/main", http.StatusInternalServerError, "down"))
	_, err := s.open().RequiredChecks(s.ctx, widgets, headA)
	s.ErrorContains(err, "github: read the rules on main: HTTP 500: down")
}

func (s *githubErrorSuite) TestRequiredChecksStatesEachCheckAtTheCommit() {
	s.remote(
		on("GET /repos/acme/widgets/rules/branches/main", 200, `[{"type": "required_status_checks", "ruleset_id": 9, "parameters": {"required_status_checks": [{"context": "ci gate"}, {"context": "lint"}]}}]`),
		on("GET /repos/acme/widgets/branches/main", 200, `{"protection": {}}`),
		on("GET /repos/acme/widgets/actions/runs?head_sha="+headA+"&per_page=100", 200, `{"workflow_runs": [{"id": 5, "workflow_id": 1}]}`),
		on("GET /repos/acme/widgets/actions/runs/5/jobs?per_page=100", 200, `{"jobs": [{"name": "ci gate", "status": "completed", "conclusion": "success"}]}`),
		on("GET /repos/acme/widgets/commits/"+headA+"/status?per_page=100", 200, `{"statuses": []}`),
	)
	got, err := s.open().RequiredChecks(s.ctx, widgets, headA)
	s.Require().NoError(err)
	s.Equal([]types.Check{{Name: "ci gate", State: types.StateSuccess}, {Name: "lint", State: types.StatePending}}, got)
}

func (s *githubErrorSuite) TestPostStatusNeedsTheWriteCredential() {
	cannedGitHub(s.T(), map[string]answer{})
	err := s.open().PostStatus(s.ctx, widgets, headA, types.CommitStatus{Context: "merge-queue", State: types.StateSuccess})
	s.ErrorContains(err, "github: post_status writes, and needs MERGEQUEUE_TOKEN")
}

func (s *githubErrorSuite) TestPostStatusNamesAnUnexpectedStatus() {
	s.remote(on("POST /repos/acme/widgets/statuses/"+headA, http.StatusUnprocessableEntity, "nope"))
	err := s.open().PostStatus(s.ctx, widgets, headA, types.CommitStatus{Context: "merge-queue", State: types.StateSuccess})
	s.ErrorContains(err, "github: post_status: HTTP 422: nope")
}

func (s *githubErrorSuite) TestRetargetNamesAnUnexpectedStatus() {
	s.remote(on("PATCH /repos/acme/widgets/pulls/7", http.StatusUnprocessableEntity, "nope"))
	err := s.open().Retarget(s.ctx, widgets, "main")
	s.ErrorContains(err, "github: retarget: HTTP 422: nope")
}

func (s *githubErrorSuite) TestMergeChangeNeedsTheQueuesApp() {
	s.remote()
	_, err := s.open().MergeChange(s.ctx, widgets, types.MergeOptions{Commit: headA, Message: "* x"})
	s.ErrorContains(err, "github: merge_change merges only as the queue's own GitHub App, and names none")
}

func (s *githubErrorSuite) TestMergeChangeNeedsAMergeMethod() {
	s.remote(on("GET /repos/acme/widgets/pulls/7", 200, `{"auto_merge": null}`))
	c := widgets
	c.Method = ""
	_, err := s.open().MergeChange(s.ctx, c, types.MergeOptions{Commit: headA, Message: "* x", App: "q"})
	s.ErrorContains(err, "github: merge_change: the change carries no merge method")
}

func (s *githubErrorSuite) TestMergeChangeRefusesAStackWhoseMemberMoved() {
	s.remote(
		on("GET /repos/acme/widgets/pulls/7", 200, `{"auto_merge": null}`),
		on("GET /repos/acme/widgets/pulls/5", 200, fmt.Sprintf(`{"head": {"sha": "%s"}}`, headA)),
	)
	_, err := s.open().MergeChange(s.ctx, widgets, types.MergeOptions{Commit: headA, Message: "* x", App: "q",
		Through: []types.PinnedChange{{ID: "5", Commit: headE}}})
	s.ErrorContains(err, "not merged: #5 moved past the head it was validated at")
}

func (s *githubErrorSuite) TestDismissReviewNamesAnUnexpectedStatus() {
	s.remote(on("PUT /repos/acme/widgets/pulls/7/reviews/12/dismissals", http.StatusForbidden, "nope"))
	err := s.open().DismissReview(s.ctx, widgets, types.Review{ID: "12", Reviewer: "ann", Commit: headD}, "a.go changed")
	s.ErrorContains(err, "github: dismiss review 12: HTTP 403: nope")
}

func (s *githubErrorSuite) TestDismissReviewRefusesAReviewThatStandsAfterARefusal() {
	s.remote(
		on("PUT /repos/acme/widgets/pulls/7/reviews/12/dismissals", http.StatusUnprocessableEntity, "stale"),
		on("GET /repos/acme/widgets/pulls/7/reviews/12", 200, `{"id": 12, "state": "COMMENTED"}`),
	)
	err := s.open().DismissReview(s.ctx, widgets, types.Review{ID: "12", Reviewer: "ann", Commit: headD}, "a.go changed")
	s.ErrorContains(err, "github: dismiss review 12: GitHub refused it, and it is COMMENTED, not dismissed")
}

func (s *githubErrorSuite) TestDismissReviewAcceptsAReviewAlreadyDismissed() {
	s.remote(
		on("PUT /repos/acme/widgets/pulls/7/reviews/12/dismissals", http.StatusUnprocessableEntity, "stale"),
		on("GET /repos/acme/widgets/pulls/7/reviews/12", 200, `{"id": 12, "state": "DISMISSED"}`),
	)
	s.NoError(s.open().DismissReview(s.ctx, widgets, types.Review{ID: "12", Reviewer: "ann", Commit: headD}, "a.go changed"))
}

func (s *githubErrorSuite) TestMarkNamesAnInvalidLabelGitHubRefuses() {
	s.remote(
		on("GET /repos/acme/widgets/issues/7", 200, `{"labels": []}`),
		on("POST /repos/acme/widgets/labels", http.StatusUnprocessableEntity, `{"message": "Validation Failed", "errors": [{"code": "invalid", "field": "description"}]}`),
	)
	err := s.open().Mark(s.ctx, widgets, types.MarkQueued)
	s.ErrorContains(err, "github: create the label 'queue: queued': HTTP 422: Validation Failed (description invalid)")
}

func (s *githubErrorSuite) TestMarkAcceptsALabelTheRepositoryAlreadyHas() {
	s.remote(
		on("GET /repos/acme/widgets/issues/7", 200, `{"labels": []}`),
		on("POST /repos/acme/widgets/labels", http.StatusUnprocessableEntity, `{"message": "Validation Failed", "errors": [{"code": "already_exists", "field": "name"}]}`),
		on("POST /repos/acme/widgets/issues/7/labels", 200, `[]`),
	)
	s.NoError(s.open().Mark(s.ctx, widgets, types.MarkQueued))
}

func (s *githubErrorSuite) TestMarkNamesALabelItCannotRemove() {
	s.remote(
		on("GET /repos/acme/widgets/issues/7", 200, `{"labels": [{"name": "queue: kicked back"}]}`),
		on("DELETE /repos/acme/widgets/issues/7/labels/queue: kicked back", http.StatusInternalServerError, "down"),
	)
	err := s.open().Mark(s.ctx, widgets, types.MarkNone)
	s.ErrorContains(err, "github: remove the label 'queue: kicked back' from #7: HTTP 500: down")
}

func (s *githubErrorSuite) TestKickBackNamesAPullRequestItCannotRead() {
	s.remote(on("GET /repos/acme/widgets/pulls/7", http.StatusNotFound, `{"message": "Not Found"}`))
	err := s.open().KickBack(s.ctx, widgets, headA, types.Kick{Code: types.CodeKickConflict, Report: "report"})
	s.ErrorContains(err, `github: kick_back: read the pull request: HTTP 404: {"message": "Not Found"}`)
}

func (s *githubErrorSuite) TestListArtifactsNeedsTheArtifactsToken() {
	s.T().Setenv("MERGEQUEUE_ARTIFACTS_TOKEN", "")
	_, err := s.open().ListArtifacts(s.ctx, "acme/widgets/runs/7")
	s.ErrorContains(err, "github: list_artifacts reads with MERGEQUEUE_ARTIFACTS_TOKEN")
}

func (s *githubErrorSuite) TestListArtifactsRefusesASourceThatIsNotARun() {
	s.T().Setenv("MERGEQUEUE_ARTIFACTS_TOKEN", "a")
	_, err := s.open().ListArtifacts(s.ctx, "nonsense")
	s.ErrorContains(err, "github: a run is <owner>/<name>/runs/<id>, got 'nonsense'")
}

func (s *githubErrorSuite) TestListArtifactsRefusesARunOfAnotherRepository() {
	s.remote(on("GET /repos/acme/widgets/actions/runs/7", 200, `{"id": 7, "event": "push", "status": "completed", "repository": {"full_name": "acme/other"}}`))
	s.T().Setenv("MERGEQUEUE_ARTIFACTS_TOKEN", "a")
	_, err := s.open().ListArtifacts(s.ctx, "acme/widgets/runs/7")
	s.ErrorContains(err, "github: run 7 belongs to 'acme/other', not 'acme/widgets'")
}

// resultMark is the echo script with mark answering body, a Result.
func resultMark(body string) string {
	op := "export fun mark(req: QueueMarkRequest) > result\\Result::<Ack> {\n    " + body + "\n}\nfun spare(req: QueueMarkRequest) > Ack {"
	return "import \"result\";\n" + strings.Replace(script, "export fun mark(req: QueueMarkRequest) > Ack {", op, 1)
}

func (s *githubErrorSuite) TestAnOpThatReturnsAFailedResultIsTheCallsError() {
	p := open(s.T(), resultMark(`return result\Result::<Ack>{ error = result\Diagnostic{ code = "WS1", message = "refused" } };`))
	s.EqualError(p.Mark(s.ctx, widgets, types.MarkQueued), `provider "echo": mark: [WS1] refused`)
}

func (s *githubErrorSuite) TestAFailedResultWithoutACodeIsItsBareMessage() {
	p := open(s.T(), resultMark(`return result\fail::<Ack>("no way");`))
	s.EqualError(p.Mark(s.ctx, widgets, types.MarkQueued), `provider "echo": mark: no way`)
}

func (s *githubErrorSuite) TestAnOpThatReturnsASucceededResultAnswersItsValue() {
	p := open(s.T(), resultMark(`return result\ok::<Ack>(Ack{ ok = true });`))
	s.NoError(p.Mark(s.ctx, widgets, types.MarkQueued))
}

func (s *githubErrorSuite) TestAResultWithNeitherAValueNorAnErrorIsMalformed() {
	p := open(s.T(), resultMark(`return result\Result::<Ack>{};`))
	s.EqualError(p.Mark(s.ctx, widgets, types.MarkQueued), `provider "echo": mark: the Result carries neither a value nor an error`)
}

func (s *githubErrorSuite) TestAFailureCarryingNoMessageIsMalformed() {
	p := open(s.T(), resultMark(`return result\Result::<Ack>{ error = result\Diagnostic{ code = "WS1" } };`))
	s.EqualError(p.Mark(s.ctx, widgets, types.MarkQueued), `provider "echo": mark: error: a failed Result carries no message`)
}
