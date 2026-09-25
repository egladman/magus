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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
	"github.com/egladman/magus/libs/mergequeue/types/gen/mocks"
	magustypes "github.com/egladman/magus/types"
)

// run is the storage behind one CI run's listing: zips served over https to a request
// carrying the listing's credential.
type run struct {
	t       *testing.T
	srv     *httptest.Server
	lister  *mocks.MockArtifactLister
	mu      sync.Mutex
	zips    map[string][]byte
	failZip map[string]bool
	fetched []string
}

const (
	runID    = "acme/widgets/runs/7"
	workflow = ".github/workflows/queue.yaml"
)

// trusted is the origin of a run main's own queue workflow made.
var trusted = types.RunOrigin{Repo: "acme/widgets", HeadRepo: "acme/widgets", HeadBranch: "main", Event: "push", BranchEvent: true, Definition: workflow}

func newRun(t *testing.T) *run {
	r := &run{t: t, lister: mocks.NewMockArtifactLister(t), zips: map[string][]byte{}, failZip: map[string]bool{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.fetched = append(r.fetched, req.URL.Path)
		body, ok := r.zips[req.URL.Path]
		switch {
		case req.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "bad credentials", http.StatusUnauthorized)
		case r.failZip[req.URL.Path]:
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
		case !ok:
			http.NotFound(w, req)
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// artifact stores files as artifact name and returns it as a listing names it.
func (r *run) artifact(name string, files map[string][]byte) types.Artifact {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := fmt.Sprintf("/artifacts/%d.zip", len(r.zips))
	r.zips[p] = zipOf(r.t, files)
	return types.Artifact{Name: name, URL: r.srv.URL + p}
}

// lists expects one more listing of the run, holding artifacts.
func (r *run) lists(complete bool, artifacts ...types.Artifact) {
	r.lister.EXPECT().ListArtifacts(mock.Anything, runID).Return(types.ArtifactListing{Run: trusted, Complete: complete,
		Headers: map[string]string{"Authorization": "Bearer tok"}, Artifacts: artifacts}, nil).Once()
}

func (r *run) follower() *ArtifactFollower {
	return &ArtifactFollower{Lister: r.lister, Source: runID, Path: filepath.Join(r.t.TempDir(), "verdicts"), Branch: "main", Definition: workflow,
		Follow: true, Interval: 1, Client: r.srv.Client()}
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
	var g []types.Change
	for _, id := range ids {
		g = append(g, change(id))
	}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, types.Plan{Base: "main", BaseCommit: base, Depth: 3, Partitions: [][]types.Change{g}}))
	return buf.Bytes()
}

func verdictFiles(t *testing.T, id string) map[string][]byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, writeVerdict(&buf, waiting(id)))
	return map[string][]byte{VerdictFile: buf.Bytes()}
}

func TestArtifactFollowerUnpacksArtifactsAcrossPollsAndIsDoneOnceTheRunCompletes(t *testing.T) {
	ctx := context.Background()
	r := newRun(t)
	f := r.follower()
	plan := r.artifact(PlanArtifact, map[string][]byte{PlanFile: planBytes(t, "1", "2")})
	one := r.artifact(VerdictArtifactPrefix+"1", verdictFiles(t, "1"))
	two := r.artifact(VerdictArtifactPrefix+"2", verdictFiles(t, "2"))
	r.lists(false, plan)
	r.lists(false, plan)
	r.lists(false, plan, one)
	r.lists(true, plan, one, two)

	got, ok, err := f.Plan(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, [][]string{{"1", "2"}}, ids(got.Partitions))

	batch, err := f.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, batch.Verdicts)
	assert.False(t, batch.Done)

	batch, err = f.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, idsOf(batch.Verdicts))
	assert.False(t, batch.Done)

	batch, err = f.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"2"}, idsOf(batch.Verdicts), "each verdict once")
	assert.True(t, batch.Done)
	assert.Len(t, r.fetched, 3, "each artifact is downloaded once")
}

// A listing is trusted to be complete only when its own status read said so: a lister
// reads the status before the artifacts, so a verdict uploaded between the two reads
// arrives in a listing that is not yet complete.
func TestArtifactFollowerIsDoneOnlyAfterAListingThatSaidTheRunCompleted(t *testing.T) {
	ctx := context.Background()
	r := newRun(t)
	f := r.follower()
	one := r.artifact(VerdictArtifactPrefix+"1", verdictFiles(t, "1"))
	r.lists(false, one)
	r.lists(true, one)
	batch, err := f.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, idsOf(batch.Verdicts))
	assert.False(t, batch.Done)

	batch, err = f.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, batch.Verdicts)
	assert.True(t, batch.Done)
}

