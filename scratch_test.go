package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const scratchFile = "calc/calc.toy"

func toyScope() coverageScope {
	return coverageScope{project: ".", spell: "toy", coverage: spells.Coverage{Test: "test"}, scope: []string{"./calc"}}
}

type SubjectScopeSuite struct {
	suite.Suite
	root string
}

func TestSubjectScopeSuite(t *testing.T) { suite.Run(t, new(SubjectScopeSuite)) }

func (s *SubjectScopeSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "web", "calc"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "web", "calc", "calc.toy"), nil, 0o644))
}

func (s *SubjectScopeSuite) TestAnEntryNamingNoFileIsDropped() {
	test := spells.Command{TestScope: []string{"./{dir}", "{dir}/{stem}_test.toy", "{file}"}}
	scope, reason := subjectScope(s.root, "web", "web/calc/calc.toy", test)
	s.Empty(reason)
	s.Equal([]string{"./calc", "calc/calc.toy"}, scope)
}

func (s *SubjectScopeSuite) TestADeclaredScopeNamingNothingIsAReason() {
	test := spells.Command{TestScope: []string{"{dir}/{stem}_test.toy"}}
	scope, reason := subjectScope(s.root, "web", "web/calc/calc.toy", test)
	s.Nil(scope)
	s.Equal("no test_scope entry names a file for web/calc/calc.toy", reason)
}

func (s *SubjectScopeSuite) TestNoDeclaredScopeRunsTheProject() {
	scope, reason := subjectScope(s.root, "web", "web/calc/calc.toy", spells.Command{})
	s.Empty(reason)
	s.Empty(scope)
}

func (s *SubjectScopeSuite) TestTheRootProjectScopesByWorkspacePath() {
	test := spells.Command{TestScope: []string{"./{dir}"}}
	scope, reason := subjectScope(s.root, ".", "web/calc/calc.toy", test)
	s.Empty(reason)
	s.Equal([]string{"./web/calc"}, scope)
}

func (s *SubjectScopeSuite) TestLinksAreEveryProjectsIgnoreDirs() {
	got := ignoreLinks(map[string][]string{"web": {"node_modules", "node_modules"}, ".": {"node_modules", "vendor"}})
	s.Equal([]string{"node_modules", "vendor", "web/node_modules"}, got)
}

// ScratchReportSuite reads the report a coverage run left in a scratch copy.
type ScratchReportSuite struct{ suite.Suite }

func TestScratchReportSuite(t *testing.T) { suite.Run(t, new(ScratchReportSuite)) }

func (s *ScratchReportSuite) TestTheRunsHitLinesAreCovered() {
	dir := s.T().TempDir()
	reports := coverage.Dir(copyCacheDir(dir), ".")
	s.Require().NoError(os.MkdirAll(reports, 0o755))
	lcov := "SF:" + scratchFile + "\nDA:4,2\nDA:9,0\nend_of_record\n"
	s.Require().NoError(os.WriteFile(filepath.Join(reports, "lcov.info"), []byte(lcov), 0o644))
	c := toyScope()
	c.coverage = spells.Coverage{Format: spells.CoverageFormatLCOV, File: "lcov.info"}
	files, err := scratchReport(dir, c)
	s.Require().NoError(err)
	s.Equal(map[int]bool{4: true}, hitLines(files, scratchFile))
}

func (s *ScratchReportSuite) TestNoReportIsAnError() {
	c := toyScope()
	c.coverage = spells.Coverage{Format: spells.CoverageFormatLCOV, File: "lcov.info"}
	_, err := scratchReport(s.T().TempDir(), c)
	s.ErrorContains(err, "its coverage run left no report")
}

func (s *ScratchReportSuite) TestTheCopysCacheDirIsPinnedInItsEnv() {
	s.Equal("MAGUS_CACHE_DIR="+filepath.Join("/copy", ".magus"), copyCacheEnv("/copy"))
}

// ScratchRunnerSuite runs /bin/sh as the magus, so run names a script in dir.
type ScratchRunnerSuite struct {
	suite.Suite
	dir string
}

func TestScratchRunnerSuite(t *testing.T) { suite.Run(t, new(ScratchRunnerSuite)) }

