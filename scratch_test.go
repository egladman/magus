package magus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	dir, err := sc.copy(context.Background(), "HEAD")
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

func (s *TreeCopySuite) TestACopyHoldsTheRevision() {
	c := treeCopier{root: s.T().TempDir(), trees: s.T().TempDir(), rev: "c0ffee",
		vcs: copyVCS{head: map[string]string{"kept.toy": "base"}}}
	dir, err := c.copy(context.Background())
	s.Require().NoError(err)
	b, err := os.ReadFile(filepath.Join(dir, "kept.toy"))
	s.Require().NoError(err)
	s.Equal("base", string(b))
}

// BaseProfilesSuite reads base profiles with a stub export and replay.
type BaseProfilesSuite struct {
	suite.Suite
	profiles *baseProfiles
	copies   int
	replays  int
	// restored is what a replay leaves at the profile; "" is a miss.
	restored  string
	replayErr error
}

func TestBaseProfilesSuite(t *testing.T) { suite.Run(t, new(BaseProfilesSuite)) }

const toyLCOV = "SF:" + scratchFile + "\nDA:4,2\nDA:9,0\nend_of_record\n"

func (s *BaseProfilesSuite) SetupTest() {
	s.copies, s.replays, s.restored, s.replayErr = 0, 0, toyLCOV, nil
	s.profiles = &baseProfiles{
		commit: "c0ffee", kept: filepath.Join(s.T().TempDir(), keptProfilesDir),
		parse: func(c coverageSource, data []byte) ([]coverage.File, error) {
			return coverage.Parse(c.coverage.Format, data, "")
		},
		copy: func(context.Context) (string, error) {
			s.copies++
			return s.T().TempDir(), nil
		},
		replay: func(_ context.Context, dir string, c coverageSource) error {
			s.replays++
			if s.replayErr != nil {
				return s.replayErr
			}
			if s.restored == "" {
				return errBaseNotCached
			}
			file := filepath.Join(dir, filepath.FromSlash(c.coverage.Profile))
			s.Require().NoError(os.MkdirAll(filepath.Dir(file), 0o755))
			return os.WriteFile(file, []byte(s.restored), 0o644)
		},
	}
}

func (s *BaseProfilesSuite) TestACachedProfileIsReplayedOnceAndKept() {
	first, err := s.profiles.read(context.Background(), toySource())
	s.Require().NoError(err)
	second, err := s.profiles.read(context.Background(), toySource())
	s.Require().NoError(err)

	s.Equal([2]int{1, 1}, [2]int{s.copies, s.replays}, "the second read is the kept profile")
	s.Equal(first, second)
	s.Equal(map[int]bool{4: true, 9: false}, fileNamed(first.files, scratchFile).Lines())
}

func (s *BaseProfilesSuite) TestEachBaseCommitKeepsItsOwnProfile() {
	_, err := s.profiles.read(context.Background(), toySource())
	s.Require().NoError(err)
	s.profiles.commit = "decaf"
	_, err = s.profiles.read(context.Background(), toySource())
	s.Require().NoError(err)

	s.Equal(2, s.replays)
}

func (s *BaseProfilesSuite) TestNoCacheEntryIsAnErrorNamingTheBaseAndTheCommand() {
	s.restored = ""
	_, err := s.profiles.read(context.Background(), toySource())

	s.ErrorIs(err, errBaseNotCached)
	s.ErrorContains(err, "no cache holds the coverage profile of base c0ffee, which main's CI writes with `magus run test .` at that commit")
}

func (s *BaseProfilesSuite) TestAMissIsNeverKept() {
	s.restored = ""
	_, _ = s.profiles.read(context.Background(), toySource())
	_, _ = s.profiles.read(context.Background(), toySource())

	s.Equal(2, s.replays, "each read asks the cache again")
}

