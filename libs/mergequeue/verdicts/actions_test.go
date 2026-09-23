package verdicts

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
)

// fakeActions serves the three GitHub REST routes an ActionsRun reads, for run 7 of
// acme/widgets.
type fakeActions struct {
	t  *testing.T
	mu sync.Mutex
	// status is what the run route reports.
	status    string
	artifacts []fakeArtifact
	// afterStatus runs once the run route has answered, before the next request is
	// served: the window in which a real run can finish between two reads.
	afterStatus func(*fakeActions)
	failZip     bool
	requests    []string
}

type fakeArtifact struct {
	id   int64
	name string
	zip  []byte
}

func (f *fakeActions) add(name string, files map[string][]byte) {
	f.artifacts = append(f.artifacts, fakeArtifact{id: int64(len(f.artifacts) + 100), name: name, zip: zipOf(f.t, files)})
}

func (f *fakeActions) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req.URL.Path)
	if req.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return
	}
	const repo = "/repos/acme/widgets/actions/"
	switch p := req.URL.Path; {
	case p == repo+"runs/7":
		writeJSON(w, map[string]string{"status": f.status})
		if f.afterStatus != nil {
			f.afterStatus(f)
			f.afterStatus = nil
		}
	case p == repo+"runs/7/artifacts":
		page, _ := strconv.Atoi(req.URL.Query().Get("page"))
		per, _ := strconv.Atoi(req.URL.Query().Get("per_page"))
		type row struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		rows := []row{}
		for i := (page - 1) * per; i < len(f.artifacts) && i < page*per; i++ {
			rows = append(rows, row{f.artifacts[i].id, f.artifacts[i].name})
		}
		writeJSON(w, map[string]any{"total_count": len(f.artifacts), "artifacts": rows})
	case strings.HasPrefix(p, repo+"artifacts/") && strings.HasSuffix(p, "/zip"):
		if f.failZip {
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(p, repo+"artifacts/"), "/zip")
		for _, a := range f.artifacts {
			if strconv.FormatInt(a.id, 10) == id {
				_, _ = w.Write(a.zip)
				return
			}
		}
		http.NotFound(w, req)
	default:
		http.NotFound(w, req)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func planBytes(t *testing.T, ids ...string) []byte {
	t.Helper()
	var g []mergequeue.Change
	for _, id := range ids {
		g = append(g, change(id))
	}
	var buf bytes.Buffer
	require.NoError(t, mergequeue.WritePlan(&buf, mergequeue.Plan{
		Base: "main", BaseCommit: strings.Repeat("b", 40), Depth: 3, Partitions: [][]mergequeue.Change{g},
	}))
	return buf.Bytes()
}

func verdictFiles(t *testing.T, id string, bundle bool) map[string][]byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, mergequeue.WriteVerdict(&buf, mergequeue.Verdict{Change: change(id), Decision: mergequeue.DecisionWait}))
	files := map[string][]byte{VerdictFile: buf.Bytes()}
	if bundle {
		files[BundleFile] = []byte("bundle")
	}
	return files
}

func newRun(t *testing.T, f *fakeActions) *ActionsRun {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &ActionsRun{API: srv.URL + "/", Repo: "acme/widgets", RunID: "7", Token: "tok", Path: filepath.Join(t.TempDir(), "verdicts"), Follow: true, Interval: 1}
}

func ids(vs []mergequeue.Verdict) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.Change.ID)
	}
	return out
}

func TestActionsRunUnpacksArtifactsAcrossPollsAndIsDoneOnceTheRunCompletes(t *testing.T) {
	ctx := context.Background()
	f := &fakeActions{status: "in_progress"}
	r := newRun(t, f)
	f.add(PlanArtifact, map[string][]byte{PlanFile: planBytes(t, "1", "2")})

	plan, ok, err := r.Plan(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, plan.Partitions, 1)
	assert.Equal(t, []string{"1", "2"}, []string{plan.Partitions[0][0].ID, plan.Partitions[0][1].ID})

	fresh, done, err := r.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, fresh)
	assert.False(t, done)

	f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", true))
	fresh, done, err = r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, ids(fresh))
	assert.Equal(t, filepath.Join(r.Path, "1", BundleFile), fresh[0].Bundle)
	assert.False(t, done)

	f.add(VerdictArtifactPrefix+"2", verdictFiles(t, "2", false))
	f.status = "completed"
	fresh, done, err = r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"2"}, ids(fresh), "each verdict once")
	assert.Empty(t, fresh[0].Bundle)
	assert.True(t, done)
}