func (s *ScratchRunnerSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *ScratchRunnerSuite) runWith(lines ...string) (testRun, error) {
	script := []byte(strings.Join(lines, "\n"))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, "run"), script, 0o644))
	return magusRunner{bin: "/bin/sh"}.run(context.Background(), s.dir, "toy::test", toyScope())
}

func resultRecord(project, target, status string, hit bool) string {
	return fmt.Sprintf(`echo '{"schema":5,"type":"run.target.result","project":%q,"target":%q,"status":%q,"cache_hit":%t}'`,
		project, target, status, hit)
}

func (s *ScratchRunnerSuite) TestARunBypassesTheCacheAndNamesTheSpell() {
	got, err := s.runWith(
		`[ "$*" = "toy::test --no-cache --no-volatility-retry -o jsonl . -- ./calc" ] || exit 2`,
		`case "$MAGUS_CACHE_DIR" in */.magus) ;; *) exit 2 ;; esac`,
		resultRecord(".", "test", "ok", false))
	s.Require().NoError(err)
	s.Equal(testRun{passed: true}, got)
}

func (s *ScratchRunnerSuite) TestAFailedResultFails() {
	got, err := s.runWith(resultRecord(".", "test", "failed", false), "exit 1")
	s.Require().NoError(err)
	s.Equal(testRun{}, got)
}

func (s *ScratchRunnerSuite) TestACachedResultSaysSo() {
	got, err := s.runWith(resultRecord(".", "test", "ok", true))
	s.Require().NoError(err)
	s.Equal(testRun{passed: true, cached: true}, got)
}

func (s *ScratchRunnerSuite) TestAFailedRunThatReportsNoResultJudgesNothing() {
	_, err := s.runWith("echo 'workspace did not load' >&2", "exit 1")
	s.ErrorContains(err, "exited 1 with no result for toy::test (workspace did not load): so no test ran")
	s.ErrorIs(err, errNoResult)
}

func (s *ScratchRunnerSuite) TestAnotherTargetsResultIsNotThisOnes() {
	_, err := s.runWith(resultRecord(".", "go-generate", "failed", false), resultRecord("web", "test", "failed", false), "exit 1")
	s.ErrorContains(err, "so no test ran")
}

func (s *ScratchRunnerSuite) TestARunMagusCouldNotSeatJudgesNothing() {
	_, err := s.runWith("exit 75")
	s.ErrorContains(err, "exited 75")
	s.ErrorIs(err, errNoSeat)
}

func (s *ScratchRunnerSuite) TestARunASignalEndedIsNotARunThatDidNotLoad() {
	_, err := s.runWith("kill -9 $$")
	s.ErrorContains(err, "was signaled")
	s.NotErrorIs(err, errNoResult)
}

type TreeCopySuite struct{ suite.Suite }

func TestTreeCopySuite(t *testing.T) { suite.Run(t, new(TreeCopySuite)) }

// copyVCS answers the four calls treeCopier makes; any other call panics.
type copyVCS struct {
	types.VCSDriver
	repo  string
	head  map[string]string
	dirty []string
}

func (v copyVCS) FindCommit(context.Context, string, string) (types.Commit, error) {
	return types.Commit{ID: "c0ffee"}, nil
}