func (s *BaseProfilesSuite) TestABaseThatDoesNotLoadIsAnError() {
	s.replayErr = errors.New("this magus cannot load the base tree")
	_, err := s.profiles.read(context.Background(), toySource())

	s.EqualError(err, "change risk: base c0ffee: this magus cannot load the base tree")
}

func (s *BaseProfilesSuite) TestAnEntryThatRestoresNoProfileIsAnError() {
	s.profiles.replay = func(context.Context, string, coverageSource) error { return nil }
	_, err := s.profiles.read(context.Background(), toySource())

	s.ErrorContains(err, "change risk: the cached `magus run test .` at base c0ffee restored no out/lcov.info")
}

func (s *BaseProfilesSuite) TestRemoveDeletesTheExport() {
	_, err := s.profiles.read(context.Background(), toySource())
	s.Require().NoError(err)
	dir := s.profiles.dir
	s.profiles.remove()
	s.NoDirExists(dir)
}

// FreshProfilesSuite records the profiles one run brought current.
type FreshProfilesSuite struct {
	suite.Suite
	m *Magus
}

func TestFreshProfilesSuite(t *testing.T) { suite.Run(t, new(FreshProfilesSuite)) }

func (s *FreshProfilesSuite) SetupTest() {
	cfg := config.Defaults()
	cfg.Cache.Dir = filepath.Join(s.T().TempDir(), "cache")
	s.m = &Magus{ws: &types.Workspace{Root: s.T().TempDir()}, cfg: cfg}
	s.write(toyLCOV)
}

func (s *FreshProfilesSuite) write(text string) {
	file := filepath.Join(s.m.ws.Root, "out", "lcov.info")
	s.Require().NoError(os.MkdirAll(filepath.Dir(file), 0o755))
	s.Require().NoError(os.WriteFile(file, []byte(text), 0o644))
}

func (s *FreshProfilesSuite) inRun(id string) freshProfiles {
	return s.m.freshProfiles(types.AppendInvocationAncestor(context.Background(), 1, id))
}

func (s *FreshProfilesSuite) TestARecordedProfileIsVouchedForInTheSameRun() {
	s.Require().NoError(s.inRun("run1").record(toySource()))
	s.True(s.inRun("run1").vouches(toySource()))
}

func (s *FreshProfilesSuite) TestAnotherRunVouchesForNothing() {
	s.Require().NoError(s.inRun("run1").record(toySource()))
	s.False(s.inRun("run2").vouches(toySource()))
}

func (s *FreshProfilesSuite) TestAProfileEditedSinceGoesUnvouched() {
	s.Require().NoError(s.inRun("run1").record(toySource()))
	s.write("SF:" + scratchFile + "\nDA:4,0\nend_of_record\n")
	s.False(s.inRun("run1").vouches(toySource()))
}

func (s *FreshProfilesSuite) TestOutsideARunNothingIsRecorded() {
	outside := s.m.freshProfiles(context.Background())
	s.Require().NoError(outside.record(toySource()))
	s.False(outside.vouches(toySource()))
}

func (s *FreshProfilesSuite) TestARecordPastItsLifeIsRemovedByTheNextRun() {
	old := s.inRun("run1")
	s.Require().NoError(old.record(toySource()))
	stale := time.Now().Add(-freshProfileLife - time.Hour)
	s.Require().NoError(os.Chtimes(old.file, stale, stale))

	s.Require().NoError(s.inRun("run2").record(toySource()))

	s.NoFileExists(old.file)
	s.FileExists(s.inRun("run2").file)
}

func (s *FreshProfilesSuite) TestARecordWithinItsLifeIsKept() {
	s.Require().NoError(s.inRun("run1").record(toySource()))
	s.Require().NoError(s.inRun("run2").record(toySource()))

	s.True(s.inRun("run1").vouches(toySource()))
}

func (s *FreshProfilesSuite) TestPruningAnAbsentDirRemovesNothing() {
	s.NoError(pruneFreshProfiles(filepath.Join(s.T().TempDir(), "absent"), time.Now()))
}
