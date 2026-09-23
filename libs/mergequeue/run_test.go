package mergequeue

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunReader is a CI system's run: artifacts served as zips, listed with a status
// read before the listing.
type fakeRunReader struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	// completed is what the next listing reports.
	completed bool
	artifacts []Artifact
	zips      map[string][]byte
	// afterStatus runs once the next listing has read its status: the window in which a
	// real run can finish between the two reads.
	afterStatus func()
	failZip     bool
	fetched     []string
}

func newRunReader(t *testing.T) *fakeRunReader {
	f := &fakeRunReader{t: t, zips: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.fetched = append(f.fetched, req.URL.Path)
		body, ok := f.zips[req.URL.Path]
		switch {
		case req.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "bad credentials", http.StatusUnauthorized)
		case f.failZip:
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
		case !ok:
			http.NotFound(w, req)
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRunReader) add(name string, files map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := fmt.Sprintf("/artifacts/%d.zip", len(f.artifacts))
	f.zips[p] = zipOf(f.t, files)
	f.artifacts = append(f.artifacts, Artifact{Name: name, URL: f.srv.URL + p})
}

func (f *fakeRunReader) RunArtifacts(_ context.Context, run string) (RunArtifacts, error) {
	if run != "acme/widgets/runs/7" {
		return RunArtifacts{}, errors.New("no such run")
	}
	f.mu.Lock()
	completed := f.completed
	after := f.afterStatus
	f.afterStatus = nil
	f.mu.Unlock()
	if after != nil {
		after()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return RunArtifacts{Completed: completed, Headers: map[string]string{"Authorization": "Bearer tok"}, Artifacts: append([]Artifact(nil), f.artifacts...)}, nil
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
	var g []Change
	for _, id := range ids {
		g = append(g, change(id))
	}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, Plan{Base: "main", BaseCommit: base, Depth: 3, Partitions: [][]Change{g}}))
	return buf.Bytes()
}

func verdictFiles(t *testing.T, id string, built bool) map[string][]byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, WriteVerdict(&buf, waiting(id)))
	files := map[string][]byte{VerdictFile: buf.Bytes()}
	if built {
		files[CandidateFile] = []byte("candidate")
	}
	return files
}

func follow(t *testing.T, f *fakeRunReader) *RunFollower {
	t.Helper()
	return &RunFollower{Reader: f, Run: "acme/widgets/runs/7", Path: filepath.Join(t.TempDir(), "verdicts"), Follow: true, Interval: 1}
}

func verdictIDs(vs []Verdict) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.Change.ID)
	}
	return out
}

func TestRunFollowerUnpacksArtifactsAcrossPollsAndIsDoneOnceTheRunCompletes(t *testing.T) {
	ctx := context.Background()
	f := newRunReader(t)
	r := follow(t, f)
	f.add(PlanArtifact, map[string][]byte{PlanFile: planBytes(t, "1", "2")})

	plan, ok, err := r.Plan(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, [][]string{{"1", "2"}}, ids(plan.Partitions))

	fresh, done, err := r.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, fresh)
	assert.False(t, done)

	f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", true))
	fresh, done, err = r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, verdictIDs(fresh))
	assert.Equal(t, filepath.Join(r.Path, "1", CandidateFile), fresh[0].CandidateFile)
	assert.False(t, done)

	f.add(VerdictArtifactPrefix+"2", verdictFiles(t, "2", false))
	f.completed = true
	fresh, done, err = r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"2"}, verdictIDs(fresh), "each verdict once")
	assert.Empty(t, fresh[0].CandidateFile)
	assert.True(t, done)
	assert.Len(t, f.fetched, 3, "each artifact is downloaded once")
}

// A listing is trusted to be complete only when its own status read said so.
func TestRunFollowerIsDoneOnlyAfterAListingThatSaidTheRunCompleted(t *testing.T) {
	ctx := context.Background()
	f := newRunReader(t)
	r := follow(t, f)
	f.afterStatus = func() {
		f.completed = true
		f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", false))
	}
	fresh, done, err := r.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, verdictIDs(fresh))
	assert.False(t, done, "the status read predates the listing, so it vouches for nothing in it")

	fresh, done, err = r.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, fresh)
	assert.True(t, done)
}

