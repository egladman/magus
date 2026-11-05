package cache

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type DiskFullSuite struct {
	suite.Suite
	dir string
}

func TestDiskFullSuite(t *testing.T) {
	suite.Run(t, new(DiskFullSuite))
}

func (s *DiskFullSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

const everyDisk = int64(1) << 62

func (s *DiskFullSuite) TestTextAndALowDiskNameTheCommand() {
	got := DiskFullCause("write x: No space left on device", s.dir, everyDisk)
	s.Contains(got, string(types.DiskFull))
	s.Contains(got, types.ReclaimDiskCommand)
}

func (s *DiskFullSuite) TestTextOnAHealthyDiskSaysNothing() {
	s.Empty(DiskFullCause("no space left on device", s.dir, 1))
}

func (s *DiskFullSuite) TestALowDiskWithoutTheTextSaysNothing() {
	s.Empty(DiskFullCause("compile error", s.dir, everyDisk))
}

func (s *DiskFullSuite) TestAnUnreadableDiskIsAnErrorNotAVerdict() {
	_, below, err := DiskBelow(filepath.Join(s.dir, "absent"), everyDisk)
	s.Error(err)
	s.False(below)
}

func (s *DiskFullSuite) TestAnUnreadableDiskNamesItselfInTheFullCause() {
	got := DiskFullCause("no space left on device", filepath.Join(s.dir, "absent"), everyDisk)
	s.Contains(got, "could not check")
	s.NotContains(got, types.ReclaimDiskCommand)
}

func (s *DiskFullSuite) TestZeroFloorIsOff() {
	_, below, err := DiskBelow(s.dir, 0)
	s.Require().NoError(err)
	s.False(below)
}

func (s *DiskFullSuite) TestDiskFullNeedsTheTextAndALowDisk() {
	_, full, err := DiskFull("No space left on device", s.dir, everyDisk)
	s.Require().NoError(err)
	s.True(full)
}

func (s *DiskFullSuite) TestDiskFullWithoutTheTextIsNot() {
	_, full, err := DiskFull("exit status 1", s.dir, everyDisk)
	s.Require().NoError(err)
	s.False(full)
}

func (s *DiskFullSuite) TestDiskFullWithTheFloorOffIsNot() {
	_, full, err := DiskFull("No space left on device", s.dir, 0)
	s.Require().NoError(err)
	s.False(full)
}

func (s *DiskFullSuite) printedFailure(logText string, floor int64) string {
	saved := diskFullFloor
	diskFullFloor = floor
	s.T().Cleanup(func() { diskFullFloor = saved })
	p := filepath.Join(s.dir, "run.log")
	s.Require().NoError(os.WriteFile(p, []byte(logText), 0o600))
	var out bytes.Buffer
	h := NewPrettyHandler(&out, slog.LevelInfo)
	h.printFailure(s.T().Context(), false, failureReport{label: "workspace", project: ".", target: "build", cause: "go exited 1", logPath: p})
	return out.String()
}

func (s *DiskFullSuite) TestAFailedRunOnALowDiskNamesTheCommand() {
	got := s.printedFailure("write x: no space left on device\n", everyDisk)
	s.Contains(got, "[MGS3052] the disk is full")
	s.Contains(got, types.ReclaimDiskCommand)
}

func (s *DiskFullSuite) TestAFailedRunOnAHealthyDiskStaysQuiet() {
	s.NotContains(s.printedFailure("no space left on device\n", 1), "MGS3052")
}

func (s *DiskFullSuite) TestTailReadsOnlyTheEnd() {
	p := filepath.Join(s.dir, "log")
	s.Require().NoError(os.WriteFile(p, []byte(strings.Repeat("a", 100)+"tail"), 0o600))
	s.Equal("tail", fileTail(p, 4))
}

func (s *DiskFullSuite) TestTailOfAMissingLogIsEmpty() {
	s.Empty(fileTail(filepath.Join(s.dir, "absent"), 4))
}

type KickingAdmitterSuite struct {
	suite.Suite
}

func TestKickingAdmitterSuite(t *testing.T) {
	suite.Run(t, new(KickingAdmitterSuite))
}

type countingAdmitter struct {
	MachineAdmitter
	claims int
}

func (c *countingAdmitter) Claim(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	c.claims++
	return types.MachineVerdict{}, nil
}

func (s *KickingAdmitterSuite) TestKicksOnceAcrossClaimsAndNeverHoldsOne() {
	inner, kicks := &countingAdmitter{}, 0
	admit := KickingAdmitter(inner, func() { kicks++ })
	for range 3 {
		_, err := admit.Claim(s.T().Context(), types.MachineClaim{})
		s.Require().NoError(err)
	}
	s.Equal(1, kicks)
	s.Equal(3, inner.claims)
}

func (s *KickingAdmitterSuite) TestATestBinaryStartsNoTick() {
	s.NotPanics(func() { KickDiskTick(filepath.Join(s.T().TempDir(), "magus.test"), ".", 1) })
}
