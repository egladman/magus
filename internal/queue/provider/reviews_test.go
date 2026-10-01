package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
)

// countingGitHub serves acme/widgets with three open pull requests, #1 and #2 queued by
// auto-merge and #3 under review with two pages of reviews, and counts every request by
// the read it is. It returns the remote URL and the counts.
func countingGitHub(t *testing.T) (string, map[string]int) {
	t.Helper()
	pr := func(n, sha, merge string) string {
		auto := `null`
		if merge != "" {
			auto = `{"merge_method": "` + merge + `"}`
		}
		return `{"number": ` + n + `, "draft": false, "title": "t", "user": {"login": "u"}, "labels": [],
			"head": {"sha": "` + strings.Repeat(sha, 40) + `", "ref": "b` + n + `", "repo": {"full_name": "acme/widgets"}},
			"base": {"ref": "main"}, "auto_merge": ` + auto + `}`
	}
	pulls := "[" + pr("1", "a", "squash") + "," + pr("2", "b", "squash") + "," + pr("3", "c", "") + "]"
	review := func(id, who, oid string) string {
		return `{"databaseId": ` + id + `, "state": "APPROVED", "authorCanPushToRepository": true, "author": {"login": "` + who + `"}, "commit": {"oid": "` + oid + `"}}`
	}
	page := func(next, cursor, nodes string) string {
		return `{"data": {"repository": {"pullRequest": {"headRefOid": "` + strings.Repeat("c", 40) + `", "reviewDecision": "APPROVED",
			"reviews": {"pageInfo": {"hasNextPage": ` + next + `, "endCursor": "` + cursor + `"}, "nodes": [` + nodes + `]}}}}}`
	}
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key, answer := r.Method+" "+r.URL.Path, ""
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
	return "https://" + strings.TrimPrefix(srv.URL, "http://") + "/acme/widgets", counts
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
