package cache

import (
	"context"
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

func (s *DiskFullSuite) TestAnUnreadableDiskIsNotBelow() {
	_, below := DiskBelow(filepath.Join(s.dir, "absent"), everyDisk)
	s.False(below)
}

func (s *DiskFullSuite) TestZeroFloorIsOff() {
	_, below := DiskBelow(s.dir, 0)
	s.False(below)
}

func (s *DiskFullSuite) TestTailReadsOnlyTheEnd() {
	p := filepath.Join(s.dir, "log")
	s.Require().NoError(os.WriteFile(p, []byte(strings.Repeat("a", 100)+"tail"), 0o600))
	s.Equal("tail", tailOf(p, 4))
}

func (s *DiskFullSuite) TestTailOfAMissingLogIsEmpty() {
	s.Empty(tailOf(filepath.Join(s.dir, "absent"), 4))
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