func TestArtifactFollowerWithoutFollowReadsOneListingOfARunInProgress(t *testing.T) {
	ctx := context.Background()
	r := newRun(t)
	f := r.follower()
	f.Follow = false
	r.lists(false)
	_, ok, err := f.Plan(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "no plan uploaded yet, and nothing waits for one")

	plan := r.artifact(PlanArtifact, map[string][]byte{PlanFile: planBytes(t, "1", "2")})
	one := r.artifact(VerdictArtifactPrefix+"1", verdictFiles(t, "1"))
	r.lists(false, plan, one)
	r.lists(false, plan, one)
	_, ok, err = f.Plan(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	batch, err := f.Poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, idsOf(batch.Verdicts))
	assert.True(t, batch.Done, "a run still going is complete as far as one pass reads")
}

func TestArtifactFollowerWithNoPlanArtifactPlansNothing(t *testing.T) {
	r := newRun(t)
	r.lists(true)
	_, ok, err := r.follower().Plan(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestArtifactFollowerStopsWhenTheListingFails(t *testing.T) {
	r := newRun(t)
	r.lister.EXPECT().ListArtifacts(mock.Anything, runID).Return(types.ArtifactListing{}, errors.New("rate limited"))
	_, err := r.follower().Poll(context.Background())
	require.EqualError(t, err, "run "+runID+": rate limited")
}

// An empty Path would unpack artifacts into the working directory.
func TestArtifactFollowerRefusesAMissingRequiredField(t *testing.T) {
	lister := mocks.NewMockArtifactLister(t)
	for name, f := range map[string]*ArtifactFollower{
		"lister":     {Source: "1", Path: t.TempDir(), Branch: "main", Definition: workflow},
		"source":     {Lister: lister, Path: t.TempDir(), Branch: "main", Definition: workflow},
		"path":       {Lister: lister, Source: "1", Branch: "main", Definition: workflow},
		"branch":     {Lister: lister, Source: "1", Path: t.TempDir(), Definition: workflow},
		"definition": {Lister: lister, Source: "1", Path: t.TempDir(), Branch: "main"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.Poll(context.Background())
			require.EqualError(t, err, "artifact follower needs a lister, a source, a path, a branch and a definition")
		})
	}
}

// A run a pull request started ran the pull request's own workflow file, which can
// upload any plan and any verdict; so can a fork's run, another branch's, and another
// workflow's. Each is refused on its first listing, before anything is downloaded.
func TestArtifactFollowerRefusesARunTheBaseBranchsQueueWorkflowDidNotMake(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(o *types.RunOrigin)
		want string
	}{
		"a pull request's event": {func(o *types.RunOrigin) { o.Event, o.BranchEvent = "pull_request", false },
			`"pull_request" started it, and that event runs a definition a change supplied`},
		"a review's event": {func(o *types.RunOrigin) { o.Event, o.BranchEvent = "pull_request_review", false },
			`"pull_request_review" started it, and that event runs a definition a change supplied`},
		"a fork": {func(o *types.RunOrigin) { o.HeadRepo = "mallory/widgets" },
			`it ran a commit of "mallory/widgets", not of "acme/widgets"`},
		"another branch": {func(o *types.RunOrigin) { o.HeadBranch = "feature" },
			`it ran on "feature", not "main"`},
		"another workflow": {func(o *types.RunOrigin) { o.Definition = ".github/workflows/evil.yaml" },
			`it ran ".github/workflows/evil.yaml", not ".github/workflows/queue.yaml"`},
		"no repository": {func(o *types.RunOrigin) { o.Repo, o.HeadRepo = "", "" },
			"the provider named no repository for it"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRun(t)
			origin := trusted
			tc.edit(&origin)
			forged := r.artifact(PlanArtifact, map[string][]byte{PlanFile: planBytes(t, "1")})
			verdict := r.artifact(VerdictArtifactPrefix+"1", verdictFiles(t, "1"))
			r.lister.EXPECT().ListArtifacts(mock.Anything, runID).Return(types.ArtifactListing{Run: origin, Complete: true,
				Headers: map[string]string{"Authorization": "Bearer tok"}, Artifacts: []types.Artifact{forged, verdict}}, nil).Once()
			f := r.follower()
			_, _, err := f.Plan(context.Background())
			var diag *magustypes.DiagnosticError
			require.ErrorAs(t, err, &diag)
			assert.Equal(t, magustypes.QueueRunUntrusted, diag.Code)
			assert.ErrorContains(t, err, tc.want+"; apply reads nothing it uploaded")
			assert.Empty(t, r.fetched, "nothing of the run is downloaded")
			assert.NoFileExists(t, filepath.Join(f.Path, PlanFile))
		})
	}
}

