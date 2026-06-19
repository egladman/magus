package interactive

import (
	"encoding/json"
	"os"
	"path/filepath"
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
// fresh temp dir pointed at by the env var before the State helpers run.
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

func (s *StateSuite) TestSaveAndLoadState() {
	want := State{
		LastTarget:     map[string]string{"/path/to/proj": "build"},
	}
	require.NoError(s.T(), SaveState(want))

	got, err := LoadState()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), want, got)
}

func (s *StateSuite) TestLoadStateMissingFile() {
	// No file written — a missing file is documented as not an error.
	_, err := LoadState()
	assert.NoError(s.T(), err)
}

func (s *StateSuite) TestSaveStateIsAtomic() {
	// Confirm no .tmp file is left behind after a successful save.
	require.NoError(s.T(), SaveState(State{}))
	path, err := StatePath()
	require.NoError(s.T(), err)
	tmp := path + ".tmp"
	_, err = os.Stat(tmp)
	assert.Error(s.T(), err, "temp file %s still exists after SaveState", tmp)
}

func (s *StateSuite) TestSaveStateValidJSON() {
	require.NoError(s.T(), SaveState(State{LastTarget: map[string]string{"proj": "test"}}))
	path, err := StatePath()
	require.NoError(s.T(), err)
	b, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	var check State
	assert.NoError(s.T(), json.Unmarshal(b, &check), "saved file is not valid JSON")
}

func (s *StateSuite) TestStatePathUsesXDGStateHome() {
	p, err := StatePath()
	require.NoError(s.T(), err)
	assert.True(s.T(), filepath.IsAbs(p), "StatePath returned relative path %q", p)
	// Must be under our custom dir.
	rel, err := filepath.Rel(s.dir, p)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), rel, "path %q is not under XDG_STATE_HOME %q", p, s.dir)
}