func (v copyVCS) ExportRevision(_ context.Context, _, rev, dst string) error {
	if rev != "c0ffee" {
		return errors.New("exported " + rev)
	}
	for rel, text := range v.head {
		if err := os.WriteFile(filepath.Join(dst, rel), []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (v copyVCS) Root(context.Context, string) (string, error) { return v.repo, nil }

func (v copyVCS) DirtyFiles(context.Context, string, []string) ([]string, error) {
	return v.dirty, nil
}

func (s *TreeCopySuite) TestACopyLandsInTheStoreWhenTheUserCacheIsDenied() {
	denied := filepath.Join(s.T().TempDir(), "denied")
	s.Require().NoError(os.Mkdir(denied, 0o500))
	s.T().Setenv("XDG_CACHE_HOME", denied)
	s.T().Setenv("HOME", denied)
	cfg := config.Defaults()
	cfg.Cache.Dir = filepath.Join(s.T().TempDir(), "granted")
	repo := testkit.NewRepository(s.T(), map[string]string{"calc.toy": "head"})
	s.Require().NoError(os.WriteFile(filepath.Join(repo.Root(), "calc.toy"), []byte("edit"), 0o644))
	m := &Magus{ws: &types.Workspace{Root: repo.Root()}, cfg: cfg}

	sc, err := m.newScratch(context.Background())
	s.Require().NoError(err)
	dir, err := sc.copy(context.Background(), "", nil)
	if dir != "" {
		s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	s.Require().NoError(err)

	store, err := config.StoreDir(repo.Root(), cfg)
	s.Require().NoError(err)
	s.Equal(filepath.Join(store, scratchTreesDir), filepath.Dir(dir))
	b, err := os.ReadFile(filepath.Join(dir, "calc.toy"))
	s.Require().NoError(err)
	s.Equal("edit", string(b))
	s.NoDirExists(filepath.Join(denied, "magus"))
}

func (s *TreeCopySuite) TestACopyIsHeadWithTheWorkingTreesEdits() {
	repo := s.T().TempDir()
	root := filepath.Join(repo, "ws")
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "node_modules"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "edited.toy"), []byte("edit"), 0o644))
	c := treeCopier{root: root, trees: s.T().TempDir(), links: []string{"node_modules"}, vcs: copyVCS{
		repo: repo, dirty: []string{"ws/edited.toy", "ws/deleted.toy"},
		head: map[string]string{"edited.toy": "head", "deleted.toy": "head", "kept.toy": "head"},
	}}
	dir, err := c.copy(context.Background())
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	s.Equal([]string{"edited.toy", "kept.toy", "node_modules"}, names)
	b, err := os.ReadFile(filepath.Join(dir, "edited.toy"))
	s.Require().NoError(err)
	s.Equal("edit", string(b))
}

func (s *TreeCopySuite) TestACopyOfARevisionTakesNoEdits() {
	repo := s.T().TempDir()
	root := filepath.Join(repo, "ws")
	s.Require().NoError(os.MkdirAll(root, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "edited.toy"), []byte("edit"), 0o644))
	c := treeCopier{root: root, trees: s.T().TempDir(), rev: "c0ffee", vcs: copyVCS{
		repo: repo, dirty: []string{"ws/edited.toy"}, head: map[string]string{"edited.toy": "base"},
	}}
	dir, err := c.copy(context.Background())
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	b, err := os.ReadFile(filepath.Join(dir, "edited.toy"))
	s.Require().NoError(err)
	s.Equal("base", string(b))
}

func (s *TreeCopySuite) TestAnEditedFileIsCopiedAndADeletedOneRemoved() {
	root, dir := s.T().TempDir(), s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(root, "edited.toy"), []byte("new"), 0o640))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "gone.toy"), []byte("old"), 0o644))
	s.Require().NoError(overlayFile(root, dir, filepath.Join(root, "edited.toy")))
	s.Require().NoError(overlayFile(root, dir, filepath.Join(root, "gone.toy")))
	b, err := os.ReadFile(filepath.Join(dir, "edited.toy"))
	s.Require().NoError(err)
	s.Equal("new", string(b))
	_, err = os.Stat(filepath.Join(dir, "gone.toy"))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *TreeCopySuite) TestAPathOutsideTheWorkspaceIsSkipped() {
	parent := s.T().TempDir()
	root, dir := filepath.Join(parent, "ws"), s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(parent, "outside.toy"), []byte("x"), 0o644))
	s.Require().NoError(overlayFile(root, dir, filepath.Join(parent, "outside.toy")))
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Empty(entries)
}

func (s *TreeCopySuite) TestALinkedDirPointsAtTheWorkspace() {
	root, dir := s.T().TempDir(), s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(root, "node_modules"), 0o755))
	s.Require().NoError(linkDirs(root, dir, []string{"node_modules", "vendor"}))
	target, err := os.Readlink(filepath.Join(dir, "node_modules"))
	s.Require().NoError(err)
	s.Equal(filepath.Join(root, "node_modules"), target)
	_, err = os.Lstat(filepath.Join(dir, "vendor"))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *TreeCopySuite) TestATrackedDirIsKeptNotLinked() {
	root, dir := s.T().TempDir(), s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(root, "vendor"), 0o755))
	s.Require().NoError(os.Mkdir(filepath.Join(dir, "vendor"), 0o755))
	s.Require().NoError(linkDirs(root, dir, []string{"vendor"}))
	info, err := os.Lstat(filepath.Join(dir, "vendor"))
	s.Require().NoError(err)
	s.True(info.IsDir())
}