// One artifact that cannot be fetched or unpacked holds its change alone; before, it
// stopped applying for the whole run.
func TestAnArtifactThatFailsIsRejectedForItsChangeAlone(t *testing.T) {
	r := newRun(t)
	f := r.follower()
	failing := r.artifact(VerdictArtifactPrefix+"1", verdictFiles(t, "1"))
	r.failZip[strings.TrimPrefix(failing.URL, r.srv.URL)] = true
	escaping := r.artifact(VerdictArtifactPrefix+"2", map[string][]byte{"../escape": []byte("x")})
	dots := r.artifact(VerdictArtifactPrefix+"..", verdictFiles(t, "3"))
	four := r.artifact(VerdictArtifactPrefix+"4", verdictFiles(t, "4"))
	r.lists(false, failing, escaping, dots, four)
	r.lists(false, failing, escaping, dots, four)
	batch, err := f.Poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"4"}, idsOf(batch.Verdicts))
	reasons := map[string]string{}
	for _, rej := range batch.Rejected {
		reasons[rej.Change] = rej.Reason
	}
	assert.Contains(t, reasons["1"], "artifact mergequeue-verdict-1: GET /artifacts/0.zip: 500 Internal Server Error: storage unavailable")
	assert.Contains(t, reasons["2"], `entry "../escape" escapes the artifact`)
	assert.Contains(t, reasons[".."], `change id ".."`)
	assert.NoDirExists(t, filepath.Join(f.Path, "1"))
	assert.NoFileExists(t, filepath.Join(filepath.Dir(f.Path), "escape"))

	again, err := f.Poll(context.Background())
	require.NoError(t, err)
	assert.Empty(t, again.Rejected, "a failed artifact is not fetched again")
}

func TestArtifactFollowerDownloadsOnlyOverHTTPS(t *testing.T) {
	r := newRun(t)
	f := r.follower()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a plain http server was asked")
	}))
	t.Cleanup(plain.Close)
	r.lists(false, types.Artifact{Name: VerdictArtifactPrefix + "1", URL: plain.URL + "/zip"}, types.Artifact{Name: VerdictArtifactPrefix + "2", URL: "file:///etc/passwd"})
	batch, err := f.Poll(context.Background())
	require.NoError(t, err)
	require.Len(t, batch.Rejected, 2)
	assert.Contains(t, batch.Rejected[0].Reason, "is not an https URL")
	assert.Contains(t, batch.Rejected[1].Reason, `"file:///etc/passwd" is not an https URL`)
}

// The listing's headers carry the provider's credential, whatever header it rides in;
// the storage host a download redirects to must see none of them, and a redirect off
// https is not followed at all.
func TestArtifactFollowerKeepsTheListingsHeadersFromAnotherHostAndOffHTTPS(t *testing.T) {
	var seen []string
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = append(seen, req.Header.Get("Authorization")+"|"+req.Header.Get("Private-Token"))
		_, _ = w.Write(zipOf(t, verdictFiles(t, "1")))
	}))
	t.Cleanup(storage.Close)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Error("a redirect off https was followed with " + req.Header.Get("Authorization"))
	}))
	t.Cleanup(plain.Close)
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/downgrade" {
			http.Redirect(w, req, plain.URL+"/blob", http.StatusFound)
			return
		}
		http.Redirect(w, req, strings.Replace(storage.URL, "127.0.0.1", "localhost", 1)+"/blob", http.StatusFound)
	}))
	t.Cleanup(api.Close)
	lister := mocks.NewMockArtifactLister(t)
	lister.EXPECT().ListArtifacts(mock.Anything, "r").Return(types.ArtifactListing{Run: trusted, Complete: true,
		Headers: map[string]string{"Authorization": "Bearer tok", "Private-Token": "tok"},
		Artifacts: []types.Artifact{{Name: VerdictArtifactPrefix + "1", URL: api.URL + "/zip"},
			{Name: VerdictArtifactPrefix + "2", URL: api.URL + "/downgrade"}}}, nil)
	f := &ArtifactFollower{Lister: lister, Source: "r", Path: filepath.Join(t.TempDir(), "v"), Branch: "main", Definition: workflow, Follow: true, Client: api.Client()}
	f.Client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true // the storage server's own certificate
	batch, err := f.Poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, idsOf(batch.Verdicts))
	assert.Equal(t, []string{"|"}, seen)
	require.Len(t, batch.Rejected, 1)
	assert.Contains(t, batch.Rejected[0].Reason, "which is not https")
}

func TestAnArtifactPastItsBoundsIsRejected(t *testing.T) {
	r := newRun(t)
	f := r.follower()
	many := map[string][]byte{}
	for i := range maxArtifactFiles + 1 {
		many[fmt.Sprint(i)] = nil
	}
	big := bytes.Repeat([]byte{0}, maxUnpackedBytes+1)
	r.lists(false, r.artifact(VerdictArtifactPrefix+"1", many), r.artifact(VerdictArtifactPrefix+"2", map[string][]byte{VerdictFile: big}))
	batch, err := f.Poll(context.Background())
	require.NoError(t, err)
	require.Len(t, batch.Rejected, 2)
	assert.Contains(t, batch.Rejected[0].Reason, "more than 64")
	assert.Contains(t, batch.Rejected[1].Reason, "unpacks to more than")
}
