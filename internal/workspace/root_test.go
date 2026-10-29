package workspace

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// RootSuite pins the one-workspace rule: a magusfile marks a PROJECT, only magus.yaml
// declares where a workspace begins, and no language's manifest roots one.
type RootSuite struct {
	suite.Suite
	w *testkit.Workspace
}

func TestRootSuite(t *testing.T) { suite.Run(t, new(RootSuite)) }

func (s *RootSuite) SetupTest() { s.w = testkit.NewWorkspace(s.T()) }

// mkTree creates dirs and marker files. A trailing "/" makes a directory, otherwise
// an empty marker file.
func (s *RootSuite) mkTree(paths ...string) {
	for _, p := range paths {
		if p[len(p)-1] == '/' {
			s.Require().NoError(os.MkdirAll(s.w.Path(p), 0o755))
			continue
		}
		s.w.Write(p, "")
	}
}

func (s *RootSuite) findRoot(rel ...string) string {
	got, err := FindRoot(s.w.Path(rel...))
	s.Require().NoError(err)
	return got
}

func (s *RootSuite) TestANestedProjectDoesNotBecomeItsOwnWorkspace() {
	s.mkTree("magus.yaml", "magusfile.buzz", "console/magusfile.buzz")
	s.Equal(s.w.Root(), s.findRoot("console"), "halting at console's magusfile is the original bug")
}

// Worktrees nest INSIDE their parent repo, so an outermost-wins rule swallows them.
// The nearest declaration governs, the same way .git behaves.
func (s *RootSuite) TestTheNearestMagusYAMLWinsSoANestedWorktreeKeepsItsOwn() {
	s.mkTree("magus.yaml", "magusfile.buzz",
		".claude/worktrees/feature/magus.yaml",
		".claude/worktrees/feature/magusfile.buzz",
		".claude/worktrees/feature/console/magusfile.buzz")
	wt := s.w.Path(".claude", "worktrees", "feature")

	s.Equal(wt, s.findRoot(".claude", "worktrees", "feature"), "not its parent repo")
	s.Equal(wt, s.findRoot(".claude", "worktrees", "feature", "console"))
	s.Equal(s.w.Root(), s.findRoot())
}

func (s *RootSuite) TestAStandaloneProjectResolvesToItsOutermostMagusfile() {
	s.mkTree("solo/magusfile.buzz", "solo/nested/magusfile.buzz")
	s.Equal(s.w.Path("solo"), s.findRoot("solo", "nested"))
}

// Contiguity bounds the fallback: without it, one stray magusfile in a home directory
// would adopt every project beneath it.
func (s *RootSuite) TestAGapStopsTheMagusfileRunFromReachingAStrayAncestor() {
	s.mkTree("magusfile.buzz", "gap/", "gap/proj/magusfile.buzz")
	s.Equal(s.w.Path("gap", "proj"), s.findRoot("gap", "proj"), "the marker-less gap must stop the walk")
}

func (s *RootSuite) TestAMagusfileWinsOverTheCheckoutAboveIt() {
	s.mkTree(".git/", "app/magusfile.buzz", "app/web/")
	s.Equal(s.w.Path("app"), s.findRoot("app", "web"))
}

// go.mod once rooted a workspace from shipped Go, which put one language's knowledge
// in core and split a multi-module repo at each submodule.
func (s *RootSuite) TestAGoModRootsNothingSoTheCheckoutDoes() {
	s.mkTree(".git/", "go.mod", "libs/diagnostics/go.mod")
	s.Equal(s.w.Root(), s.findRoot("libs", "diagnostics"))
}

// A linked git worktree carries .git as a file, and the nearest checkout governs.
func (s *RootSuite) TestTheNearestCheckoutRootsTheWorkspace() {
	s.mkTree(".hg/", "worktree/.git", "worktree/src/")
	s.Equal(s.w.Path("worktree"), s.findRoot("worktree", "src"))
}

func (s *RootSuite) TestRootingAtTheCheckoutWarnsAndNamesInit() {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	s.T().Cleanup(func() { slog.SetDefault(prev) })

	s.mkTree(".jj/", "src/")
	s.findRoot("src")
	s.findRoot()

	s.Contains(logged.String(), "the workspace root is the VCS checkout")
	s.Contains(logged.String(), "init")
	s.Equal(1, bytes.Count(logged.Bytes(), []byte("level=WARN")), "one warning per root per process")
}

func (s *RootSuite) TestNoMagusFileAndNoCheckoutIsNoWorkspaceRoot() {
	above := filepath.Dir(s.w.Root())
	if d, ok := project.CheckoutRoot(above); ok {
		s.T().Skipf("%s, above the temp dir, roots a checkout, so the walk cannot run out", d)
	}
	for d := above; d != filepath.Dir(d); d = filepath.Dir(d) {
		if dirHasMarker(d, append([]string{workspaceMarker}, projectMarkers...)) {
			s.T().Skipf("%s, above the temp dir, carries a marker, so the walk cannot run out", d)
		}
	}
	s.mkTree("go.mod", "pkg/")
	_, err := FindRoot(s.w.Path("pkg"))
	s.ErrorIs(err, types.NoWorkspaceRoot)
}