func TestRunFollowerWithoutFollowReadsOneListingOfARunInProgress(t *testing.T) {
	ctx := context.Background()
	f := newRunReader(t)
	r := follow(t, f)
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
	assert.Equal(t, []string{"1"}, verdictIDs(fresh))
	assert.True(t, done, "a run still going is complete as far as one pass reads")
}

func TestRunFollowerWithNoPlanArtifactPlansNothing(t *testing.T) {
	f := newRunReader(t)
	f.completed = true
	_, ok, err := follow(t, f).Plan(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
}

// An empty Path would unpack artifacts into the working directory.
func TestRunFollowerRefusesAMissingRequiredField(t *testing.T) {
	f := newRunReader(t)
	for name, r := range map[string]*RunFollower{
		"reader": {Run: "1", Path: t.TempDir()},
		"run":    {Reader: f, Path: t.TempDir()},
		"path":   {Reader: f, Run: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := r.Poll(context.Background())
			require.EqualError(t, err, "a RunFollower needs a Reader, a Run and a Path")
		})
	}
}

func TestRunFollowerStopsOnAFailedDownload(t *testing.T) {
	f := newRunReader(t)
	f.failZip = true
	r := follow(t, f)
	f.add(VerdictArtifactPrefix+"1", verdictFiles(t, "1", false))
	_, done, err := r.Poll(context.Background())
	require.ErrorContains(t, err, "artifact magus-queue-verdict-1: GET /artifacts/0.zip: 500 Internal Server Error: storage unavailable")
	assert.False(t, done)
	assert.NoDirExists(t, filepath.Join(r.Path, "1"))
}

func TestRunFollowerRefusesAnArtifactThatEscapesTheDirectory(t *testing.T) {
	f := newRunReader(t)
	r := follow(t, f)
	f.add(VerdictArtifactPrefix+"..", verdictFiles(t, "1", false))
	_, _, err := r.Poll(context.Background())
	require.ErrorContains(t, err, `change id ".."`)

	f.artifacts = nil
	f.add(VerdictArtifactPrefix+"1", map[string][]byte{"../escape": []byte("x")})
	_, _, err = r.Poll(context.Background())
	require.ErrorContains(t, err, `entry "../escape" escapes the artifact`)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(r.Path), "escape"))
}

func TestRunFollowerDownloadsOnlyOverHTTP(t *testing.T) {
	f := newRunReader(t)
	r := follow(t, f)
	f.artifacts = []Artifact{{Name: VerdictArtifactPrefix + "1", URL: "file:///etc/passwd"}}
	_, _, err := r.Poll(context.Background())
	require.ErrorContains(t, err, `"file:///etc/passwd" is not an http or https URL`)
}

// The listing's headers carry the provider's credential, whatever header it rides in;
// the storage host a download redirects to must see none of them.
func TestRunFollowerDropsTheListingsHeadersOnARedirectToAnotherHost(t *testing.T) {
	var seen []string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = append(seen, req.Header.Get("Authorization")+"|"+req.Header.Get("Private-Token"))
		_, _ = w.Write(zipOf(t, verdictFiles(t, "1", false)))
	}))
	t.Cleanup(storage.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, strings.Replace(storage.URL, "127.0.0.1", "localhost", 1)+"/blob", http.StatusFound)
	}))
	t.Cleanup(api.Close)
	reader := &staticRun{list: RunArtifacts{Completed: true,
		Headers:   map[string]string{"Authorization": "Bearer tok", "Private-Token": "tok"},
		Artifacts: []Artifact{{Name: VerdictArtifactPrefix + "1", URL: api.URL + "/zip"}}}}
	r := &RunFollower{Reader: reader, Run: "r", Path: filepath.Join(t.TempDir(), "v"), Follow: true}
	fresh, _, err := r.Poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, verdictIDs(fresh))
	assert.Equal(t, []string{"|"}, seen)
}

type staticRun struct{ list RunArtifacts }

func (s *staticRun) RunArtifacts(context.Context, string) (RunArtifacts, error) { return s.list, nil }
