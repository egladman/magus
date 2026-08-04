package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/dependency"
	"github.com/egladman/magus/types"
)

const (
	testFullHash  = "4cfedce2aa7a510f5fcbd4fd530e8d220edd36be"
	testShortHash = "4cfedce2"
)

func testMeta() types.VCSMeta {
	return types.VCSMeta{Hash: testFullHash, ShortHash: testShortHash}
}

// TestContainsShortHash pins the token rule. An abbreviated hash is only a match when it
// stands alone: without the boundary test, every file holding ANY longer hex id whose middle
// happens to spell the abbreviation would report as self-staling, and long hex ids are
// everywhere (lockfile digests, SRI hashes, test fixtures).
func TestContainsShortHash(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"standalone token", "built from " + testShortHash + " today", true},
		{"at end of file", "commit " + testShortHash, true},
		{"inside a longer hash", "sha256:" + testShortHash + "aa99bb00cc11dd22", false},
		{"preceded by hex", "ff" + testShortHash, false},
		{"absent", "no identifiers here at all", false},
		{"empty body", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, containsShortHash([]byte(tc.body), testShortHash))
		})
	}
}

// TestFileRecordsCommit covers what the scan will and will not read. The skips are the
// difference between a health check and a build step on a workspace with thousands of
// declared outputs.
func TestFileRecordsCommit(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, body, 0o644))
		return p
	}

	t.Run("full hash matches", func(t *testing.T) {
		p := write("full.html", []byte("<small>Last updated ("+testFullHash+")</small>"))
		assert.True(t, fileRecordsCommit(p, testMeta()))
	})

	t.Run("short hash matches", func(t *testing.T) {
		p := write("short.html", []byte("<small>built "+testShortHash+"</small>"))
		assert.True(t, fileRecordsCommit(p, testMeta()))
	})

	t.Run("a different repository's commit does not match", func(t *testing.T) {
		p := write("lock.json", []byte(`{"rev":"0123456789abcdef0123456789abcdef01234567"}`))
		assert.False(t, fileRecordsCommit(p, testMeta()))
	})

	t.Run("binary content is skipped", func(t *testing.T) {
		p := write("bundle.wasm", append([]byte{0x00, 0x61, 0x73, 0x6d, 0x00}, []byte(testFullHash)...))
		assert.False(t, fileRecordsCommit(p, testMeta()), "a NUL in the first KiB means do not search it")
	})

	t.Run("oversized files are skipped", func(t *testing.T) {
		big := make([]byte, selfStalingMaxFileSize+1)
		for i := range big {
			big[i] = 'a'
		}
		copy(big, []byte(testFullHash))
		p := write("huge.js", big)
		assert.False(t, fileRecordsCommit(p, testMeta()), "past the size cap it is a bundle, not a page")
	})

	t.Run("a missing file is not a finding", func(t *testing.T) {
		assert.False(t, fileRecordsCommit(filepath.Join(dir, "absent.html"), testMeta()))
	})

	t.Run("no commit yet means nothing matches", func(t *testing.T) {
		p := write("empty-meta.html", []byte("Last updated ("+testFullHash+")"))
		assert.False(t, fileRecordsCommit(p, types.VCSMeta{}))
	})
}

// TestDeclaredOutputFiles proves the expansion reads AllOutputs (project-wide PLUS
// per-target), since this workspace declares almost everything per-target with
// ctx.writesFiles - reading only p.Outputs would scan nothing here and report clean.
func TestDeclaredOutputFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen"), 0o755))
	for _, name := range []string{"gen/a.html", "gen/b.html", "untouched.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("x"), 0o644))
	}

	p := &types.Project{
		Path: ".", Dir: dir,
		TargetOutputs: map[string][]types.OutputRef{
			"site-generate": {{Glob: "gen/**"}},
		},
	}
	got := declaredOutputFiles(p)
	assert.Equal(t, []string{"gen/a.html", "gen/b.html"}, got,
		"per-target outputs are expanded; undeclared files are not")

	t.Run("a directory is not a file to scan", func(t *testing.T) {
		p := &types.Project{Path: ".", Dir: dir, Outputs: []string{"gen"}}
		assert.Empty(t, declaredOutputFiles(p))
	})
}

// TestSelfStalingSkipsWithoutTrackedReporter is the guard that keeps this check honest on a
// backend that cannot answer "is this tracked?". Reporting on those would flag the FIXED
// state - a generator still writes the same hash into the same files once they are
// untracked - so the check has to skip instead of guess.
func TestSelfStalingSkipsWithoutTrackedReporter(t *testing.T) {
	// A non-git tree resolves no VCS, which is the same degrade path.
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	got := r.checkSelfStalingOutputs(nil)
	assert.Equal(t, StatusSkip, got.Status)
	assert.True(t,
		strings.Contains(got.Message, "skipped") ||
			strings.Contains(got.Message, "no VCS") ||
			strings.Contains(got.Message, "no commit"),
		"expected a skip, got %q", got.Message)
}

// stubWorkspace is the minimum WorkspaceReader the checks need: a root, a project
// list, and the dependency graph built from it. The embedded interface panics on
// anything else, so a check that grows a new dependency fails loudly here.
type stubWorkspace struct {
	types.WorkspaceReader
	root     string
	projects []*types.Project
}

func (s stubWorkspace) Root() string               { return s.root }
func (s stubWorkspace) All() []*types.Project      { return s.projects }
func (stubWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }
func (stubWorkspace) Where(string) (*types.Project, bool) {
	return nil, false
}

func (s stubWorkspace) Get(path string) *types.Project {
	for _, p := range s.projects {
		if p.Path == path {
			return p
		}
	}
	return nil
}

func (s stubWorkspace) Graph() (*types.Graph, error) {
	ws := &types.Workspace{Projects: map[string]*types.Project{}}
	for _, p := range s.projects {
		ws.Projects[p.Path] = p
	}
	return dependency.Build(ws)
}
