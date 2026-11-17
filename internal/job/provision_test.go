package job

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

const everyDiskIsLow = int64(1) << 62

// lowDisk reports every disk below the floor, as a real probe does when the path reads.
var lowDisk = Disk{
	Below: func(dir string, floor int64) (int64, bool, error) {
		if _, err := os.Stat(dir); err != nil {
			return 0, false, err
		}
		return 1 << 20, floor > 1<<20, nil
	},
	ReclaimSnippet: "",
}

type ForkDiskFloorSuite struct {
	suite.Suite
	root string
}

func TestForkDiskFloorSuite(t *testing.T) { suite.Run(t, new(ForkDiskFloorSuite)) }

func (s *ForkDiskFloorSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *ForkDiskFloorSuite) TestANewCheckoutOnALowDiskIsRefusedNamingTheCode() {
	err := RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", s.root, filepath.Join(s.root, "new"), everyDiskIsLow)
	s.ErrorIs(err, types.DiskFull)
	s.ErrorContains(err, types.ReclaimDiskCommand)
}

func (s *ForkDiskFloorSuite) TestAnAdoptedCheckoutIsExempt() {
	s.NoError(RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", s.root, s.root, everyDiskIsLow))
}

func (s *ForkDiskFloorSuite) TestAnUnreadableDiskIsRefusedSayingSo() {
	absent := filepath.Join(s.root, "absent")
	err := RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", absent, filepath.Join(s.root, "new"), 1)
	s.ErrorContains(err, "cannot read the disk holding "+absent)
	s.NotErrorIs(err, types.DiskFull)
}

func (s *ForkDiskFloorSuite) TestADiskAboveTheFloorPasses() {
	s.NoError(RefuseForkOnFullDisk(s.T().Context(), lowDisk, "false", s.root, filepath.Join(s.root, "new"), 1))
}

type progressWriterSuite struct {
	suite.Suite
	said     bytes.Buffer
	previous *slog.Logger
}

func TestProgressWriterSuite(t *testing.T) { suite.Run(t, new(progressWriterSuite)) }

func (s *progressWriterSuite) SetupTest() {
	s.previous = slog.Default()
	s.said.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, nil)))
}

func (s *progressWriterSuite) TearDownTest() { slog.SetDefault(s.previous) }

func (s *progressWriterSuite) TestProgressGoesToStderrUnlessQuiet() {
	s.Equal(os.Stderr, progressWriter(s.T().Context()))
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.said, &slog.HandlerOptions{Level: slog.LevelWarn})))
	s.Equal(io.Discard, progressWriter(s.T().Context()))
}

// FailedProvisionSuite pins what a fork says, and leaves, when readying its checkout fails.
type FailedProvisionSuite struct {
	suite.Suite
	repo  *testkit.Repo
	store *Store
}

func TestFailedProvisionSuite(t *testing.T) { suite.Run(t, new(FailedProvisionSuite)) }

func (s *FailedProvisionSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"magus.yaml": ""})
	s.store = NewStore(Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: s.repo.Root()})
}

// failed bootstraps wt, which fails: the forker declares no hook.
func (s *FailedProvisionSuite) failed(wt types.JobWorktree, made bool) error {
	return FailedProvision(s.T().Context(), s.store, wt, made, bootstrapCheckout(s.T().Context(), lowDisk, "false", s.repo.Root(), wt.Root))
}

func (s *FailedProvisionSuite) TestAnAdoptedCheckoutStaysAndTheErrorSaysSo() {
	dir := s.repo.AddWorktree("adopted").Root()

	err := s.failed(types.JobWorktree{Root: dir, Source: s.repo.Root(), Base: s.repo.Head()}, false)

	s.Require().ErrorContains(err, NoCheckoutHook)
	s.NotContains(err.Error(), "removed")
	s.ErrorContains(err, fmt.Sprintf("%s stays as it was, since this fork did not make it", dir))
	s.DirExists(dir)
}

func (s *FailedProvisionSuite) TestACheckoutThisForkMadeIsRemovedAndTheErrorSaysSo() {
	wt, made, err := s.store.ForkWorktree(s.T().Context(), "w", filepath.Join(s.T().TempDir(), "w"), "", "")
	s.Require().NoError(err)
	s.Require().True(made)

	err = s.failed(wt, made)

	s.Require().ErrorContains(err, NoCheckoutHook)
	s.ErrorContains(err, fmt.Sprintf("this fork made %s, so it removed it", wt.Root))
	s.NoDirExists(wt.Root)
}
