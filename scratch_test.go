package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const scratchFile = "calc/calc.toy"

func toySource() coverageSource {
	return coverageSource{project: ".", spell: "toy", target: "test",
		coverage: spells.Coverage{Format: spells.CoverageFormatLCOV, Profile: "out/lcov.info"}}
}

type ScratchLinksSuite struct{ suite.Suite }

func TestScratchLinksSuite(t *testing.T) { suite.Run(t, new(ScratchLinksSuite)) }

func (s *ScratchLinksSuite) TestLinksAreEveryProjectsIgnoreDirs() {
	got := ignoreLinks(map[string][]string{"web": {"node_modules", "node_modules"}, ".": {"node_modules", "vendor"}})
	s.Equal([]string{"node_modules", "vendor", "web/node_modules"}, got)
}

func (s *ScratchLinksSuite) TestTheCopysCacheDirIsPinnedInItsEnv() {
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
	return magusRunner{bin: "/bin/sh"}.run(context.Background(), s.dir, toySource())
}

func resultRecord(project, target, status string, hit bool) string {
	return fmt.Sprintf(`echo '{"schema":5,"type":"run.target.result","project":%q,"target":%q,"status":%q,"cache_hit":%t}'`,
		project, target, status, hit)
}

func (s *ScratchRunnerSuite) TestARunIsCachedInTheCopysOwnCache() {
	got, err := s.runWith(
		`[ "$*" = "test --no-volatility-retry -o jsonl ." ] || exit 2`,
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
	s.ErrorContains(err, "exited 1 with no result for test (workspace did not load): so no test ran")
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

func (s *ScratchRunnerSuite) TestASpellQualifiedTargetMatchesItsOp() {
	r, ok := recordedResult(`{"schema":5,"type":"run.target.result","project":".","target":"go-test","status":"ok"}`, ".", "go::go-test")
	s.True(ok)
	s.Equal("go-test", r.Target)
}

func (s *ScratchRunnerSuite) TestAPlainTargetMatchesItself() {
	r, ok := recordedResult(`{"schema":5,"type":"run.target.result","project":".","target":"test","status":"ok"}`, ".", "test")
	s.True(ok)
	s.Equal("test", r.Target)
}

type TreeCopySuite struct{ suite.Suite }

func TestTreeCopySuite(t *testing.T) { suite.Run(t, new(TreeCopySuite)) }

// copyVCS answers the export treeCopier makes; any other call panics.
type copyVCS struct {
	types.VCSDriver
	head map[string]string
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
	dir, err := sc.copy(context.Background(), "HEAD", nil)
	if dir != "" {
		s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	s.Require().NoError(err)

	store, err := config.StoreDir(repo.Root(), cfg)
	s.Require().NoError(err)
	s.Equal(filepath.Join(store, scratchTreesDir), filepath.Dir(dir))
	b, err := os.ReadFile(filepath.Join(dir, "calc.toy"))
	s.Require().NoError(err)
	s.Equal("head", string(b), "a revision is exported without the working tree's edits")
	s.NoDirExists(filepath.Join(denied, "magus"))
}

func (s *TreeCopySuite) TestACopyOfARevisionLinksTheIgnoreDirs() {
	root := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "node_modules"), 0o755))
	c := treeCopier{root: root, trees: s.T().TempDir(), rev: "c0ffee", links: []string{"node_modules"},
		vcs: copyVCS{head: map[string]string{"kept.toy": "base"}}}
	dir, err := c.copy(context.Background())
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	s.Equal([]string{"kept.toy", "node_modules"}, names)
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

// BaseProfilesSuite reads base profiles with a stub copy and run.
type BaseProfilesSuite struct {
	suite.Suite
	profiles *baseProfiles
	copies   int
	runs     int
	passed   bool
	runErr   error
	// wrote is what the stub run leaves at the profile, "" for nothing.
	wrote string
}

func TestBaseProfilesSuite(t *testing.T) { suite.Run(t, new(BaseProfilesSuite)) }

const toyLCOV = "SF:" + scratchFile + "\nDA:4,2\nDA:9,0\nend_of_record\n"

func (s *BaseProfilesSuite) SetupTest() {
	s.copies, s.runs, s.passed, s.runErr, s.wrote = 0, 0, true, nil, toyLCOV
	s.profiles = &baseProfiles{
		commit: "c0ffee", kept: filepath.Join(s.T().TempDir(), keptProfilesDir), start: time.Now(),
		parse: func(c coverageSource, data []byte) ([]coverage.File, error) {
			return coverage.Parse(c.coverage.Format, data, "")
		},
		copy: func(context.Context) (string, error) {
			s.copies++
			return s.T().TempDir(), nil
		},
		run: func(_ context.Context, dir string, c coverageSource) (testRun, error) {
			s.runs++
			if s.wrote != "" {
				file := filepath.Join(dir, filepath.FromSlash(c.coverage.Profile))
				s.Require().NoError(os.MkdirAll(filepath.Dir(file), 0o755))
				s.Require().NoError(os.WriteFile(file, []byte(s.wrote), 0o644))
			}
			return testRun{passed: s.passed}, s.runErr
		},
	}
}

func (s *BaseProfilesSuite) read() profileRead {
	got, err := s.profiles.read(context.Background(), toySource())
	s.Require().NoError(err)
	return got
}

func (s *BaseProfilesSuite) TestAMissRunsTheTargetOnceAndKeepsItsProfile() {
	first := s.read()
	second := s.read()

	s.Equal([2]int{1, 1}, [2]int{s.copies, s.runs}, "the second read is the kept profile")
	s.Empty(first.reason)
	s.Equal(first, second)
	s.Equal(map[int]bool{4: true, 9: false}, fileNamed(first.files, scratchFile).Lines())
}

func (s *BaseProfilesSuite) TestEachBaseCommitKeepsItsOwnProfile() {
	s.read()
	s.profiles.commit = "decaf"
	s.read()

	s.Equal(2, s.runs)
}

func (s *BaseProfilesSuite) TestARedRunIsAReasonAndKeepsNothing() {
	s.passed = false
	got := s.read()
	s.read()

	s.Equal("a test fails at base c0ffee: `magus run test .`", got.reason)
	s.Equal(2, s.runs, "a red run is never kept")
}

func (s *BaseProfilesSuite) TestARunThatLeftNoProfileIsAReason() {
	s.wrote = ""
	s.Equal("`magus run test .` at base c0ffee left no out/lcov.info", s.read().reason)
}

func (s *BaseProfilesSuite) TestARunWithNoSeatIsAnError() {
	s.runErr = fmt.Errorf("exited 75: %w", errNoSeat)
	_, err := s.profiles.read(context.Background(), toySource())
	s.ErrorIs(err, errNoSeat)
}

func (s *BaseProfilesSuite) TestARunThatRecordedNoResultIsAReason() {
	s.runErr = fmt.Errorf("exited 1: %w", errNoResult)
	s.Contains(s.read().reason, "so no test ran")
}

func (s *BaseProfilesSuite) TestAStoppedRunNamesItsCommandAndBase() {
	ctx, cancel := context.WithCancelCause(context.Background())
	s.profiles.run = func(context.Context, string, coverageSource) (testRun, error) {
		cancel(errors.New("the gate ended"))
		return testRun{}, context.Canceled
	}
	_, err := s.profiles.read(ctx, toySource())
	s.ErrorContains(err, "change risk: stopped running `magus run test .` at base c0ffee after ")
	s.ErrorContains(err, ": the gate ended")
}

func (s *BaseProfilesSuite) TestRemoveDeletesTheCopy() {
	s.read()
	dir := s.profiles.dir
	s.profiles.remove()
	s.NoDirExists(dir)
}
