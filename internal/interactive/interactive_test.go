package interactive

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func makeProjects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p}
	}
	return out
}

func TestScoreProjectsNoFilter(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users", "api/orders", "web/app")
	got := ScoreProjects(all, nil)
	assert.Len(t, got, 3)
}

func TestScoreProjectsFilterMatchesSubset(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users", "api/orders", "web/app")
	got := ScoreProjects(all, []string{"api"})
	require.Len(t, got, 2)
	for _, sp := range got {
		assert.NotEqual(t, "web/app", sp.P.Path, "web/app should not match filter 'api'")
	}
}

func TestScoreProjectsMultipleFilters(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users", "api/orders", "web/app", "api/users/v2")
	got := ScoreProjects(all, []string{"api", "users"})
	for _, sp := range got {
		assert.NotContains(t, []string{"api/orders", "web/app"}, sp.P.Path, "unexpected project matched all filters")
	}
}

func TestScoreProjectsEmptyFilterTokensIgnored(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users")
	got := ScoreProjects(all, []string{"", "   "})
	assert.Len(t, got, 1, "blank filters should not filter anything")
}

func TestScoreProjectsLeafRanking(t *testing.T) {
	t.Parallel()
	// "users" appears in two paths; the one where it's the leaf component
	// should rank higher.
	all := makeProjects("api/users", "services/users-svc")
	got := ScoreProjects(all, []string{"users"})
	require.GreaterOrEqual(t, len(got), 2, "expected both projects to match")
	assert.Equal(t, "api/users", got[0].P.Path, "expected api/users ranked first")
}

func TestScoreProjectsCaseInsensitive(t *testing.T) {
	t.Parallel()
	all := makeProjects("API/Users")
	got := ScoreProjects(all, []string{"api"})
	assert.Len(t, got, 1, "filter should be case-insensitive")
}

// StateSuite groups tests that share the XDG_STATE_HOME setup: each needs a
// fresh temp dir pointed at by the env var before the state helpers run.
type StateSuite struct {
	suite.Suite
	dir string
}

func (s *StateSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.T().Setenv("XDG_STATE_HOME", s.dir)
}

func TestStateSuite(t *testing.T) {
	suite.Run(t, new(StateSuite))
}

func (s *StateSuite) TestSaveAndLoadLastTarget() {
	require.NoError(s.T(), SaveLastTarget("/path/to/proj", "build"))
	assert.Equal(s.T(), "build", LastTarget("/path/to/proj"))
}

func (s *StateSuite) TestLastTargetIsEmptyForAnUnseenProject() {
	assert.Empty(s.T(), LastTarget("/never/picked"))
}

func (s *StateSuite) TestSaveLastTargetReplacesTheEarlierPick() {
	require.NoError(s.T(), SaveLastTarget("/path/to/proj", "build"))
	require.NoError(s.T(), SaveLastTarget("/path/to/proj", "test"))
	assert.Equal(s.T(), "test", LastTarget("/path/to/proj"))
}

// One file per project is what makes a project's entry independent. Two projects
// sharing one document is the shape this replaced.
func (s *StateSuite) TestProjectsGetSeparateFiles() {
	require.NoError(s.T(), SaveLastTarget("/proj/a", "build"))
	require.NoError(s.T(), SaveLastTarget("/proj/b", "lint"))

	assert.Equal(s.T(), "build", LastTarget("/proj/a"))
	assert.Equal(s.T(), "lint", LastTarget("/proj/b"))

	entries, err := os.ReadDir(filepath.Join(s.dir, "magus", "x"))
	require.NoError(s.T(), err)
	assert.Len(s.T(), entries, 2, "each project owns one file")
}

// The filename is a digest, so a listing of the state dir cannot reproduce which
// directories on this machine the user works in.
func (s *StateSuite) TestFilenameDoesNotEmbedTheProjectPath() {
	require.NoError(s.T(), SaveLastTarget("/home/someone/secret-client/api", "build"))

	entries, err := os.ReadDir(filepath.Join(s.dir, "magus", "x"))
	require.NoError(s.T(), err)
	require.Len(s.T(), entries, 1)
	assert.Regexp(s.T(), `^[0-9a-f]{16}$`, entries[0].Name())
}

// Saves in DIFFERENT projects touch different files, so this cannot corrupt anything
// by construction. It is kept as the regression pin on that shape: the moment these
// share one document again, one of these reads comes back wrong.
func (s *StateSuite) TestConcurrentSavesInDifferentProjectsAllSurvive() {
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(s.T(), SaveLastTarget(fmt.Sprintf("/proj/%d", i), fmt.Sprintf("target%d", i)))
		}()
	}
	wg.Wait()

	for i := range 16 {
		assert.Equal(s.T(), fmt.Sprintf("target%d", i), LastTarget(fmt.Sprintf("/proj/%d", i)))
	}
}

// The race that survives the reshape: two pickers finishing in the SAME project. One
// of the two values must win whole. A plain write truncates before it fills, so a
// reader landing in that window would see "" or a prefix of a target name.
func (s *StateSuite) TestConcurrentSavesInOneProjectLeaveOneValueIntact() {
	const dir = "/proj/contended"
	// Names of very different lengths, so a torn write reads as neither rather than
	// as one name overwritten by an equally long one.
	names := []string{"ci", "coverage-badge-and-then-some-much-longer-target-name"}

	for range 50 {
		var wg sync.WaitGroup
		for _, n := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.NoError(s.T(), SaveLastTarget(dir, n))
			}()
		}
		wg.Wait()

		assert.Contains(s.T(), names, LastTarget(dir),
			"the file holds a fragment rather than one saver's whole value")
	}

	entries, err := os.ReadDir(filepath.Join(s.dir, "magus", "x"))
	require.NoError(s.T(), err)
	assert.Len(s.T(), entries, 1, "concurrent saves leaked temp files")
}

func (s *StateSuite) TestStateDirIsUnderXDGStateHome() {
	p, err := targetPath("/proj/a")
	require.NoError(s.T(), err)
	assert.True(s.T(), filepath.IsAbs(p), "targetPath returned relative path %q", p)
	rel, err := filepath.Rel(s.dir, p)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), rel, "path %q is not under XDG_STATE_HOME %q", p, s.dir)
}