func TestActionsRunReadsTheStatusBeforeTheListingItTrusts(t *testing.T) {
	ctx := context.Background()
	f := &fakeActions{status: "in_progress"}
	r := newRun(t, f)
	// The run finishes, uploading its last verdict, after its status was read.
	f.afterStatus = func(f *fakeActions) {
		f.status = "completed"
		f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", false))
	}
	fresh, done, err := r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, ids(fresh))
	assert.False(t, done, "the status read predates the listing, so it vouches for nothing in it")
	assert.Equal(t, []string{
		"/repos/acme/widgets/actions/runs/7",
		"/repos/acme/widgets/actions/runs/7/artifacts",
		"/repos/acme/widgets/actions/artifacts/100/zip",
	}, f.requests)

	fresh, done, err = r.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, fresh)
	assert.True(t, done)
}

func TestActionsRunPagesThroughTheListing(t *testing.T) {
	f := &fakeActions{status: "completed"}
	r := newRun(t, f)
	for i := range artifactPage + 2 {
		f.add(fmt.Sprintf("%sc%d", VerdictArtifactPrefix, i), verdictFiles(t, fmt.Sprintf("c%d", i), false))
	}
	fresh, done, err := r.Poll(context.Background())
	require.NoError(t, err)
	assert.Len(t, fresh, artifactPage+2)
	assert.True(t, done)
}

func TestActionsRunWithoutFollowReadsOneListingOfARunInProgress(t *testing.T) {
	ctx := context.Background()
	f := &fakeActions{status: "in_progress"}
	r := newRun(t, f)
	r.Follow = false
	_, ok, err := r.Plan(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "no plan uploaded yet, and nothing waits for one")

	f.add(PlanArtifact, map[string][]byte{PlanFile: planBytes(t, "1", "2")})
	f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", false))
	_, ok, err = r.Plan(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	fresh, done, err := r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, ids(fresh))
	assert.True(t, done, "a run still going is complete as far as one pass reads")
}

func TestActionsRunWithNoPlanArtifactPlansNothing(t *testing.T) {
	f := &fakeActions{status: "completed"}
	r := newRun(t, f)
	_, ok, err := r.Plan(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
}

// An empty Path would unpack artifacts into the working directory.
func TestActionsRunRefusesAMissingRequiredField(t *testing.T) {
	for name, r := range map[string]*ActionsRun{
		"repo":   {RunID: "1", Path: t.TempDir()},
		"run id": {Repo: "acme/widgets", Path: t.TempDir()},
		"path":   {Repo: "acme/widgets", RunID: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := r.Poll(context.Background())
			require.ErrorContains(t, err, "an ActionsRun needs a Repo, a RunID and a Path")
		})
	}
}

func TestActionsRunStopsOnAFailedDownload(t *testing.T) {
	f := &fakeActions{status: "in_progress", failZip: true}
	r := newRun(t, f)
	f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", false))
	_, done, err := r.Poll(context.Background())
	require.ErrorContains(t, err, "artifact magus-queue-verdict-1: GET /repos/acme/widgets/actions/artifacts/100/zip: 500 Internal Server Error: storage unavailable")
	assert.False(t, done)
	assert.NoDirExists(t, filepath.Join(r.Path, "1"))
}

func TestActionsRunRefusesAnArtifactThatEscapesTheDirectory(t *testing.T) {
	f := &fakeActions{status: "in_progress"}
	r := newRun(t, f)
	f.add(VerdictArtifactPrefix+"..", verdictFiles(t, "1", false))
	_, _, err := r.Poll(context.Background())
	require.ErrorContains(t, err, `change id ".."`)

	f.artifacts = nil
	f.add(VerdictArtifactPrefix+"1", map[string][]byte{"../escape": []byte("x")})
	_, _, err = r.Poll(context.Background())
	require.ErrorContains(t, err, `entry "../escape" escapes the artifact`)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(r.Path), "escape"))
}
