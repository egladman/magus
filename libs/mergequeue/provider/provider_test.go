package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
)

// The GitHub provider's own `test` blocks, run on the same VM surface the queue gives it.
func TestGitHubProviderBuzzTests(t *testing.T) {
	ctx := context.Background()
	sess, err := NewSession(ctx)
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

func TestTheGitHubProviderLoadsByName(t *testing.T) {
	p, err := Load(context.Background(), "github")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	assert.Equal(t, "github", p.Name())
}

// script is a provider whose ops echo what they were handed, so a test reads the
// bridge's encoding from the answers.
const script = `
import "std";

export fun list_queue(io: {str: any}) > [any] {
    return [{
        "id": "7", "repo": "acme/acme", "head": "abc", "ref": "refs/pull/7/head",
        "branch": "feat", "base": "{io["base"]}", "title": "{io["remote"]}", "author": "priya", "fork": true,
    }];
}

export fun approval_at(io: {str: any}) > any {
    return {"approved": io["id"] == "7", "head": io["sha"], "required": 1, "approvals": 2, "reason": "{io["title"]}"};
}

export fun post_status(io: {str: any}) > bool {
    return io["context"] == "merge-queue" and io["state"] == "success";
}

export fun merge_change(io: {str: any}) > any {
    return {"merged": io["message"] == "* body", "reason": "head moved"};
}

export fun kick_back(io: {str: any}) > bool {
    return io["body"] == "report";
}
`

func open(t *testing.T, src string) *Buzz {
	t.Helper()
	p, err := Open(context.Background(), "echo", src)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

var change = mergequeue.Change{ID: "7", Repo: "acme/acme", Head: "abc", Base: "main", Title: "add x"}

func TestListDecodesEveryField(t *testing.T) {
	got, err := open(t, script).List(context.Background(), mergequeue.ListQuery{Base: "main", Remote: "git@github.com:acme/acme.git"})
	require.NoError(t, err)
	assert.Equal(t, []mergequeue.Change{{
		ID: "7", Repo: "acme/acme", Head: "abc", Ref: "refs/pull/7/head", Branch: "feat",
		Base: "main", Title: "git@github.com:acme/acme.git", Author: "priya", Fork: true,
	}}, got)
}

func TestListRefusesAChangeWithoutAHead(t *testing.T) {
	p := open(t, strings.Replace(script, `"head": "abc", `, "", 1))
	_, err := p.List(context.Background(), mergequeue.ListQuery{Base: "main"})
	require.ErrorContains(t, err, "needs both id and head")
}

func TestApprovalDecodesAtTheCommitAsked(t *testing.T) {
	got, err := open(t, script).ApprovalAt(context.Background(), change, "def")
	require.NoError(t, err)
	assert.Equal(t, mergequeue.Approval{Approved: true, Head: "def", Required: 1, Approvals: 2, Reason: "add x"}, got)
}

func TestApprovalOfTheWrongTypeNamesTheField(t *testing.T) {
	p := open(t, strings.Replace(script, `"approved": io["id"] == "7"`, `"approved": "yes"`, 1))
	_, err := p.ApprovalAt(context.Background(), change, "abc")
	require.ErrorContains(t, err, `field "approved" is string, want bool`)
}

func TestWritesCarryTheirParametersAndARefusalIsAnError(t *testing.T) {
	p, ctx := open(t, script), context.Background()
	require.NoError(t, p.PostStatus(ctx, change, "abc", mergequeue.Status{Context: "merge-queue", State: mergequeue.StateSuccess}))
	require.ErrorContains(t, p.PostStatus(ctx, change, "abc", mergequeue.Status{Context: "other", State: mergequeue.StateSuccess}), "the host refused")
	require.NoError(t, p.Merge(ctx, change, "abc", "* body"))
	require.ErrorContains(t, p.Merge(ctx, change, "abc", "* other"), "not merged: head moved")
	require.NoError(t, p.KickBack(ctx, change, "abc", "report"))
	require.ErrorContains(t, p.KickBack(ctx, change, "abc", "other"), "the host refused")
}

func TestAScriptMissingAnOpIsRefused(t *testing.T) {
	_, err := Open(context.Background(), "half", `export fun list_queue(io: {str: any}) > [any] { return []; }`)
	require.EqualError(t, err, `provider "half" does not export approval_at, post_status, merge_change, kick_back`)
}

func TestAnUnknownProviderNamesBothPlacesItLooked(t *testing.T) {
	_, err := Load(context.Background(), "gitlab")
	require.ErrorContains(t, err, `provider "gitlab": not built in`)
}

func TestHostRequestSendsHeadersAndReturnsStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, r.Method+" "+r.Header.Get("Authorization")+" "+string(body))
	}))
	t.Cleanup(srv.Close)
	src := strings.NewReplacer(
		`import "std";`, `import "std";
import "mergequeue";`,
		`export fun kick_back(io: {str: any}) > bool {
    return io["body"] == "report";`, `export fun kick_back(io: {str: any}) > bool !> any {
    final res = mergequeue\request("POST", url: "`+srv.URL+`", body: "hi", headers: {"Authorization": "Bearer t"});
    return res["status"] == 201 and res["body"] == "POST Bearer t hi";`,
	).Replace(script)
	require.NoError(t, open(t, src).KickBack(context.Background(), change, "abc", ""))
}
