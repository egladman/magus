package job

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

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
